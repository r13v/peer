package main

import (
	"bufio"
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

//go:embed roles/reader.md
var readerInstructions []byte

//go:embed roles/writer.md
var writerInstructions []byte

var errNoSession = errors.New("no session; the writer must run peer start")

// openURL is replaced in tests.
var openURL = func(link string) error { return exec.Command("open", link).Run() }

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
		return errors.New("usage: peer skills reader|writer, peer update, or peer start|status|send|wait|end|log|history")
	}
	if args[0] == "update" {
		if len(args) != 1 {
			return errors.New("usage: peer update")
		}
		return update(in, out)
	}
	if args[0] == "skills" {
		if len(args) != 2 {
			return errors.New("usage: peer skills reader|writer")
		}
		switch args[1] {
		case "reader":
			_, err := out.Write(readerInstructions)
			return err
		case "writer":
			_, err := out.Write(writerInstructions)
			return err
		default:
			return errors.New("usage: peer skills reader|writer")
		}
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
	openReader := fs.String("open-reader", "", "codex or claude: open a prefilled reader chat after start")
	timeout := fs.Duration("timeout", 90*time.Second, "wait timeout, at most 110s")
	id := fs.String("session", "", "session ID for log")
	follow := fs.Bool("follow", false, "follow log")
	if err := fs.Parse(args[1:]); err != nil {
		return err
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
		if *openReader != "" && *openReader != "codex" && *openReader != "claude" {
			return errors.New("--open-reader must be codex or claude")
		}
		if err := s.start(*writer, *reader, out); err != nil || *openReader == "" {
			return err
		}
		link := readerLink(*openReader, s.repo, *writer, *reader)
		if err := openURL(link); err != nil {
			return fmt.Errorf("session started, but opening %s failed: %v; open this link: %s", *openReader, err, link)
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
		if *timeout < 0 || *timeout > 110*time.Second {
			return errors.New("timeout must be between 0 and 110s")
		}
		return s.wait(*actor, *timeout, out)
	case "end":
		if err := checkName(*actor); err != nil {
			return err
		}
		return s.end(*actor, out)
	case "log":
		return s.log(*id, *follow, out)
	case "history":
		return s.history(out)
	default:
		return fmt.Errorf("unknown command %q", args[0])
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

// readerLink builds a desktop deep link that opens a new chat in repo with
// the reader prompt prefilled. Neither app submits the prompt by itself.
func readerLink(app, repo, writer, reader string) string {
	prompt := fmt.Sprintf("Use the peer skill. You are participant %s, the reader in %s's peer session in this checkout. Discuss the approach through peer, then review the diff and send concrete findings through peer. Do not edit files. Keep waiting for replies until the review is closed.", reader, writer)
	if app == "codex" {
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
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
	root, err := cmd.Output()
	if err != nil {
		return nil, errors.New("run this command inside the shared Git checkout")
	}
	repo, err := filepath.EvalSymlinks(strings.TrimSpace(string(root)))
	if err != nil {
		return nil, err
	}
	home := os.Getenv("PEER_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".peer")
	}
	key := sha256.Sum256([]byte(repo))
	dir := filepath.Join(home, "repos", hex.EncodeToString(key[:8]))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &store{dir: dir, repo: repo}, nil
}

func (s *store) locked(fn func() error) error {
	f, err := os.OpenFile(filepath.Join(s.dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
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

func validID(id string) bool {
	if len(id) != 16 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (s *store) session(id string) (session, error) {
	if !validID(id) {
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

func (s *store) current() (session, error) {
	id, err := s.activeID()
	if err != nil {
		return session{}, err
	}
	return s.session(id)
}

func (s *store) start(writer, reader string, out io.Writer) error {
	return s.locked(func() error {
		old, err := s.current()
		if err == nil && old.EndedAt == "" {
			return fmt.Errorf("session %s is active; end it before starting another", old.ID)
		}
		if err != nil && !errors.Is(err, errNoSession) {
			return err
		}
		id, err := newID()
		if err != nil {
			return err
		}
		v := session{ID: id, Repo: s.repo, Writer: writer, Reader: reader, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		dir := filepath.Join(s.dir, "sessions", id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
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
			return nil
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

func (s *store) log(id string, follow bool, out io.Writer) error {
	if id == "" {
		var err error
		id, err = s.activeID()
		if err != nil {
			return err
		}
	}
	if _, err := s.session(id); err != nil {
		return err
	}
	path := filepath.Join(s.dir, "sessions", id, "messages.jsonl")
	var offset int64
	for {
		f, err := os.Open(path)
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
				if _, err := fmt.Fprintf(out, "[%s] %s -> %s\n%s\n\n", m.At, m.From, m.To, m.Text); err != nil {
					f.Close()
					return err
				}
			}
			f.Close()
		}
		if !follow {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *store) history(out io.Writer) error {
	dirs, err := os.ReadDir(filepath.Join(s.dir, "sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var sessions []session
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		v, err := s.session(d.Name())
		if err != nil {
			return err
		}
		sessions = append(sessions, v)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].StartedAt > sessions[j].StartedAt })
	return json.NewEncoder(out).Encode(sessions)
}
