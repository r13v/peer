// Command peer lets two coding agents pair on one Git checkout through a
// shared message store, and shows their rooms in a terminal UI.
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"
)

type session struct {
	ID        string `json:"id"`
	Repo      string `json:"repo"`
	Writer    string `json:"writer"`
	Reader    string `json:"reader"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`
	// EndedReason says why a session ended other than by its writer's end.
	EndedReason string `json:"ended_reason,omitempty"`
}

type message struct {
	ID   string `json:"id"`
	At   string `json:"at"`
	From string `json:"from"`
	To   string `json:"to"`
	Text string `json:"text"`
}

type store struct {
	dir  string
	repo string
}

//go:embed instructions/flow.md
var flowInstructions []byte

//go:embed instructions/reader.md
var readerInstructions []byte

//go:embed instructions/writer.md
var writerInstructions []byte

const usage = "usage: peer, peer --version, peer update, peer skills flow|writer|reader, peer start NAME, peer send|wait|end ID --as NAME, peer status [ID], peer history, or peer log ID"

// version is set at release build time.
var version = "dev"

// openURL is replaced in tests.
var openURL = func(link string) error { return exec.Command("open", link).Run() }

// waitTimeout bounds one wait call below Claude Code's two-minute Bash
// limit; it is replaced in tests.
var waitTimeout = 90 * time.Second

const (
	// human is the author of messages sent from the peer TUI; new rooms
	// refuse it as a participant name.
	human = "user"
	// everyone addresses a message to every participant of a room.
	everyone = "*"
)

func main() {
	cwd, err := os.Getwd()
	if err == nil {
		err = run(os.Args[1:], os.Stdin, os.Stdout, cwd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "peer:", err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out io.Writer, cwd string) error {
	if len(args) == 0 {
		return picker(in, out, cwd)
	}
	if args[0] == "--version" || args[0] == "version" {
		if len(args) != 1 {
			return errors.New("usage: peer --version")
		}
		_, err := fmt.Fprintln(out, version)
		return err
	}
	if args[0] == "update" {
		if len(args) != 1 {
			return errors.New("usage: peer update")
		}
		return update(in, out)
	}
	if args[0] == "skills" {
		docs := map[string][]byte{"flow": flowInstructions, "writer": writerInstructions, "reader": readerInstructions}
		if len(args) != 2 || docs[args[1]] == nil {
			return errors.New("usage: peer skills flow|writer|reader")
		}
		_, err := out.Write(docs[args[1]])
		return err
	}
	s, err := openStore(cwd)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	actor := fs.String("as", "", "participant name")
	writer := fs.String("writer", "", "writer name for start")
	reader := fs.String("reader", "", "reader name for start")
	headed := fs.Bool("headed", false, "open the reader's desktop app for start")
	// The room name or ID comes first, as in peer send ID --as NAME; Go's
	// flag parsing would stop at it, so it is taken off before the flags.
	rest, id := args[1:], ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		id, rest = rest[0], rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	needID := func(cmd string) error {
		if id == "" {
			return fmt.Errorf("usage: peer %s ID; list active rooms with peer status", cmd)
		}
		return nil
	}
	switch args[0] {
	case "log":
		if err := needID("log"); err != nil {
			return err
		}
		return s.log(id, newPrinter(out, s.repo))
	case "start":
		if !roomName.MatchString(id) {
			return errors.New("usage: peer start NAME --writer NAME --reader NAME; the room NAME is 1-40 characters: a-z, 0-9 or -, starting with a letter")
		}
		for _, name := range []string{*writer, *reader} {
			if err := checkName(name); err != nil {
				return err
			}
			if name == human {
				return fmt.Errorf("%q is reserved for messages from the peer TUI", human)
			}
		}
		if *writer == *reader {
			return errors.New("writer and reader must be different participants")
		}
		v, err := s.start(id, *writer, *reader, out)
		if err != nil || appBundles[*reader] == "" {
			return err
		}
		if *headed && appRunning(*reader) {
			link := readerLink(s.repo, v)
			if err := openURL(link); err != nil {
				return fmt.Errorf("session started, but opening %s failed: %w; open this link: %s", *reader, err, link)
			}
			return nil
		}
		if *headed {
			why := *reader + " is not open"
			if runtime.GOOS != "darwin" {
				why = "desktop chats open only on macOS"
			}
			fmt.Fprintf(os.Stderr, "peer: %s, so %s runs headless\n", why, *reader)
		}
		logPath := filepath.Join(s.dir, "sessions", v.ID, "reader.log")
		if err := startReader(readerArgs(s, v), s.repo, logPath); err != nil {
			return fmt.Errorf("session started, but launching %s failed: %w; open %s in this checkout and send: %s", *reader, err, *reader, readerPrompt(v, false))
		}
		fmt.Fprintf(os.Stderr, "peer: %s runs headless; watch it in peer, or read %s\n", *reader, logPath)
		return nil
	case "status":
		return s.status(id, out)
	case "send":
		if err := needID("send"); err != nil {
			return err
		}
		if err := checkName(*actor); err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(in, 64*1024+1))
		if err != nil {
			return err
		}
		text, err := messageText(string(body))
		if err != nil {
			return err
		}
		return s.send(id, *actor, text, out)
	case "wait":
		if err := needID("wait"); err != nil {
			return err
		}
		if err := checkName(*actor); err != nil {
			return err
		}
		return s.wait(id, *actor, waitTimeout, out)
	case "end":
		if err := needID("end"); err != nil {
			return err
		}
		if err := checkName(*actor); err != nil {
			return err
		}
		return s.end(id, *actor, out)
	case "history":
		return s.history(out)
	default:
		return errors.New(usage)
	}
}

// update reruns the latest release's installer over this binary.
func update(in io.Reader, out io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	target, err := installDir(executable)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "peer-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "install.sh")
	cmd := exec.Command("curl", "-fsSL", "https://github.com/r13v/peer/releases/latest/download/install.sh", "-o", script)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download latest installer: %w", err)
	}
	cmd = exec.Command("sh", script)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "PEER_INSTALL_DIR="+target)
	return cmd.Run()
}

