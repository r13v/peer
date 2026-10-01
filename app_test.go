package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAppRooms(t *testing.T) {
	repoA := testRepo(t)
	repoB := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repoB).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	startRoom(t, repoA, "task")
	startRoom(t, repoB, "task")
	if _, err := invoke(repoA, "for a", "send", "task", "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	// The app runs outside any checkout.
	outside := t.TempDir()
	got, err := invoke(outside, "", "app", "rooms")
	var rooms []appRoom
	if err != nil || json.Unmarshal([]byte(got), &rooms) != nil || len(rooms) != 2 {
		t.Fatalf("rooms: %q, %v", got, err)
	}
	var store string
	for _, r := range rooms {
		if r.Session.ID != "task" {
			t.Fatalf("room %+v", r)
		}
		if filepath.Base(r.Session.Repo) == filepath.Base(repoA) {
			store = r.Store
		}
	}
	if m := rooms[0].Members; len(m) != 2 || m[0].Role != writer || m[0].State != "waiting" || m[1].State != "busy" {
		t.Fatalf("member states: %+v", m)
	}
	if store == "" || rooms[0].Store == rooms[1].Store {
		t.Fatalf("rooms with one ID share a store: %+v", rooms)
	}
	snap := func(args ...string) appSnapshot {
		t.Helper()
		got, err := invoke(outside, "", append([]string{"app", "room", "task", "--store", store}, args...)...)
		var s appSnapshot
		if err != nil || json.Unmarshal([]byte(got), &s) != nil {
			t.Fatalf("room: %q, %v", got, err)
		}
		return s
	}
	first := snap()
	if len(first.Messages) != 2 || first.Messages[1].Text != "for a" {
		t.Fatalf("snapshot messages: %+v", first.Messages)
	}
	if again := snap("--after", "0"); len(again.Messages) != 2 {
		t.Fatal("reading moved a cursor")
	}
	if got, err := invoke(repoA, "", "wait", "task", "--as", "reader"); err != nil || !strings.Contains(got, `"text":"for a"`) {
		t.Fatalf("the app's read took the reader's message: %s, %v", got, err)
	}
	if _, err := invoke(outside, "from the app", "app", "post", "task", "--store", store, "--to", "reader"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(outside, "security reviewer", "app", "add", "task", "--store", store, "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(outside, "x", "app", "add", "task", "--store", store, "--agent", "copilot"); err == nil {
		t.Fatal("add accepted an agent invite cannot launch")
	}
	later := snap("--after", jsonInt(first.Next))
	if len(later.Messages) != 2 || later.Messages[0].From != human || later.Messages[1].To != writer || !strings.Contains(later.Messages[1].Text, "peer invite task ROLE --as writer --agent codex") {
		t.Fatalf("new messages: %+v", later.Messages)
	}
	for _, bad := range []string{"", "..", ".", "../x", "a/b", "missing"} {
		if _, err := invoke(outside, "", "app", "room", "task", "--store", bad); err == nil {
			t.Fatalf("store %q accepted", bad)
		}
	}
	if _, err := invoke(outside, "", "app", "close", "task", "--store", store); err != nil {
		t.Fatal(err)
	}
	if s := snap(); s.Session.EndedReason != "closed in peer" {
		t.Fatalf("close: %+v", s.Session)
	}
	if _, err := invoke(repoB, "still open", "send", "task", "--as", "writer"); err != nil {
		t.Fatalf("closing one checkout's room closed the other: %v", err)
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestAppLogTail(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "task")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"item.completed","item":{"type":"agent_message","text":"hello"}}`
	log := strings.Repeat("2026-10-01T12:00:00Z\t"+line+"\n", logTail/len(line)+10)
	if err := os.WriteFile(s.logPath(v.ID, "reader"), []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	snap, err := s.snapshot(v.ID, 0, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if snap.LogRole != "reader" || snap.LogNext != int64(len(log)) || len(snap.Log) == 0 || len(snap.Log) >= strings.Count(log, "\n") {
		t.Fatalf("log tail: role %q next %d entries %d", snap.LogRole, snap.LogNext, len(snap.Log))
	}
	for _, e := range snap.Log {
		if e.Kind != "text" || e.Text != "hello" || e.At == "" {
			t.Fatalf("partial or unparsed line: %+v", e)
		}
	}
	if snap.LogStart != snap.Log[0].Offset || log[snap.LogStart-1] != '\n' {
		t.Fatalf("log start %d is not the first entry's line", snap.LogStart)
	}
	// Reading back from the start reaches every line once.
	total, start := len(snap.Log), snap.LogStart
	for start > 0 {
		part, err := s.logBefore(v.ID, "reader", start)
		if err != nil || len(part.Log) == 0 || part.LogStart >= start {
			t.Fatalf("log before %d: %+v, %v", start, part.LogStart, err)
		}
		last := part.Log[len(part.Log)-1]
		if last.Offset+int64(len(log)/strings.Count(log, "\n")) != start {
			t.Fatalf("earlier part ends at %d, not %d", last.Offset, start)
		}
		total, start = total+len(part.Log), part.LogStart
	}
	if total != strings.Count(log, "\n") {
		t.Fatalf("read %d of %d entries", total, strings.Count(log, "\n"))
	}
	// A line longer than the tail is read whole, not as an empty part.
	long := "2026-10-01T12:00:00Z\t" + `{"type":"item.completed","item":{"type":"agent_message","text":"` + strings.Repeat("x", logTail*2) + `"}}` + "\n"
	big := log + long + "2026-10-01T12:00:00Z\t" + line + "\n"
	if err := os.WriteFile(s.logPath(v.ID, "reader"), []byte(big), 0600); err != nil {
		t.Fatal(err)
	}
	end := int64(len(log + long))
	part, err := s.logBefore(v.ID, "reader", end)
	if err != nil || part.LogStart != int64(len(log)) || len(part.Log) != 1 || len(part.Log[0].Text) != logTail*2 {
		t.Fatalf("long line before %d: start %d, %d entries, %v", end, part.LogStart, len(part.Log), err)
	}
	if _, err := s.logBefore(v.ID, "../x", 10); err == nil {
		t.Fatal("log of a non-member read")
	}
}

func TestMCP(t *testing.T) {
	repo := testRepo(t)
	cin, sout := io.Pipe()
	sin, cout := io.Pipe()
	go func() { _ = serveMCP(repo, sin, sout) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	ctx := context.Background()
	cs, err := client.Connect(ctx, &mcp.IOTransport{Reader: cin, Writer: cout}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(name string, args map[string]any) (string, bool) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res.Content[0].(*mcp.TextContent).Text, res.IsError
	}
	if got, failed := call("peer_start", map[string]any{"name": "task", "agent": "codex"}); failed || !strings.Contains(got, `"id":"task"`) {
		t.Fatalf("start: %s", got)
	}
	if got, failed := call("peer_join", map[string]any{"room": "task", "role": "reader", "repo": filepath.Join(repo, ".")}); failed {
		t.Fatalf("join: %s", got)
	}
	if got, failed := call("peer_send", map[string]any{"room": "task", "as": "reader", "to": "*", "text": "to all"}); failed {
		t.Fatalf("send to *: %s", got)
	}
	if got, failed := call("peer_send", map[string]any{"room": "task", "as": "writer", "to": "reader", "text": "  hi \"there\" $(x)  "}); failed {
		t.Fatalf("send: %s", got)
	}
	if got, _ := call("peer_wait", map[string]any{"room": "task", "as": "reader", "timeout_seconds": 1}); !strings.Contains(got, `"text":"hi \"there\" $(x)"`) {
		t.Fatalf("wait: %s", got)
	}
	if got, _ := call("peer_wait", map[string]any{"room": "task", "as": "reader", "timeout_seconds": 1}); got != `{"status":"timeout"}` {
		t.Fatalf("wait timeout: %s", got)
	}
	for name, args := range map[string]map[string]any{
		"peer_start":  {"name": "../x"},
		"peer_join":   {"room": "task", "role": "writer"},
		"peer_send":   {"room": "task", "as": "nobody", "text": "x"},
		"peer_invite": {"room": "task", "as": "reader", "role": "helper", "agent": "codex"},
		"peer_end":    {"room": "task", "as": "reader"},
	} {
		if got, failed := call(name, args); !failed {
			t.Fatalf("%s accepted %v: %s", name, args, got)
		}
	}
	if got, _ := call("peer_skills", map[string]any{"name": "flow"}); got != string(flowInstructions) {
		t.Fatal("skills flow")
	}
	if got, failed := call("peer_end", map[string]any{"room": "task", "as": "writer"}); failed || !strings.Contains(got, "ended_at") {
		t.Fatalf("end: %s", got)
	}
}

func TestWaitContextCancelled(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "task")
	if _, err := invoke(repo, "kept", "send", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out strings.Builder
	if err := s.waitContext(ctx, v.ID, "reader", time.Second, &out); !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatalf("cancelled wait: %q, %v", out.String(), err)
	}
	if got, err := invoke(repo, "", "wait", v.ID, "--as", "reader"); err != nil || !strings.Contains(got, `"text":"kept"`) {
		t.Fatalf("a cancelled wait took the message: %s, %v", got, err)
	}
}
