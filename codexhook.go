package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// codexEvent is the part of a Codex hook's input that codexHook reads.
type codexEvent struct {
	Event        string          `json:"hook_event_name"`
	SessionID    string          `json:"session_id"`
	Cwd          string          `json:"cwd"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
}

// codexBinding ties a Codex session to a room it leads, so a resumed
// session finds the room and its forward.
type codexBinding struct {
	Repo string `json:"repo"`
	Room string `json:"room"`
}

// forwardReady bounds how long a hook waits for the forward it started.
var forwardReady = 3 * time.Second

// codexHook handles one hook event of the Codex plugin, read from in. After
// a peer start it starts peer forward for the new room, and on SessionStart
// it starts it again for the session's rooms. Nothing stops forward but the
// room's end: Codex fires SessionEnd when a session is resumed, and a
// closed Codex session goes on running queued turns.
// It tells the model to skip peer wait only for a room whose forward is
// ready; without that, main follows the usual instructions. The bindings
// live in the plugin's data directory, PLUGIN_DATA.
func codexHook(in io.Reader, out io.Writer) error {
	var e codexEvent
	if err := json.NewDecoder(in).Decode(&e); err != nil {
		return err
	}
	data := os.Getenv("PLUGIN_DATA")
	if data == "" || !codexThread.MatchString(e.SessionID) {
		return nil // forward needs both; without them main uses wait
	}
	// One file per room, so hooks that run at once never overwrite each
	// other's bindings.
	dir := filepath.Join(data, "sessions", e.SessionID)
	var ready []string
	switch e.Event {
	case "PostToolUse":
		v, ok := startedRoom(e)
		if !ok {
			return nil
		}
		b := codexBinding{Repo: v.Repo, Room: v.ID}
		// Bind before starting forward, so a resume finds every forward.
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		key := sha256.Sum256([]byte(b.Repo))
		if err := writeJSON(filepath.Join(dir, hex.EncodeToString(key[:8])+"-"+b.Room+".json"), b); err != nil {
			return err
		}
		isReady, err := startForward(b, e.SessionID)
		if err != nil {
			return err
		}
		if isReady {
			ready = append(ready, b.Room)
		}
	case "SessionStart":
		bindings, err := readBindings(dir)
		if err != nil {
			return err
		}
		for _, b := range bindings {
			isReady, err := startForward(b, e.SessionID)
			if err != nil {
				return err
			}
			if isReady {
				ready = append(ready, b.Room)
			}
		}
	default:
		return nil
	}
	if len(ready) == 0 {
		return nil
	}
	return emitJSON(out, map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":     e.Event,
		"additionalContext": forwardNote(ready),
	}})
}

func forwardNote(rooms []string) string {
	return "peer forward delivers the messages for main of peer room " + strings.Join(rooms, ", ") +
		" into this Codex session through codex queue: each batch comes as a turn of its own. Do not run peer wait for these rooms; peer refuses it while forward runs."
}

// startedRoom returns the room that a peer start printed in a Bash tool
// call, if this call was one: the whole command is a plain peer start NAME,
// and the room it printed exists in its checkout's store, active, with the
// same start time and main alone in it. A peer start inside a longer shell
// command is not recognized; main then reads the room with peer wait.
func startedRoom(e codexEvent) (session, bool) {
	var input struct {
		Command json.RawMessage `json:"command"`
	}
	if json.Unmarshal(e.ToolInput, &input) != nil {
		return session{}, false
	}
	// command is a string, or an argv such as ["bash", "-lc", "peer start x"].
	var names []string
	for _, cmd := range jsonStrings(input.Command) {
		if strings.ContainsAny(cmd, ";&|<>`$()\"'#\\\n") {
			continue // more than one plain command
		}
		words := strings.Fields(cmd)
		if len(words) >= 3 && filepath.Base(words[0]) == "peer" && words[1] == "start" && roomName.MatchString(words[2]) {
			names = append(names, words[2])
		}
	}
	if len(names) == 0 {
		return session{}, false
	}
	for _, text := range jsonStrings(e.ToolResponse) {
		for _, line := range strings.Split(text, "\n") {
			var v session
			if json.Unmarshal([]byte(strings.TrimSpace(line)), &v) != nil || v.Repo == "" || len(v.Members) != 1 || v.Members[0].Role != mainRole {
				continue
			}
			if !slices.ContainsFunc(names, func(name string) bool {
				return v.ID == name || sessionID.MatchString(v.ID) && strings.HasPrefix(v.ID, name+"-")
			}) {
				continue
			}
			s, err := openStore(v.Repo)
			if err != nil {
				continue
			}
			if got, err := s.session(v.ID); err == nil && got.EndedAt == "" && got.StartedAt == v.StartedAt {
				return v, true
			}
		}
	}
	return session{}, false
}

// jsonStrings collects every string in a JSON value, since Codex does not
// fix the shape of a tool's input and output.
func jsonStrings(raw json.RawMessage) []string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, x := range v {
				walk(x)
			}
		case map[string]any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(v)
	return out
}

func readBindings(dir string) ([]codexBinding, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var bindings []codexBinding
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var b codexBinding
		if err := json.Unmarshal(data, &b); err != nil {
			return nil, err
		}
		bindings = append(bindings, b)
	}
	return bindings, nil
}

// startForward starts peer forward for b's room in a session of its own,
// so it outlives the hook, and reports whether a forward for thread holds
// main's delivery: false when the room needs no forward any more or
// another session's forward holds it. A running forward counts as ready.
func startForward(b codexBinding, thread string) (bool, error) {
	s, err := openStore(b.Repo)
	if err != nil {
		return false, err
	}
	if _, err := s.session(b.Room); err != nil {
		return false, nil // gone: nothing to deliver
	}
	if _, err := os.Stat(s.forwardDonePath(b.Room, mainRole)); err == nil {
		return false, nil
	}
	// A forward that runs for this session already counts as ready; one
	// for another session delivers there, so this session gets no note.
	if h, isHeld, err := s.forwardHolder(b.Room, mainRole); err != nil || isHeld {
		return err == nil && h.Thread == thread, err
	}
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	log, err := os.OpenFile(filepath.Join(s.dir, "sessions", b.Room, "forward-main.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return false, err
	}
	defer log.Close()
	cmd := exec.Command(exe, "forward", b.Room, "--as", mainRole, "--codex-thread", thread)
	cmd.Dir, cmd.Stdout, cmd.Stderr = s.repo, log, log
	cmd.Env = append(os.Environ(), "PEER_REPO="+s.repo)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	for deadline := time.Now().Add(forwardReady); time.Now().Before(deadline); {
		// Ready only when this child holds the room, not a rival forward.
		if h, isHeld, err := s.forwardHolder(b.Room, mainRole); err != nil || isHeld && h.PID == cmd.Process.Pid {
			return err == nil, err
		}
		select {
		case <-exited:
			return false, nil // done already, or failed: see forward-main.log
		case <-time.After(20 * time.Millisecond):
		}
	}
	return false, nil
}