// installDir is the directory update replaces executable in. Homebrew owns
// its copies, so replacing one would leave brew with a version it did not
// install.
func installDir(executable string) (string, error) {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	for _, part := range strings.Split(filepath.ToSlash(resolved), "/") {
		if part == "Caskroom" || part == "Cellar" {
			return "", errors.New("installed with Homebrew; update it with brew upgrade --cask peer")
		}
	}
	return filepath.Dir(resolved), nil
}

// readerPrompt starts the reader's chat. A headless reader has nobody to
// ask, so it is told to keep waiting on its own.
func readerPrompt(v session, headless bool) string {
	prompt := fmt.Sprintf("Use the peer skill. You are participant %s, the reader in %s's peer room %s in this checkout. Discuss the approach through peer, then review the diff and send concrete findings through peer. Do not edit files. Keep waiting for replies until the review is closed.", v.Reader, v.Writer, v.ID)
	if headless {
		prompt += " Nobody reads this chat: do not ask the user anything, run peer and git directly rather than through wrapper commands, and keep calling peer wait until the writer ends the session. Once peer reports that the session has ended, stop."
	}
	return prompt
}

// appBundles maps each reader with a desktop app to its macOS bundle ID.
var appBundles = map[string]string{"codex": "com.openai.codex", "claude": "com.anthropic.claudefordesktop"}

