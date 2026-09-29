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
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type session struct {
	ID        string `json:"id"`
	Repo      string `json:"repo"`
	Writer    string `json:"writer"`
	Reader    string `json:"reader"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`
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

var errNoSession = errors.New("no session; the writer must run peer start")

const usage = "usage: peer, peer skills flow|writer|reader, peer update, peer start|status|send|wait|end|follow|history, or peer log ID"

// openURL is replaced in tests.
var openURL = func(link string) error { return exec.Command("open", link).Run() }

// waitTimeout bounds one wait call below Claude Code's two-minute Bash
// limit; it is replaced in tests.
var waitTimeout = 90 * time.Second

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
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if args[0] == "log" {
		if fs.NArg() != 1 {
			return errors.New("usage: peer log ID; list IDs with peer history")
		}
		return s.log(fs.Arg(0), false, newPrinter(out, s.repo, false), nil)
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	switch args[0] {
	case "start":
		if err := checkName(*writer); err != nil {
			return err
		}
		if err := checkName(*reader); err != nil {
			return err
		}
		if *writer == *reader {
			return errors.New("writer and reader must be different participants")
		}
		if err := s.start(*writer, *reader, out); err != nil || appBundles[*reader] == "" {
			return err
		}
		if *headed && appRunning(*reader) {
			link := readerLink(s.repo, *writer, *reader)
			if err := openURL(link); err != nil {
				return fmt.Errorf("session started, but opening %s failed: %v; open this link: %s", *reader, err, link)
			}
			return nil
		}
		if *headed {
			fmt.Fprintf(os.Stderr, "peer: %s is not open, so it runs headless\n", *reader)
		}
		v, err := s.active()
		if err == nil {
			logPath := filepath.Join(s.dir, "sessions", v.ID, "reader.log")
			if err = startReader(readerArgs(s, *writer, *reader), s.repo, logPath, v.ID); err == nil {
				fmt.Fprintf(os.Stderr, "peer: %s runs headless; press Tab in peer to watch it, or read %s\n", *reader, logPath)
			}
		}
		if err != nil {
			return fmt.Errorf("session started, but launching %s failed: %v; open %s in this checkout and send: %s", *reader, err, *reader, readerPrompt(*writer, *reader, false))
		}
		return nil
	case "status":
		return s.status(out)
	case "send":
		if err := checkName(*actor); err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(in, 64*1024+1))
		if err != nil {
			return err
		}
		if len(body) > 64*1024 || !utf8.Valid(body) {
			return errors.New("message must be UTF-8 and at most 64 KiB")
		}
		text := strings.TrimSpace(string(body))
		if text == "" {
			return errors.New("message is empty; pipe its text to stdin")
		}
		return s.send(*actor, text, out)
	case "wait":
		if err := checkName(*actor); err != nil {
			return err
		}
		return s.wait(*actor, waitTimeout, out)
	case "end":
		if err := checkName(*actor); err != nil {
			return err
		}
		return s.end(*actor, out)
	case "follow":
		p := newPrinter(out, s.repo, true)
		id, err := s.awaitSession(p)
		if err != nil {
			return err
		}
		return s.log(id, true, p, nil)
	case "history":
		return s.history(out)
	default:
		return errors.New(usage)
	}
}

func update(in io.Reader, out io.Writer) error {
	dir, err := os.MkdirTemp("", "peer-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "install.sh")
	cmd := exec.Command("curl", "-fsSL", "https://github.com/r13v/peer/releases/download/latest/install.sh", "-o", script)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download latest installer: %w", err)
	}
	cmd = exec.Command("sh", script)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	cmd.Env = append(os.Environ(), "PEER_INSTALL_DIR="+filepath.Dir(executable))
	return cmd.Run()
}

// readerPrompt starts the reader's chat. A headless reader has nobody to
// ask, so it is told to keep waiting on its own.
func readerPrompt(writer, reader string, headless bool) string {
	prompt := fmt.Sprintf("Use the peer skill. You are participant %s, the reader in %s's peer session in this checkout. Discuss the approach through peer, then review the diff and send concrete findings through peer. Do not edit files. Keep waiting for replies until the review is closed.", reader, writer)
	if headless {
		prompt += " Nobody reads this chat: do not ask the user anything, run peer and git directly rather than through wrapper commands, and keep calling peer wait until the writer ends the session. Once peer reports that the session has ended, stop."
	}
	return prompt
}

