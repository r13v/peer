package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const appUsage = "usage: peer app rooms, peer app room ID --store S [--after N] [--log ROLE] [--log-after N], peer app log ID --store S --log ROLE --before N, peer app post|add ID --store S (--to ROLE | --agent claude|codex), or peer app close ID --store S"

// logTail bounds how much of a member log the app reads on first view.
const logTail = 256 * 1024

// appRoom is a room in the app's list. Store is the base name of the
// room's store directory: with the ID it names the room across checkouts.
type appRoom struct {
	Store    string        `json:"store"`
	Session  session       `json:"session"`
	Messages int           `json:"messages"`
	Status   string        `json:"status"`
	Members  []memberState `json:"members"`
}

// appLogEntry is a logEntry for the app, without terminal links.
type appLogEntry struct {
	// Offset is where the entry's line starts in the log: with Kind's
	// position in the line it identifies the entry.
	Offset int64  `json:"offset"`
	At     string `json:"at,omitempty"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Failed bool   `json:"failed,omitempty"`
}

// appSnapshot is what a room gained since the app last read it. Next and
// LogNext are the offsets to pass back as --after and --log-after;
// LogStart is where the returned log begins, so a log read from the tail
// can be read further back with peer app log --before.
type appSnapshot struct {
	Session  session       `json:"session"`
	Status   string        `json:"status"`
	Members  []memberState `json:"members"`
	Messages []message     `json:"messages"`
	Next     int64         `json:"next"`
	LogRoles []string      `json:"log_roles"`
	LogRole  string        `json:"log_role,omitempty"`
	Log      []appLogEntry `json:"log"`
	LogNext  int64         `json:"log_next"`
	LogStart int64         `json:"log_start"`
}

// appLog is a part of a member log read before an offset.
type appLog struct {
	Log      []appLogEntry `json:"log"`
	LogStart int64         `json:"log_start"`
}

var logKinds = map[logKind]string{logRaw: "raw", logText: "text", logTool: "tool", logOutput: "output"}

// appCommand serves the macOS app. It runs outside any checkout, since
// Finder starts the app in /, so rooms are addressed by store and ID.
// Reading never touches a participant's cursor.
func appCommand(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(appUsage)
	}
	cmd, rest, id := args[0], args[1:], ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		id, rest = rest[0], rest[1:]
	}
	fs := flag.NewFlagSet("app "+cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	storeName := fs.String("store", "", "store directory name")
	after := fs.Int64("after", 0, "transcript offset")
	logRole := fs.String("log", "", "member whose log to read")
	logAfter := fs.Int64("log-after", 0, "log offset")
	before := fs.Int64("before", 0, "log offset to read up to")
	to := fs.String("to", everyone, "recipient role")
	agent := fs.String("agent", "", "agent to add")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *after < 0 || *logAfter < 0 || *before < 0 {
		return errors.New("offsets are not negative")
	}
	enc := json.NewEncoder(out)
	if cmd == "rooms" {
		if id != "" {
			return errors.New(appUsage)
		}
		entries, err := listEntries(map[string]int{})
		if err != nil {
			return err
		}
		rooms := make([]appRoom, len(entries))
		for i, e := range entries {
			dir := filepath.Join(e.s.dir, "sessions", e.v.ID)
			rooms[i] = appRoom{Store: filepath.Base(e.s.dir), Session: e.v, Messages: e.count, Status: roomStatus(e.v, dir), Members: memberStates(e.v, dir)}
		}
		return enc.Encode(rooms)
	}
	if id == "" {
		return errors.New(appUsage)
	}
	s, err := appStore(*storeName)
	if err != nil {
		return err
	}
	switch cmd {
	case "room":
		snap, err := s.snapshot(id, *after, *logRole, *logAfter)
		if err != nil {
			return err
		}
		return enc.Encode(snap)
	case "log":
		part, err := s.logBefore(id, *logRole, *before)
		if err != nil {
			return err
		}
		return enc.Encode(part)
	case "post", "add":
		body, err := io.ReadAll(io.LimitReader(in, 64*1024+1))
		if err != nil {
			return err
		}
		if cmd == "add" {
			if appBundles[*agent] == "" {
				return errors.New("add asks the writer to invite codex or claude")
			}
			desc, err := messageText(string(body))
			if err != nil {
				return err
			}
			return s.post(id, writer, addRequest(id, *agent, desc))
		}
		return s.post(id, *to, string(body))
	case "close":
		return s.close(id)
	}
	return errors.New(appUsage)
}

// appStore opens the store named by the base name of its directory.
func appStore(name string) (*store, error) {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return nil, errors.New("invalid store; use the store field of peer app rooms")
	}
	repos, err := reposDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(repos, name)
	// Lstat, so a link cannot point the app outside reposDir.
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("no store %s", name)
	}
	return &store{dir: dir}, nil
}

// snapshot returns room id's messages from offset after on, and the log
// of logRole, or of the first launched member, from logAfter on.
func (s *store) snapshot(id string, after int64, logRole string, logAfter int64) (appSnapshot, error) {
	v, err := s.refresh(id)
	if err != nil {
		return appSnapshot{}, err
	}
	s.repo = v.Repo
	dir := filepath.Join(s.dir, "sessions", v.ID)
	// As in readRoom, the session is read before the transcript.
	lines, next, _, err := readLines(filepath.Join(dir, "messages.jsonl"), after)
	if err != nil {
		return appSnapshot{}, err
	}
	snap := appSnapshot{Session: v, Status: roomStatus(v, dir), Members: memberStates(v, dir), Messages: []message{}, Next: next, LogRoles: []string{}, Log: []appLogEntry{}}
	for _, line := range lines {
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			return appSnapshot{}, err
		}
		snap.Messages = append(snap.Messages, m)
	}
	for _, role := range v.members()[1:] {
		if _, err := os.Stat(s.logPath(v.ID, role)); err == nil {
			snap.LogRoles = append(snap.LogRoles, role)
		}
	}
	if !slices.Contains(snap.LogRoles, logRole) {
		logRole, logAfter = "", 0
		if len(snap.LogRoles) > 0 {
			logRole = snap.LogRoles[0]
		}
	}
	if logRole == "" {
		return snap, nil
	}
	path := s.logPath(v.ID, logRole)
	if logAfter == 0 {
		if logAfter, err = tailOffset(path, logTail); err != nil {
			return appSnapshot{}, err
		}
	}
	lines, snap.LogNext, _, err = readLines(path, logAfter)
	if err != nil {
		return appSnapshot{}, err
	}
	snap.LogRole, snap.LogStart, snap.Log = logRole, logAfter, logEntries(lines, logAfter)
	return snap, nil
}

// logBefore reads up to logTail bytes of role's log that end at before,
// from the start of a line.
func (s *store) logBefore(id, role string, before int64) (appLog, error) {
	v, err := s.session(id)
	if err != nil {
		return appLog{}, err
	}
	if role == "" || v.member(role) == nil {
		return appLog{}, fmt.Errorf("%q is not a participant in session %s", role, id)
	}
	path := s.logPath(v.ID, role)
	start, err := lineBefore(path, before, logTail)
	if err != nil {
		return appLog{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return appLog{}, err
	}
	defer f.Close()
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return appLog{}, err
	}
	var lines [][]byte
	r := bufio.NewReader(io.LimitReader(f, before-start))
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return appLog{}, err
		}
		lines = append(lines, line)
	}
	return appLog{Log: logEntries(lines, start), LogStart: start}, nil
}

// logEntries turns log lines read from offset into entries for the app,
// without terminal links.
func logEntries(lines [][]byte, offset int64) []appLogEntry {
	entries := []appLogEntry{}
	for _, line := range lines {
		at, text := stamped(line)
		for _, e := range memberLogLine(text) {
			le := appLogEntry{Offset: offset, Kind: logKinds[e.Kind], Text: e.Text, Failed: e.Failed}
			if !at.IsZero() {
				le.At = at.Format("2006-01-02T15:04:05.000Z07:00")
			}
			entries = append(entries, le)
		}
		offset += int64(len(line))
	}
	return entries
}

// lineBefore returns the start of the first line that begins within the
// limit bytes before offset end of path, which starts a line. A line
// longer than limit is returned whole, so a part read back is never empty.
func lineBefore(path string, end, limit int64) (int64, error) {
	if end <= limit {
		return 0, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	// The newline at end-1 ends the line before end; the line wanted
	// starts after the first newline in the window before it.
	buf := make([]byte, limit)
	if _, err := f.ReadAt(buf, end-1-limit); err != nil {
		return 0, err
	}
	if i := bytes.IndexByte(buf, '\n'); i >= 0 {
		return end - limit + int64(i), nil
	}
	// No line starts in the window: go back to the start of the line.
	for at := end - 1 - limit; at > 0; {
		n := min(at, limit)
		at -= n
		if _, err := f.ReadAt(buf[:n], at); err != nil {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			return at + int64(i) + 1, nil
		}
	}
	return 0, nil
}

// tailOffset returns the offset of the first whole line within the last
// limit bytes of path, or 0 when path is shorter.
func tailOffset(path string, limit int64) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return lineBefore(path, st.Size(), limit)
}