// appRunning reports whether reader's desktop app is open without
// launching it; it is replaced in tests. The desktop apps and their deep
// links exist only on macOS, so other systems report false and run the
// reader headless.
var appRunning = func(reader string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	out, err := exec.Command("osascript", "-e", `application id "`+appBundles[reader]+`" is running`).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// readerArgs runs the reader's CLI without a chat window. Codex's sandbox
// also lets it write the peer store outside the checkout; Claude gets no
// edit tools and only peer and read-only git in Bash.
func readerArgs(s *store, v session) []string {
	prompt := readerPrompt(v, true)
	if v.Reader == "codex" {
		return []string{"codex", "exec", "-C", s.repo, "-s", "workspace-write", "--add-dir", s.dir, "-c", "approval_policy=never", prompt}
	}
	return []string{"claude", "-p", "--verbose", "--output-format", "stream-json", "--permission-mode", "dontAsk", "--permission-prompts", "none", "--tools", "Bash", "Read", "Grep", "Glob", "Skill", "--allowedTools", "Skill", "Bash(peer:*)", "Bash(git diff:*)", "Bash(git status:*)", "Bash(git log:*)", "Bash(git show:*)", "Read", "Grep", "Glob", "--", prompt}
}

// startReader runs argv in dir in its own process session, so it outlives
// the writer's command, and appends its output and exit status to logPath.
// The status also goes to reader.exit next to it, which ends the room; it
// is replaced in tests.
var startReader = func(argv []string, dir, logPath string) error {
	if _, err := exec.LookPath(argv[0]); err != nil {
		return err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	exit := filepath.Join(filepath.Dir(logPath), readerExit)
	cmd := exec.Command("sh", append([]string{"-c", `exit_file=$1; shift; "$@"; status=$?; echo "[peer] reader exited with status $status"; echo "$status" > "$exit_file"`, "sh", exit}, argv...)...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// readerLink builds a desktop deep link that opens a new codex or claude
// chat in repo with the reader prompt prefilled. Neither app submits the prompt by itself.
func readerLink(repo string, v session) string {
	prompt := readerPrompt(v, false)
	if v.Reader == "codex" {
		return (&url.URL{Scheme: "codex", Host: "threads", Path: "/new", RawQuery: url.Values{"path": {repo}, "prompt": {prompt}}.Encode()}).String()
	}
	return (&url.URL{Scheme: "claude", Host: "code", Path: "/new", RawQuery: url.Values{"folder": {repo}, "q": {prompt}}.Encode()}).String()
}

func checkName(name string) error {
	if len(name) == 0 || len(name) > 64 {
		return errors.New("participant name must be 1-64 characters: a-z, 0-9, - or _, starting with a letter")
	}
	for i, c := range name {
		if c >= 'a' && c <= 'z' || i > 0 && (c >= '0' && c <= '9' || c == '-' || c == '_') {
			continue
		}
		return errors.New("participant name must be 1-64 characters: a-z, 0-9, - or _, starting with a letter")
	}
	return nil
}

// messageText trims a message body and checks that it can be sent.
func messageText(body string) (string, error) {
	if len(body) > 64*1024 || !utf8.ValidString(body) {
		return "", errors.New("message must be UTF-8 and at most 64 KiB")
	}
	text := strings.TrimSpace(body)
	if text == "" {
		return "", errors.New("message is empty; pipe its text to stdin")
	}
	return text, nil
}

// members lists the room's participants in the order the TUI offers them.
func (s session) members() []string { return []string{s.Writer, s.Reader} }

func (s session) other(name string) (string, error) {
	if name == s.Writer {
		return s.Reader, nil
	}
	if name == s.Reader {
		return s.Writer, nil
	}
	return "", fmt.Errorf("%q is not a participant in session %s", name, s.ID)
}

func openStore(cwd string) (*store, error) {
	repo, err := repoRoot(cwd)
	if err != nil {
		return nil, err
	}
	repos, err := reposDir()
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(repo))
	dir := filepath.Join(repos, repoSlug(repo)+"-"+hex.EncodeToString(key[:8]))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &store{dir: dir, repo: repo}, nil
}

func repoRoot(cwd string) (string, error) {
	root, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", errors.New("run this command inside the shared Git checkout")
	}
	return filepath.EvalSymlinks(strings.TrimSpace(string(root)))
}

// reposDir holds one store per checkout under PEER_HOME, by default ~/.peer.
func reposDir() (string, error) {
	home := os.Getenv("PEER_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, ".peer")
	}
	repos := filepath.Join(home, "repos")
	return repos, os.MkdirAll(repos, 0700)
}

// repoSlug turns the checkout's directory name into a readable,
// filesystem-safe prefix for its store directory.
func repoSlug(repo string) string {
	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(filepath.Base(repo)))
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if slug = strings.Trim(slug, ".-"); slug == "" {
		return "repo"
	}
	return slug
}

func (s *store) locked(fn func() error) error {
	return withLock(filepath.Join(s.dir, ".lock"), fn)
}

func withLock(path string, fn func() error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func newID() (string, error) {
	var b [8]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}

func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".peer-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'))
}

var (
	// roomName is the name the writer gives a room at start.
	roomName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	// sessionID is a room name, with -2, -3… if the name was taken.
	sessionID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}(-\d+)?$`)
)

// readerExit holds the headless reader's exit status once it stops.
const readerExit = "reader.exit"

// session reads session id without ending it; participants use refresh.
func (s *store) session(id string) (session, error) {
	if !sessionID.MatchString(id) {
		return session{}, errors.New("invalid session ID")
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "sessions", id, "session.json"))
	if errors.Is(err, os.ErrNotExist) {
		return session{}, fmt.Errorf("no session %s; list active rooms with peer status", id)
	}
	if err != nil {
		return session{}, err
	}
	var v session
	err = json.Unmarshal(b, &v)
	return v, err
}

// load reads session id under the store lock and ends it once its
// headless reader has exited, so nobody waits on a reader that is gone.
func (s *store) load(id string) (session, error) {
	v, err := s.session(id)
	if err != nil || v.EndedAt != "" {
		return v, err
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "sessions", id, readerExit))
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	status, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return v, nil // the wrapper is still writing it
	}
	return v, s.finish(&v, fmt.Sprintf("%s exited with status %d", v.Reader, status))
}

// refresh is load for callers that do not hold the store lock.
func (s *store) refresh(id string) (session, error) {
	var v session
	err := s.locked(func() error {
		var err error
		v, err = s.load(id)
		return err
	})
	return v, err
}

// finish ends active session v; the caller holds the store lock.
func (s *store) finish(v *session, reason string) error {
	v.EndedAt, v.EndedReason = time.Now().UTC().Format(time.RFC3339Nano), reason
	return writeJSON(filepath.Join(s.dir, "sessions", v.ID, "session.json"), v)
}

func (s *store) start(name, writer, reader string, out io.Writer) (session, error) {
	var v session
	err := s.locked(func() error {
		if err := os.MkdirAll(filepath.Join(s.dir, "sessions"), 0700); err != nil {
			return err
		}
		id, dir := name, ""
		for n := 2; ; n++ {
			dir = filepath.Join(s.dir, "sessions", id)
			err := os.Mkdir(dir, 0700)
			if err == nil {
				break
			}
			if !errors.Is(err, os.ErrExist) {
				return err
			}
			id = fmt.Sprintf("%s-%d", name, n)
		}
		v = session{ID: id, Repo: s.repo, Writer: writer, Reader: reader, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if err := writeJSON(filepath.Join(dir, "session.json"), v); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(v)
	})
	return v, err
}

// status prints room id, or every active room in this checkout.
func (s *store) status(id string, out io.Writer) error {
	if id != "" {
		v, err := s.refresh(id)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(v)
	}
	sessions, err := s.sessions()
	if err != nil {
		return err
	}
	for _, v := range sessions {
		if v.EndedAt == "" {
			if err := json.NewEncoder(out).Encode(v); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *store) send(sid, from, text string, out io.Writer) error {
	return s.locked(func() error {
		v, err := s.load(sid)
		if err != nil {
			return err
		}
		if v.EndedAt != "" {
			return endedError(v)
		}
		to, err := v.other(from)
		if err != nil {
			return err
		}
		m, err := s.appendMessage(v, from, to, text)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(m)
	})
}

// post sends text from the human to one participant of an active room,
// or to all of them when to is everyone.
func (s *store) post(sid, to, text string) error {
	text, err := messageText(text)
	if err != nil {
		return err
	}
	return s.locked(func() error {
		v, err := s.load(sid)
		if err != nil {
			return err
		}
		if v.EndedAt != "" {
			return endedError(v)
		}
		if to != everyone && !slices.Contains(v.members(), to) {
			return fmt.Errorf("%q is not a participant in session %s", to, v.ID)
		}
		_, err = s.appendMessage(v, human, to, text)
		return err
	})
}

// appendMessage adds a message to v's transcript; the caller holds the
// store lock.
func (s *store) appendMessage(v session, from, to, text string) (message, error) {
	id, err := newID()
	if err != nil {
		return message{}, err
	}
	m := message{ID: id, At: time.Now().UTC().Format(time.RFC3339Nano), From: from, To: to, Text: text}
	b, err := json.Marshal(m)
	if err != nil {
		return message{}, err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "sessions", v.ID, "messages.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return message{}, err
	}
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return message{}, err
	}
	return m, closeErr
}

func (s *store) nextMessage(sid, as string) (*message, error) {
	var found *message
	err := s.locked(func() error {
		v, err := s.load(sid)
		if err != nil {
			return err
		}
		if _, err := v.other(as); err != nil {
			return err
		}
		dir := filepath.Join(s.dir, "sessions", v.ID)
		cursorPath := filepath.Join(dir, "cursor-"+as)
		var offset int64
		if b, err := os.ReadFile(cursorPath); err == nil {
			offset, err = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
			if err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		f, err := os.Open(filepath.Join(dir, "messages.jsonl"))
		if errors.Is(err, os.ErrNotExist) {
			if v.EndedAt != "" {
				return endedError(v)
			}
			// Record the poll so log can show this participant as waiting.
			return writeAtomic(cursorPath, []byte("0\n"))
		}
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		r := bufio.NewReader(f)
		for {
			line, err := r.ReadBytes('\n')
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			offset += int64(len(line))
			var m message
			if err := json.Unmarshal(line, &m); err != nil {
				return err
			}
			if m.To == as || m.To == everyone {
				found = &m
				break
			}
		}
		if found == nil && v.EndedAt != "" {
			return endedError(v)
		}
		return writeAtomic(cursorPath, []byte(strconv.FormatInt(offset, 10)+"\n"))
	})
	return found, err
}

func (s *store) wait(sid, as string, timeout time.Duration, out io.Writer) error {
	deadline := time.Now().Add(timeout)
	for {
		m, err := s.nextMessage(sid, as)
		if err != nil {
			return err
		}
		if m != nil {
			return json.NewEncoder(out).Encode(struct {
				Status  string  `json:"status"`
				Message message `json:"message"`
			}{"message", *m})
		}
		if !time.Now().Before(deadline) {
			return json.NewEncoder(out).Encode(map[string]string{"status": "timeout"})
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *store) end(sid, as string, out io.Writer) error {
	return s.locked(func() error {
		v, err := s.load(sid)
		if err != nil {
			return err
		}
		if v.Writer != as {
			return errors.New("only the writer can end this session")
		}
		if v.EndedAt != "" {
			return errors.New("session has already ended")
		}
		if err := s.finish(&v, ""); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(v)
	})
}

// close ends room id without a participant, as x in the picker does.
func (s *store) close(id string) error {
	return s.locked(func() error {
		v, err := s.load(id)
		if err != nil || v.EndedAt != "" {
			return err
		}
		return s.finish(&v, "closed in peer")
	})
}

func endedError(v session) error {
	if v.EndedReason != "" {
		return errors.New("session has ended: " + v.EndedReason)
	}
	return errors.New("session has ended")
}

// log prints session id's transcript.
func (s *store) log(id string, p *printer) error {
	v, err := s.refresh(id)
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(s.dir, "sessions", id, "messages.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			return err
		}
		if err := p.message(v, m); err != nil {
			return err
		}
	}
}

const (
	ansiReset   = "\x1b[0m"
	ansiDim     = "\x1b[2m"
	ansiWriter  = "\x1b[1;36m"
	ansiReader  = "\x1b[1;35m"
	ansiHuman   = "\x1b[1;33m"
	ansiLinkEnd = "\x1b]8;;\x1b\\"
)

var (
	// pathRef matches file-like tokens such as main.go, ui/src/a.ts:42 or /abs/b.go:3:7.
	pathRef    = regexp.MustCompile(`(?:/|\.{1,2}/)?(?:[\w.@-]+/)*[\w@-][\w.@-]*\.[A-Za-z]\w*(?::(\d+))?(?::\d+)?`)
	codeSpan   = regexp.MustCompile("`[^`\n]+`")
	boldSpan   = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	listMarker = regexp.MustCompile(`^(\s*)([-*]|\d+\.)(\s)`)
)

// notify shows a desktop notification; it is replaced in tests.
var notify = func(title, text string) {
	if runtime.GOOS != "darwin" {
		return
	}
	// Pass text as arguments so it is never parsed as AppleScript.
	_ = exec.Command("osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", title, text).Run()
}

// printer renders the transcript for people. Colors and links need a
// terminal, so redirected logs stay plain; NO_COLOR turns them off.
type printer struct {
	out   io.Writer
	repo  string
	color bool
	day   string
}

func newPrinter(out io.Writer, repo string) *printer {
	f, ok := out.(*os.File)
	tty := ok && term.IsTerminal(f.Fd())
	color := tty && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	return &printer{out: out, repo: repo, color: color}
}

func (p *printer) paint(code, text string) string {
	if !p.color {
		return text
	}
	return code + text + ansiReset
}

func (p *printer) message(v session, m message) error {
	at, err := time.Parse(time.RFC3339Nano, m.At)
	if err != nil {
		return err
	}
	at = at.Local()
	if day := at.Format("Mon, 2 Jan 2006"); day != p.day {
		p.day = day
		if _, err := fmt.Fprintf(p.out, "%s\n\n", p.paint(ansiDim, "── "+day+" ──")); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(p.out, "%s  %s %s %s\n%s\n\n",
		p.paint(ansiDim, at.Format("15:04:05")),
		p.author(v, m.From), p.paint(ansiDim, "→"), p.author(v, m.To),
		p.body(m.Text))
	return err
}

func (p *printer) author(v session, name string) string {
	switch name {
	case everyone:
		return "all"
	case human:
		return p.paint(ansiHuman, name)
	}
	if name == v.Writer {
		return p.paint(ansiWriter, name)
	}
	return p.paint(ansiReader, name)
}

// body indents the text and highlights `code`, **bold** and list markers.
func (p *printer) body(text string) string {
	if !p.color {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		line = listMarker.ReplaceAllString(line, "$1\x1b[2m$2\x1b[22m$3")
		line = codeSpan.ReplaceAllStringFunc(line, func(c string) string { return "\x1b[33m" + c + "\x1b[39m" })
		line = boldSpan.ReplaceAllString(line, "\x1b[1m$1\x1b[22m")
		lines[i] = "  " + p.links(line)
	}
	return strings.Join(lines, "\n")
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// links wraps paths to existing files in OSC 8 hyperlinks. PEER_EDITOR_URL
// sets the target, e.g. vscode://file/{path}:{line}; the default is file://{path}.
func (p *printer) links(text string) string {
	if !p.color {
		return text
	}
	tmpl := os.Getenv("PEER_EDITOR_URL")
	if tmpl == "" {
		tmpl = "file://{path}"
	}
	return pathRef.ReplaceAllStringFunc(text, func(ref string) string {
		file, line, _ := strings.Cut(ref, ":")
		line, _, _ = strings.Cut(line, ":")
		if !filepath.IsAbs(file) {
			file = filepath.Join(p.repo, file)
		}
		if st, err := os.Stat(file); err != nil || st.IsDir() {
			return ref
		}
		if line == "" {
			line = "1"
		}
		target := strings.NewReplacer("{path}", (&url.URL{Path: file}).EscapedPath(), "{line}", line).Replace(tmpl)
		return "\x1b]8;;" + target + "\x1b\\" + ref + ansiLinkEnd
	})
}

// sessions returns this checkout's sessions, newest first.
func (s *store) sessions() ([]session, error) {
	dirs, err := os.ReadDir(filepath.Join(s.dir, "sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sessions []session
	for _, d := range dirs {
		// Skip directories that are not sessions, such as ones left by
		// older versions, and damaged ones, so the rest stay listed.
		if !d.IsDir() || !sessionID.MatchString(d.Name()) {
			continue
		}
		v, err := s.session(d.Name())
		if err == nil && v.EndedAt == "" {
			v, err = s.refresh(d.Name())
		}
		if err != nil {
			continue
		}
		sessions = append(sessions, v)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].StartedAt > sessions[j].StartedAt })
	return sessions, nil
}

// count returns the number of messages in session id.
func (s *store) count(id string) int {
	b, _ := os.ReadFile(filepath.Join(s.dir, "sessions", id, "messages.jsonl"))
	return bytes.Count(b, []byte("\n"))
}

// summary describes a session in one line, e.g.
// "Tue 29 Sep 19:59  claude→codex  12 msgs  8m".
func (s *store) summary(v session) string {
	start, _ := time.Parse(time.RFC3339Nano, v.StartedAt)
	end := time.Now()
	if v.EndedAt != "" {
		end, _ = time.Parse(time.RFC3339Nano, v.EndedAt)
	}
	return fmt.Sprintf("%s  %s→%s  %3d msgs  %s", start.Local().Format("Mon _2 Jan 15:04"), v.Writer, v.Reader, s.count(v.ID), humanDuration(end.Sub(start)))
}

func (s *store) history(out io.Writer) error {
	sessions, err := s.sessions()
	if err != nil {
		return err
	}
	for _, v := range sessions {
		state := "ended"
		if v.EndedAt == "" {
			state = "active"
		} else if v.EndedReason != "" {
			state += ": " + v.EndedReason
		}
		if _, err := fmt.Fprintf(out, "%-24s  %s  %s\n", v.ID, s.summary(v), state); err != nil {
			return err
		}
	}
	return nil
}
