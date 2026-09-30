package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type session struct {
	ID   string `json:"id"`
	Repo string `json:"repo"`
	// Members lists the room's participants in joining order; the first
	// is the writer.
	Members   []member `json:"members"`
	StartedAt string   `json:"started_at"`
	EndedAt   string   `json:"ended_at,omitempty"`
	// EndedReason says why a session ended other than by its writer's end.
	EndedReason string `json:"ended_reason,omitempty"`
}

// member is a participant, known in the room by its role. Agent names the
// app behind it, such as codex, for display and launching.
type member struct {
	Role  string `json:"role"`
	Agent string `json:"agent,omitempty"`
	// Exited is set once a launched member's process has stopped.
	Exited bool `json:"exited,omitempty"`
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

const (
	// writer is the role of the one participant that edits files.
	writer = "writer"
	// human is the author of messages sent from the peer TUI; rooms
	// refuse it as a role.
	human = "user"
	// system is the author of peer's own notices, such as a member joining.
	system = "peer"
	// everyone addresses a message to every participant of a room.
	everyone = "*"
)

// checkRole checks a role that join or invite adds to a room.
func checkRole(role string) error {
	if err := checkName(role); err != nil {
		return err
	}
	if role == writer || role == human || role == system {
		return fmt.Errorf("role %q is reserved", role)
	}
	return nil
}

// checkAgent checks an optional app name.
func checkAgent(agent string) error {
	if agent == "" {
		return nil
	}
	return checkName(agent)
}

func checkName(name string) error {
	if len(name) == 0 || len(name) > 64 {
		return errors.New("role must be 1-64 characters: a-z, 0-9, - or _, starting with a letter")
	}
	for i, c := range name {
		if c >= 'a' && c <= 'z' || i > 0 && (c >= '0' && c <= '9' || c == '-' || c == '_') {
			continue
		}
		return errors.New("role must be 1-64 characters: a-z, 0-9, - or _, starting with a letter")
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

// members lists the roles of the room's participants in joining order.
func (s session) members() []string {
	roles := make([]string, len(s.Members))
	for i, m := range s.Members {
		roles[i] = m.Role
	}
	return roles
}

// member returns the participant with role, or nil.
func (s *session) member(role string) *member {
	for i := range s.Members {
		if s.Members[i].Role == role {
			return &s.Members[i]
		}
	}
	return nil
}

// label names a participant for people, e.g. "reader · codex".
func (s session) label(role string) string {
	if m := s.member(role); m != nil && m.Agent != "" {
		return role + " · " + m.Agent
	}
	return role
}

func (s session) check(role string) error {
	if s.member(role) == nil {
		return fmt.Errorf("%q is not a participant in session %s", role, s.ID)
	}
	return nil
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
	if err := json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	if len(v.Members) == 0 || v.Members[0].Role != writer {
		return v, fmt.Errorf("session %s was made by an older peer", id)
	}
	return v, nil
}

// load reads session id under the store lock and marks members whose
// launched process has exited. The writer hears of each, so it does not
// wait on a member that is gone and can invite the role again.
func (s *store) load(id string) (session, error) {
	v, err := s.session(id)
	if err != nil || v.EndedAt != "" {
		return v, err
	}
	for i := range v.Members {
		m := &v.Members[i]
		if m.Role == writer || m.Exited {
			continue
		}
		b, err := os.ReadFile(s.exitPath(id, m.Role))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return v, err
		}
		status, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			continue // the wrapper is still writing it
		}
		m.Exited = true
		if err := writeJSON(s.sessionPath(id), v); err != nil {
			return v, err
		}
		if _, err := s.appendMessage(v, system, writer, fmt.Sprintf("%s exited with status %d", m.Role, status)); err != nil {
			return v, err
		}
	}
	return v, nil
}

func (s *store) sessionPath(id string) string {
	return filepath.Join(s.dir, "sessions", id, "session.json")
}

// logPath and exitPath hold a launched member's output and exit status.
func (s *store) logPath(id, role string) string {
	return filepath.Join(s.dir, "sessions", id, role+".log")
}

func (s *store) exitPath(id, role string) string {
	return filepath.Join(s.dir, "sessions", id, role+".exit")
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
	return writeJSON(s.sessionPath(v.ID), v)
}

func (s *store) start(name, agent string, out io.Writer) (session, error) {
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
		v = session{ID: id, Repo: s.repo, Members: []member{{Role: writer, Agent: agent}}, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if err := writeJSON(filepath.Join(dir, "session.json"), v); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(v)
	})
	return v, err
}

// join adds m to active room id and tells the writer. A role that is
// taken stays taken, except that a member who has exited can be replaced.
func (s *store) join(id string, m member, out io.Writer) error {
	return s.locked(func() error {
		v, err := s.add(id, m)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(v)
	})
}

// add is join for callers that hold the store lock.
func (s *store) add(id string, m member) (session, error) {
	v, err := s.load(id)
	if err != nil {
		return v, err
	}
	if v.EndedAt != "" {
		return v, endedError(v)
	}
	if old := v.member(m.Role); old != nil {
		if !old.Exited {
			return v, fmt.Errorf("role %s is already in room %s; if that is you, continue as %s, otherwise use another role, such as %s-2", m.Role, id, m.Role, m.Role)
		}
		// The newcomer replays the room from the start, and the old
		// status must not mark it as exited.
		for _, f := range []string{s.exitPath(id, m.Role), filepath.Join(s.dir, "sessions", id, "cursor-"+m.Role)} {
			if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				return v, err
			}
		}
		*old = m
	} else {
		v.Members = append(v.Members, m)
	}
	if err := writeJSON(s.sessionPath(id), v); err != nil {
		return v, err
	}
	_, err = s.appendMessage(v, system, writer, v.label(m.Role)+" joined")
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

func (s *store) send(sid, from, to, text string, out io.Writer) error {
	return s.locked(func() error {
		v, err := s.load(sid)
		if err != nil {
			return err
		}
		if v.EndedAt != "" {
			return endedError(v)
		}
		if err := v.check(from); err != nil {
			return err
		}
		if to == from {
			return errors.New("cannot send a message to yourself")
		}
		if to != everyone {
			if err := v.check(to); err != nil {
				return err
			}
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
		if err := v.check(as); err != nil {
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
			if m.From != as && (m.To == as || m.To == everyone) {
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
		if as != writer {
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
// "Tue 29 Sep 19:59  writer, reader  12 msgs  8m".
func (s *store) summary(v session) string {
	start, _ := time.Parse(time.RFC3339Nano, v.StartedAt)
	end := time.Now()
	if v.EndedAt != "" {
		end, _ = time.Parse(time.RFC3339Nano, v.EndedAt)
	}
	return fmt.Sprintf("%s  %s  %3d msgs  %s", start.Local().Format("Mon _2 Jan 15:04"), strings.Join(v.members(), ", "), s.count(v.ID), humanDuration(end.Sub(start)))
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
