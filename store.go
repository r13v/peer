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
	// is main.
	Members   []member `json:"members"`
	StartedAt string   `json:"started_at"`
	EndedAt   string   `json:"ended_at,omitempty"`
	// EndedReason says why a session ended other than by its main's end.
	EndedReason string `json:"ended_reason,omitempty"`
}

// member is a participant, known in the room by its role. Agent names the
// app behind it, such as codex, for display and launching.
type member struct {
	Role  string `json:"role"`
	Agent string `json:"agent,omitempty"`
	// Exited is set once a launched member's process has stopped.
	Exited bool `json:"exited,omitempty"`
	// Worker is set for a launched member that edits files.
	Worker bool `json:"worker,omitempty"`
	// Model is the model a launched member's CLI was asked to use.
	Model string `json:"model,omitempty"`
	// Effort is the reasoning effort it was asked to use.
	Effort string `json:"effort,omitempty"`
	// Worktree, Branch and Base are set for a worker in its own linked
	// worktree: its path, its branch and the commit it started from.
	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Base     string `json:"base,omitempty"`
	// Kicked is set once main or the user has kicked the member,
	// by KickedBy. Its role stays taken for the rest of the room, so a
	// late exit status or the old member cannot act as a newcomer.
	Kicked   bool   `json:"kicked,omitempty"`
	KickedBy string `json:"kicked_by,omitempty"`
	// PID leads the process group of a launched member's wrapper.
	PID int `json:"pid,omitempty"`
	// Unread counts the messages that wait for the member; status sets
	// it, and the session file never holds it.
	Unread int `json:"unread,omitempty"`
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
	// mainRole is the role of the one participant that edits files.
	mainRole = "main"
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
	if role == mainRole || role == human || role == system {
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
		return "", errors.New("message is empty; pipe its text to stdin or pass --text")
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

// label names a participant for people, e.g. "reader · codex/gpt-5/high,
// readonly".
func (s session) label(role string) string {
	if m := s.member(role); m != nil && m.Agent != "" {
		return role + " · " + m.app()
	}
	return role
}

// app names m's agent, with the model and effort it was launched with when
// peer knows them, and, for a member other than main, whether it
// edits files, e.g. "codex/gpt-5/high, worker".
func (m member) app() string {
	parts := []string{m.Agent}
	for _, p := range []string{m.Model, m.Effort} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	out := strings.Join(parts, "/")
	switch {
	case m.Role == mainRole:
	case m.Worker:
		out += ", worker"
	default:
		out += ", readonly"
	}
	return out
}

func (s session) check(role string) error {
	m := s.member(role)
	if m == nil {
		return fmt.Errorf("%q is not a participant in session %s", role, s.ID)
	}
	if m.Kicked {
		return fmt.Errorf("%s was kicked from session %s", role, s.ID)
	}
	return nil
}

// openStore opens the store of cwd's checkout, or of PEER_REPO when it is
// set, so a worker in its own worktree reaches the room it was invited to.
func openStore(cwd string) (*store, error) {
	if r := os.Getenv("PEER_REPO"); r != "" {
		cwd = r
	}
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
	// roomName is the name main gives a room at start.
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
	if len(v.Members) == 0 || v.Members[0].Role != mainRole {
		return v, fmt.Errorf("session %s was made by an older peer", id)
	}
	return v, nil
}

// load reads session id under the store lock and marks members whose
// launched process has exited. Main hears of each, so it does not
// wait on a member that is gone and can invite the role again.
func (s *store) load(id string) (session, error) {
	v, err := s.session(id)
	if err != nil || v.EndedAt != "" {
		return v, err
	}
	for i := range v.Members {
		m := &v.Members[i]
		if m.Role == mainRole || m.Exited || m.Kicked {
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
		if _, err := s.appendMessage(v, system, mainRole, fmt.Sprintf("%s exited with status %d", m.Role, status)); err != nil {
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
		v = session{ID: id, Repo: s.repo, Members: []member{{Role: mainRole, Agent: agent}}, StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if err := writeJSON(filepath.Join(dir, "session.json"), v); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(v)
	})
	return v, err
}

// join adds m to active room id and tells main. A role that is
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
		if old.Kicked {
			return v, fmt.Errorf("%s was kicked from room %s, and its role is not reused; use another role, such as %s-2", m.Role, id, m.Role)
		}
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
	_, err = s.appendMessage(v, system, mainRole, v.label(m.Role)+" joined")
	return v, err
}

// status prints room id, or every active room in this checkout.
func (s *store) status(id string, out io.Writer) error {
	if id != "" {
		v, err := s.refresh(id)
		if err != nil {
			return err
		}
		if err := s.countUnread(&v); err != nil {
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
			if err := s.countUnread(&v); err != nil {
				return err
			}
			if err := json.NewEncoder(out).Encode(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// countUnread sets Unread for v's members that can still read, including
// one that exited, whose role can be invited again; it reads without the
// store lock, so a count can be a moment old.
func (s *store) countUnread(v *session) error {
	for i := range v.Members {
		m := &v.Members[i]
		if m.Kicked {
			continue
		}
		n, err := s.unread(v.ID, m.Role)
		if err != nil {
			return err
		}
		m.Unread = n
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
		// The sender has its text, so send prints only how to find the
		// message and how many messages wait for the sender.
		unread, err := s.unread(v.ID, from)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(struct {
			ID     string `json:"id"`
			At     string `json:"at"`
			Unread int    `json:"unread"`
		}{m.ID, m.At, unread})
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
		if to != everyone {
			if err := v.check(to); err != nil {
				return err
			}
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

// A batch holds at most batchCount messages and, unless its one message is
// larger, about batchBytes of their JSON; the rest waits for the next wait.
const (
	batchCount = 16
	batchBytes = 32 * 1024
)

// forever is the wait timeout that waits until a message comes, the room
// ends or the participant is kicked.
const forever time.Duration = -1

// batch is what wait prints. Statuses are messages, timeout, ended and
// kicked; ended comes only with the last messages of the room.
type batch struct {
	Status   string    `json:"status"`
	Messages []message `json:"messages"`
	// HasMore says that more messages wait for the next wait.
	HasMore bool `json:"has_more,omitempty"`
	// Oversized marks a batch of one message larger than batchBytes.
	Oversized bool `json:"oversized,omitempty"`
	// Reason says why an ended room ended other than by its main's end.
	Reason string `json:"reason,omitempty"`
}

func (b batch) MarshalJSON() ([]byte, error) {
	if b.Status == "kicked" {
		return []byte(`{"status":"kicked"}`), nil
	}
	type plain batch
	if b.Messages == nil {
		b.Messages = []message{}
	}
	return json.Marshal(plain(b))
}

// isFor reports whether m goes to role.
func (m message) isFor(role string) bool {
	return m.From != role && (m.To == role || m.To == everyone)
}

func (s *store) cursorPath(id, role string) string {
	return filepath.Join(s.dir, "sessions", id, "cursor-"+role)
}

// cursor is the transcript offset up to which role has read room id.
func (s *store) cursor(id, role string) (int64, error) {
	b, err := os.ReadFile(s.cursorPath(id, role))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

// scan calls fn with each transcript message of room id after offset and
// the offset just past it, until fn returns false.
func (s *store) scan(id string, offset int64, fn func(m message, next int64) bool) error {
	f, err := os.Open(filepath.Join(s.dir, "sessions", id, "messages.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
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
			return nil
		}
		if err != nil {
			return err
		}
		offset += int64(len(line))
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			return err
		}
		if !fn(m, offset) {
			return nil
		}
	}
}

// unread counts the messages for role in room id that it has not read.
func (s *store) unread(id, role string) (int, error) {
	offset, err := s.cursor(id, role)
	if err != nil {
		return 0, err
	}
	n := 0
	err = s.scan(id, offset, func(m message, _ int64) bool {
		if m.isFor(role) {
			n++
		}
		return true
	})
	return n, err
}

// deliverBatch passes as's next batch to write and only then moves as's
// cursor past it, so a wait killed while printing leaves the batch for the
// next wait; a repeat is better than a loss. It reports false, and writes
// nothing, while the room is active and nothing is unread. write runs
// under the checkout's lock, so a stalled stdout holds up every room here;
// give each role its own delivery lock if that ever matters.
func (s *store) deliverBatch(sid, as string, write func(batch) error) (bool, error) {
	delivered := false
	err := s.locked(func() error {
		v, err := s.load(sid)
		if err != nil {
			return err
		}
		// A kicked member gets no backlog, not even the room's end.
		if m := v.member(as); m != nil && m.Kicked {
			delivered = true
			return write(batch{Status: "kicked"})
		}
		if err := v.check(as); err != nil {
			return err
		}
		offset, err := s.cursor(v.ID, as)
		if err != nil {
			return err
		}
		var b batch
		size, full := 0, false
		err = s.scan(v.ID, offset, func(m message, next int64) bool {
			if !m.isFor(as) {
				offset = next
				return true
			}
			j, _ := json.Marshal(m)
			if full || len(b.Messages) > 0 && size+len(j) > batchBytes {
				b.HasMore = true
				return false
			}
			b.Messages, size, offset = append(b.Messages, m), size+len(j), next
			b.Oversized = len(j) > batchBytes
			full = b.Oversized || len(b.Messages) == batchCount
			return true
		})
		if err != nil {
			return err
		}
		switch {
		case !b.HasMore && v.EndedAt != "":
			b.Status, b.Reason = "ended", v.EndedReason
		case len(b.Messages) > 0:
			b.Status = "messages"
		}
		if b.Status != "" {
			if err := write(b); err != nil {
				return err
			}
			delivered = true
		}
		// An empty poll still writes the cursor, so log can show this
		// participant as waiting.
		return writeAtomic(s.cursorPath(v.ID, as), []byte(strconv.FormatInt(offset, 10)+"\n"))
	})
	return delivered, err
}

// wait prints as's unread messages as one batch once there are any, or a
// timeout once timeout passes: a zero timeout checks once, and forever
// waits until a message comes, the room ends or as is kicked.
func (s *store) wait(sid, as string, timeout time.Duration, out io.Writer) error {
	emit := func(b batch) error {
		data, err := json.Marshal(b)
		if err != nil {
			return err
		}
		return writeFull(out, append(data, '\n'))
	}
	deadline := time.Now().Add(timeout)
	for {
		delivered, err := s.deliverBatch(sid, as, emit)
		if err != nil || delivered {
			return err
		}
		if timeout != forever && !time.Now().Before(deadline) {
			return emit(batch{Status: "timeout"})
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// writeFull writes data in one Write and fails unless all of it went out.
func writeFull(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err == nil && n < len(data) {
		err = io.ErrShortWrite
	}
	return err
}

func (s *store) end(sid, as string, out io.Writer) error {
	return s.locked(func() error {
		v, err := s.load(sid)
		if err != nil {
			return err
		}
		if as != mainRole {
			return errors.New("only main can end this session")
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

// kick removes role from active room id for by, main or the user,
// and stops the member's process if peer launched it. Kicking a kicked
// member again only retries stopping it. It returns a note on the process.
func (s *store) kick(id, role, by string) (string, error) {
	var m member
	err := s.locked(func() error {
		v, err := s.load(id)
		if err != nil {
			return err
		}
		if v.EndedAt != "" {
			return endedError(v)
		}
		cur := v.member(role)
		switch {
		case role == mainRole:
			return errors.New("main cannot be kicked; end the room instead")
		case cur == nil:
			return fmt.Errorf("%q is not a participant in session %s", role, v.ID)
		case cur.Exited && !cur.Kicked:
			return fmt.Errorf("%s has already exited", role)
		}
		if !cur.Kicked {
			cur.Kicked, cur.KickedBy = true, by
			if err := writeJSON(s.sessionPath(id), v); err != nil {
				return err
			}
			if _, err := s.appendMessage(v, system, everyone, role+" was kicked by "+by); err != nil {
				return err
			}
		}
		m = *cur
		return nil
	})
	if err != nil {
		return "", err
	}
	note := role + " was kicked"
	if m.Worktree != "" {
		note += "; its worktree stays in " + m.Worktree + " on branch " + m.Branch
	}
	if m.PID == 0 {
		// A member launched right now is stopped by its invite.
		return note + "; peer did not launch it, so its process is left alone", nil
	}
	if err := stopMember(m.PID, s.exitPath(id, role)); err != nil {
		return "", fmt.Errorf("%s, but its process %d is not confirmed stopped: %w; run peer kick again to retry", note, m.PID, err)
	}
	return note + " and its process stopped", nil
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
// "Tue 29 Sep 19:59  main, reader  12 msgs  8m".
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