// appBundles maps each reader with a desktop app to its macOS bundle ID.
var appBundles = map[string]string{"codex": "com.openai.codex", "claude": "com.anthropic.claudefordesktop"}

// appRunning reports whether reader's desktop app is open without
// launching it; it is replaced in tests. Only macOS can tell, so other
// systems report true and --headed opens the link as before.
var appRunning = func(reader string) bool {
	if runtime.GOOS != "darwin" {
		return true
	}
	out, err := exec.Command("osascript", "-e", `application id "`+appBundles[reader]+`" is running`).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// readerArgs runs the reader's CLI without a chat window. Codex's sandbox
// also lets it write the peer store outside the checkout; Claude gets no
// edit tools and only peer and read-only git in Bash.
func readerArgs(s *store, writer, reader string) []string {
	prompt := readerPrompt(writer, reader, true)
	if reader == "codex" {
		return []string{"codex", "exec", "-C", s.repo, "-s", "workspace-write", "--add-dir", s.dir, "-c", "approval_policy=never", prompt}
	}
	return []string{"claude", "-p", "--verbose", "--output-format", "stream-json", "--permission-mode", "dontAsk", "--permission-prompts", "none", "--tools", "Bash", "Read", "Grep", "Glob", "Skill", "--allowedTools", "Skill", "Bash(peer:*)", "Bash(git diff:*)", "Bash(git status:*)", "Bash(git log:*)", "Bash(git show:*)", "Read", "Grep", "Glob", "--", prompt}
}

// startReader runs argv in dir in its own process session, so it outlives
// the writer's command, pins it to peer session id, and appends its output
// and exit status to logPath; it is replaced in tests.
var startReader = func(argv []string, dir, logPath, id string) error {
	if _, err := exec.LookPath(argv[0]); err != nil {
		return err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command("sh", append([]string{"-c", `"$@"; echo "[peer] reader exited with status $?"`, "sh"}, argv...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PEER_SESSION="+id)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// readerLink builds a desktop deep link that opens a new codex or claude
// chat in repo with the reader prompt prefilled. Neither app submits the prompt by itself.
func readerLink(repo, writer, reader string) string {
	prompt := readerPrompt(writer, reader, false)
	if reader == "codex" {
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
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
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

func (s *store) activeID() (string, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "active"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errNoSession
		}
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// sessionID matches IDs made from the local start time, such as
// 20260929-120911 or 20260929-120911-2 for a second session in that second.
var sessionID = regexp.MustCompile(`^\d{8}-\d{6}(-\d+)?$`)

func (s *store) session(id string) (session, error) {
	if !sessionID.MatchString(id) {
		return session{}, errors.New("invalid session ID")
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "sessions", id, "session.json"))
	if err != nil {
		return session{}, err
	}
	var v session
	err = json.Unmarshal(b, &v)
	return v, err
}

// current returns the active session, or the one PEER_SESSION names. A
// headless reader is pinned this way, so once its session ends it cannot
// join the next one and take messages meant for another participant.
func (s *store) current() (session, error) {
	if id := os.Getenv("PEER_SESSION"); id != "" {
		return s.session(id)
	}
	return s.active()
}

// active returns the active session regardless of PEER_SESSION.
func (s *store) active() (session, error) {
	id, err := s.activeID()
	if err != nil {
		return session{}, err
	}
	return s.session(id)
}

func (s *store) start(writer, reader string, out io.Writer) error {
	return s.locked(func() error {
		old, err := s.active() // PEER_SESSION may pin an older session
		if err == nil && old.EndedAt == "" {
			return fmt.Errorf("session %s is active; end it before starting another", old.ID)
		}
		if err != nil && !errors.Is(err, errNoSession) {
			return err
		}
		now := time.Now()
		if err := os.MkdirAll(filepath.Join(s.dir, "sessions"), 0700); err != nil {
			return err
		}
		base := now.Format("20060102-150405")
		id, dir := base, ""
		for n := 2; ; n++ {
			dir = filepath.Join(s.dir, "sessions", id)
			err := os.Mkdir(dir, 0700)
			if err == nil {
				break
			}
			if !errors.Is(err, os.ErrExist) {
				return err
			}
			id = fmt.Sprintf("%s-%d", base, n)
		}
		v := session{ID: id, Repo: s.repo, Writer: writer, Reader: reader, StartedAt: now.UTC().Format(time.RFC3339Nano)}
		if err := writeJSON(filepath.Join(dir, "session.json"), v); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(s.dir, "active"), []byte(id+"\n")); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(v)
	})
}

func (s *store) status(out io.Writer) error {
	return s.locked(func() error {
		v, err := s.current()
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(v)
	})
}

func (s *store) send(from, text string, out io.Writer) error {
	return s.locked(func() error {
		v, err := s.current()
		if err != nil {
			return err
		}
		if v.EndedAt != "" {
			return errors.New("session has ended")
		}
		to, err := v.other(from)
		if err != nil {
			return err
		}
		id, err := newID()
		if err != nil {
			return err
		}
		m := message{ID: id, At: time.Now().UTC().Format(time.RFC3339Nano), From: from, To: to, Text: text}
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(s.dir, "sessions", v.ID, "messages.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if _, err = f.Write(append(b, '\n')); err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		return json.NewEncoder(out).Encode(m)
	})
}

func (s *store) nextMessage(as string) (*message, error) {
	var found *message
	err := s.locked(func() error {
		v, err := s.current()
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
				return errors.New("session has ended")
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
			if m.To == as {
				found = &m
				break
			}
		}
		if found == nil && v.EndedAt != "" {
			return errors.New("session has ended")
		}
		return writeAtomic(cursorPath, []byte(strconv.FormatInt(offset, 10)+"\n"))
	})
	return found, err
}

func (s *store) wait(as string, timeout time.Duration, out io.Writer) error {
	deadline := time.Now().Add(timeout)
	for {
		m, err := s.nextMessage(as)
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

func (s *store) end(as string, out io.Writer) error {
	return s.locked(func() error {
		v, err := s.current()
		if err != nil {
			return err
		}
		if v.Writer != as {
			return errors.New("only the writer can end this session")
		}
		if v.EndedAt != "" {
			return errors.New("session has already ended")
		}
		v.EndedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := writeJSON(filepath.Join(s.dir, "sessions", v.ID, "session.json"), v); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(v)
	})
}

// log prints session id's transcript. When following, it streams new
// messages until the session ends or stop is closed.
func (s *store) log(id string, follow bool, p *printer, stop <-chan struct{}) error {
	if _, err := s.session(id); err != nil {
		return err
	}
	dir := filepath.Join(s.dir, "sessions", id)
	var offset int64
	for {
		// Read the session before the transcript: send refuses after end,
		// so an ended session has no messages beyond what we drain next.
		v, err := s.session(id)
		if err != nil {
			return err
		}
		f, err := os.Open(filepath.Join(dir, "messages.jsonl"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				f.Close()
				return err
			}
			r := bufio.NewReader(f)
			for {
				line, err := r.ReadBytes('\n')
				if err == io.EOF {
					break
				}
				if err != nil {
					f.Close()
					return err
				}
				offset += int64(len(line))
				var m message
				if err := json.Unmarshal(line, &m); err != nil {
					f.Close()
					return err
				}
				if err := p.message(v, m); err != nil {
					f.Close()
					return err
				}
			}
			f.Close()
		}
		if !follow {
			return nil
		}
		if v.EndedAt != "" {
			return p.ended(v)
		}
		p.status(v, dir)
		select {
		case <-stop:
			return p.clearStatus()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// awaitSession returns the active session ID. It waits for a new session
// instead of failing or replaying an ended one. Any session that replaces
// the stale one counts, even if it ended between polls.
func (s *store) awaitSession(p *printer) (string, error) {
	stale, first, notified := "", true, false
	for {
		v, err := s.current()
		if err != nil && !errors.Is(err, errNoSession) {
			return "", err
		}
		if first {
			stale, first = v.ID, false
		}
		if err == nil && (v.EndedAt == "" || v.ID != stale) {
			return v.ID, nil
		}
		if !notified {
			notified = true
			if _, err := fmt.Fprintf(p.out, "%s\n\n", p.paint(ansiDim, "waiting for a session to start…")); err != nil {
				return "", err
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

const (
	ansiReset   = "\x1b[0m"
	ansiDim     = "\x1b[2m"
	ansiWriter  = "\x1b[1;36m"
	ansiReader  = "\x1b[1;35m"
	ansiLinkEnd = "\x1b]8;;\x1b\\"
	clearLine   = "\r\x1b[K"
	idleNotice  = 10 * time.Minute
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
	exec.Command("osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", title, text).Run()
}

// printer renders the transcript for people. Colors, links, the status
// line and notifications need a terminal, so redirected logs stay plain;
// NO_COLOR turns off the ANSI parts but keeps notifications.
type printer struct {
	out      io.Writer
	repo     string
	color    bool
	live     bool
	day      string
	counts   map[string]int
	lastAt   time.Time
	idleSent bool
	statusOn bool
	watched  bool // saw the session active, so its end is news
}

func newPrinter(out io.Writer, repo string, follow bool) *printer {
	f, ok := out.(*os.File)
	tty := ok && isTerminal(f.Fd())
	color := tty && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	return &printer{out: out, repo: repo, color: color, live: tty && follow, counts: map[string]int{}}
}

func (p *printer) paint(code, text string) string {
	if !p.color {
		return text
	}
	return code + text + ansiReset
}

func (p *printer) clearStatus() error {
	if !p.statusOn {
		return nil
	}
	p.statusOn = false
	_, err := io.WriteString(p.out, clearLine)
	return err
}

func (p *printer) message(v session, m message) error {
	at, err := time.Parse(time.RFC3339Nano, m.At)
	if err != nil {
		return err
	}
	if err := p.clearStatus(); err != nil {
		return err
	}
	p.counts[m.From]++
	p.lastAt, p.idleSent = at, false
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

// status redraws a bottom line inferred from wait polling: a participant
// whose cursor was touched in the last 2s is waiting; otherwise it is busy.
func (p *printer) status(v session, dir string) {
	if !p.live {
		return
	}
	p.watched = true
	if p.lastAt.IsZero() {
		p.lastAt, _ = time.Parse(time.RFC3339Nano, v.StartedAt)
	}
	if quiet := time.Since(p.lastAt); quiet >= idleNotice && !p.idleSent {
		p.idleSent = true
		notify("peer", "No messages for "+humanDuration(quiet))
	}
	if !p.color {
		return
	}
	var parts []string
	for _, name := range []string{v.Writer, v.Reader} {
		since, _ := time.Parse(time.RFC3339Nano, v.StartedAt)
		if st, err := os.Stat(filepath.Join(dir, "cursor-"+name)); err == nil {
			if time.Since(st.ModTime()) < 2*time.Second {
				parts = append(parts, p.author(v, name)+" waiting")
				continue
			}
			since = st.ModTime()
		}
		parts = append(parts, p.author(v, name)+" busy "+humanDuration(time.Since(since)))
	}
	p.statusOn = true
	fmt.Fprint(p.out, clearLine+p.paint(ansiDim, "… ")+strings.Join(parts, p.paint(ansiDim, " · ")))
}

func (p *printer) ended(v session) error {
	start, err := time.Parse(time.RFC3339Nano, v.StartedAt)
	if err != nil {
		return err
	}
	end, err := time.Parse(time.RFC3339Nano, v.EndedAt)
	if err != nil {
		return err
	}
	if err := p.clearStatus(); err != nil {
		return err
	}
	total := p.counts[v.Writer] + p.counts[v.Reader]
	noun := "messages"
	if total == 1 {
		noun = "message"
	}
	summary := fmt.Sprintf("%d %s (%s %d, %s %d) in %s", total, noun, v.Writer, p.counts[v.Writer], v.Reader, p.counts[v.Reader], humanDuration(end.Sub(start)))
	if p.watched {
		notify("peer", "Session ended: "+summary)
	}
	_, err = fmt.Fprintf(p.out, "%s\n%s\n", p.paint(ansiDim, "── session ended at "+end.Local().Format("15:04:05")+" ──"), summary)
	return err
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
		}
		if _, err := fmt.Fprintf(out, "%-17s  %s  %s\n", v.ID, s.summary(v), state); err != nil {
			return err
		}
	}
	return nil
}
