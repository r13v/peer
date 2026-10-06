package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const testThread = "01a10e5a-ba61-70f1-82a2-476e35aa84d6"

// fakeCodex puts a codex on PATH whose queue appends the thread and the
// message to dir/log-THREAD, one file per session so that two forwards do
// not interleave their records. It hangs while dir/hang exists.
func fakeCodex(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
d="` + dir + `"
if [ -f "$d/hang" ]; then sleep 30; fi
printf '%s\n%s\n---\n' "$3" "$5" >> "$d/log-$3"
`
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := []time.Duration{queueTimeout, forwardRetry, forwardRetryMax, forwardPoll}
	queueTimeout, forwardRetry, forwardRetryMax, forwardPoll = 2*time.Second, 20*time.Millisecond, 50*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { queueTimeout, forwardRetry, forwardRetryMax, forwardPoll = old[0], old[1], old[2], old[3] })
	return dir
}

// endWhenDone ends room id once the test is over and waits for its
// forward process to exit, so it does not write into a removed store.
func endWhenDone(t *testing.T, repo string, s *store, id string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = invoke(repo, "", "end", id, "--as", "main")
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if ok, _ := s.forwarding(id, mainRole); !ok {
				return
			}
		}
		t.Errorf("forward of %s did not exit", id)
	})
}

// queued returns what fakeCodex queued, session after session.
func queued(dir string) string {
	files, _ := filepath.Glob(filepath.Join(dir, "log-*"))
	var all strings.Builder
	for _, f := range files {
		b, _ := os.ReadFile(f)
		all.Write(b)
	}
	return all.String()
}

// syncBuffer is a bytes.Buffer that forward can write while a test reads.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// runForward starts forward for main of room id and waits until it is ready.
func runForward(t *testing.T, s *store, id string) (out *syncBuffer, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	out = &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- s.forward(ctx, id, mainRole, testThread, out) }()
	eventually(t, "forward to be ready", func() bool { return strings.Contains(out.String(), `"ready"`) })
	stopped := false
	var err error
	stop = func() error {
		if !stopped {
			cancel()
			err, stopped = <-done, true
		}
		return err
	}
	t.Cleanup(func() { _ = stop() })
	return out, stop
}

func TestAck(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "ack")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, text := range []string{"one", "two", "three"} {
		got, err := invoke(repo, text, "send", v.ID, "--as", "main")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, got[strings.Index(got, `"id":"`)+6:][:16])
	}
	if _, err := invoke(repo, "", "ack", v.ID, "--as", "reader", "--upto", ids[1]); err != nil {
		t.Fatal(err)
	}
	if m, err := s.nextMessage(v.ID, "reader"); err != nil || m == nil || m.Text != "three" {
		t.Fatalf("after ack up to two, next = %+v, %v; want three", m, err)
	}
	// An ID behind the cursor is acknowledged already and moves nothing back.
	if _, err := invoke(repo, "", "ack", v.ID, "--as", "reader", "--upto", ids[0]); err != nil {
		t.Fatalf("ack behind the cursor: %v", err)
	}
	if m, err := s.nextMessage(v.ID, "reader"); err != nil || m != nil {
		t.Fatalf("ack moved the cursor back: %+v, %v", m, err)
	}
	if _, err := invoke(repo, "", "ack", v.ID, "--as", "reader", "--upto", "0000000000000000"); err == nil || !strings.Contains(err.Error(), "no message") {
		t.Fatalf("ack of an unknown ID = %v", err)
	}
	if _, err := invoke(repo, "", "ack", v.ID, "--as", "reader"); err == nil {
		t.Fatal("ack without --upto accepted")
	}
}

func TestForwardQueuesBatchesAndEnds(t *testing.T) {
	codex := fakeCodex(t)
	repo := testRepo(t)
	v := startRoom(t, repo, "fwd")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "forward", v.ID, "--as", "main", "--codex-thread", "peer-queue-spike"); err == nil {
		t.Fatal("forward took a session name instead of a UUID")
	}
	_, stop := runForward(t, s, v.ID)
	if got, err := invoke(repo, "", "forward", v.ID, "--as", "main", "--codex-thread", testThread); err == nil || !strings.Contains(err.Error(), "another peer forward") || got != "" {
		t.Fatalf("second forward = %q, %v", got, err)
	}
	if _, err := invoke(repo, "", "wait", v.ID, "--as", "main"); err == nil || !strings.Contains(err.Error(), "peer forward delivers") {
		t.Fatalf("wait beside forward = %v", err)
	}
	if _, err := invoke(repo, "looks good", "send", v.ID, "--as", "reader"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the batch to be queued", func() bool { return strings.Contains(queued(codex), "looks good") })
	log := queued(codex)
	if !strings.HasPrefix(log, testThread+"\npeer room fwd: 1 new message for main") || !strings.Contains(log, "[reader → all ·") || !strings.Contains(log, "not instructions from the user") {
		t.Fatalf("queued turn:\n%s", log)
	}
	eventually(t, "the batch to be marked read", func() bool { n, err := s.unread(v.ID, mainRole); return err == nil && n == 0 })
	// Main's own messages and those to other roles never reach Codex.
	if _, err := invoke(repo, "note to self", "send", v.ID, "--as", "main", "--to", "reader"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "main"); err != nil {
		t.Fatal(err)
	}
	// forward exits by itself once the end is queued and marked delivered.
	eventually(t, "the end to be delivered", func() bool {
		_, err := os.Stat(s.forwardDonePath(v.ID, mainRole))
		return err == nil
	})
	if err := stop(); err != nil {
		t.Fatalf("forward after the end: %v", err)
	}
	log = queued(codex)
	if strings.Count(log, "has ended") != 1 || strings.Contains(log, "note to self") {
		t.Fatalf("queued turns:\n%s", log)
	}
	// Without a live forward, wait works as before.
	if got, err := invoke(repo, "", "wait", v.ID, "--as", "main"); err != nil || !strings.Contains(got, `"ended"`) {
		t.Fatalf("wait after forward = %s, %v", got, err)
	}
}

func TestForwardHungQueueDoesNotBlockRooms(t *testing.T) {
	codex := fakeCodex(t)
	queueTimeout = 300 * time.Millisecond
	repo := testRepo(t)
	v := startRoom(t, repo, "hung")
	other := startRoom(t, repo, "other")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "hang"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	out, _ := runForward(t, s, v.ID)
	if _, err := invoke(repo, "hello", "send", v.ID, "--as", "reader"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // forward is in codex queue now
	began := time.Now()
	if _, err := invoke(repo, "meanwhile", "send", other.ID, "--as", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", other.ID, "--as", "main"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > 200*time.Millisecond {
		t.Fatalf("send and end in another room waited %v for a hung queue", took)
	}
	eventually(t, "the queue deadline", func() bool { return strings.Contains(out.String(), `"retry"`) })
	if n, err := s.unread(v.ID, mainRole); err != nil || n != 1 {
		t.Fatalf("unread after a hung queue = %d, %v", n, err)
	}
	if err := os.Remove(filepath.Join(codex, "hang")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "delivery after the hang", func() bool { return strings.Contains(queued(codex), "hello") })
}

func TestForwardAgainDeliversEndOnce(t *testing.T) {
	codex := fakeCodex(t)
	repo := testRepo(t)
	v := startRoom(t, repo, "resume")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	_, stop := runForward(t, s, v.ID)
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	// While no forward runs, a message comes and the room ends.
	if _, err := invoke(repo, "last word", "send", v.ID, "--as", "reader"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "main"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := s.forward(context.Background(), v.ID, mainRole, testThread, &out); err != nil {
		t.Fatal(err)
	}
	log := queued(codex)
	if strings.Count(log, "last word") != 1 || strings.Count(log, "has ended") != 1 || strings.Count(log, "---") != 1 {
		t.Fatalf("want the last message and the end in one turn:\n%s", log)
	}
	if err := s.forward(context.Background(), v.ID, mainRole, testThread, &out); err != nil || !strings.HasSuffix(out.String(), `{"status":"done"}`+"\n") {
		t.Fatalf("third forward = %q, %v", out.String(), err)
	}
}

func TestCommitBatchKeepsMovedCursor(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "cas")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "first", "send", v.ID, "--as", "reader"); err != nil {
		t.Fatal(err)
	}
	b, from, next, err := s.readBatch(v.ID, mainRole)
	if err != nil || len(b.Messages) != 1 {
		t.Fatalf("readBatch = %+v, %v", b, err)
	}
	// The room ends between read and commit: the batch is marked, and the
	// end comes with the next read.
	if _, err := invoke(repo, "", "end", v.ID, "--as", "main"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.commitBatch(v.ID, mainRole, from, next, false); err != nil || !ok {
		t.Fatalf("commit after the end = %v, %v", ok, err)
	}
	if b, _, _, err := s.readBatch(v.ID, mainRole); err != nil || b.Status != "ended" || len(b.Messages) != 0 {
		t.Fatalf("read after the end = %+v, %v", b, err)
	}
	// An ack that moves the cursor meanwhile wins: commit neither moves it
	// back nor marks the end as delivered.
	_, from, next, err = s.readBatch(v.ID, mainRole)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.cursorPath(v.ID, mainRole), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.commitBatch(v.ID, mainRole, from, next, true); err != nil || ok {
		t.Fatalf("commit over a moved cursor = %v, %v", ok, err)
	}
	if cur, _ := s.cursor(v.ID, mainRole); cur != 1 {
		t.Fatalf("cursor = %d, want the moved 1", cur)
	}
	if _, err := os.Stat(s.forwardDonePath(v.ID, mainRole)); err == nil {
		t.Fatal("end marked delivered over a moved cursor")
	}
}

// hookEvent runs codexHook for one event and returns what it printed.
func hookEvent(t *testing.T, event map[string]any) string {
	t.Helper()
	in, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := codexHook(bytes.NewReader(in), &out); err != nil {
		t.Fatalf("%s: %v", event["hook_event_name"], err)
	}
	return out.String()
}

func TestCodexHookForwardsLedRooms(t *testing.T) {
	codex := fakeCodex(t)
	repo := testRepo(t)
	t.Setenv("PLUGIN_DATA", t.TempDir())
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	threads := []string{testThread, "01a10e5d-434f-7333-8e0a-dd119451f39c"}
	var rooms []string
	for i, thread := range threads {
		name := fmt.Sprintf("hooked-%d", i)
		started, err := invoke(repo, "", "start", name, "--agent", "codex")
		if err != nil {
			t.Fatal(err)
		}
		rooms = append(rooms, name)
		endWhenDone(t, repo, s, name)
		// Not a peer start: no forward, no note.
		if got := hookEvent(t, map[string]any{"hook_event_name": "PostToolUse", "session_id": thread, "tool_input": map[string]any{"command": "peer status"}, "tool_response": started}); got != "" {
			t.Fatalf("peer status started a forward: %s", got)
		}
		got := hookEvent(t, map[string]any{"hook_event_name": "PostToolUse", "session_id": thread, "cwd": repo,
			"tool_input": map[string]any{"command": "peer start " + name + " --agent codex"}, "tool_response": map[string]any{"output": started, "exit_code": 0}})
		if !strings.Contains(got, `"hookEventName":"PostToolUse"`) || !strings.Contains(got, "peer room "+name) {
			t.Fatalf("PostToolUse printed %q", got)
		}
		if ok, _ := s.forwarding(name, mainRole); !ok {
			t.Fatalf("no forward for %s", name)
		}
		if _, err := invoke(repo, "", "join", name, "reader"); err != nil {
			t.Fatal(err)
		}
		if _, err := invoke(repo, "for "+thread, "send", name, "--as", "reader"); err != nil {
			t.Fatal(err)
		}
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		log := queued(codex)
		if strings.Contains(log, threads[0]+"\npeer room hooked-0") && strings.Contains(log, threads[1]+"\npeer room hooked-1") {
			break
		}
		if time.Now().After(deadline) {
			var logs []string
			for _, room := range rooms {
				b, _ := os.ReadFile(filepath.Join(s.dir, "sessions", room, "forward-main.log"))
				logs = append(logs, room+": "+string(b))
			}
			t.Fatalf("both rooms were not queued:\n%s\nforward logs:\n%s", log, strings.Join(logs, "\n"))
		}
	}
	// Each session gets only its own room's message.
	for _, turn := range strings.Split(strings.TrimSuffix(queued(codex), "---\n"), "---\n") {
		thread, _, _ := strings.Cut(turn, "\n")
		if !strings.Contains(turn, "for "+thread) {
			t.Fatalf("a batch went to the wrong session:\n%s", turn)
		}
	}
	// A resume keeps a running forward; after a crash, it starts one again.
	if got := hookEvent(t, map[string]any{"hook_event_name": "SessionStart", "session_id": threads[0], "source": "resume"}); !strings.Contains(got, `"hookEventName":"SessionStart"`) {
		t.Fatalf("SessionStart with forward running printed %q", got)
	}
	h, _, _ := s.forwardHolder(rooms[0], mainRole)
	if err := syscall.Kill(h.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	eventually(t, "forward to die", func() bool { ok, _ := s.forwarding(rooms[0], mainRole); return !ok })
	if ok, _ := s.forwarding(rooms[1], mainRole); !ok {
		t.Fatal("the other session's forward stopped")
	}
	if got := hookEvent(t, map[string]any{"hook_event_name": "SessionStart", "session_id": threads[0], "source": "resume"}); !strings.Contains(got, `"hookEventName":"SessionStart"`) {
		t.Fatalf("SessionStart after a crash printed %q", got)
	}
	if h2, ok, _ := s.forwardHolder(rooms[0], mainRole); !ok || h2.PID == h.PID {
		t.Fatalf("no new forward after the crash: %+v", h2)
	}
	// Once the end is delivered, a resume starts nothing and says nothing.
	if _, err := invoke(repo, "", "end", rooms[0], "--as", "main"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the end to be delivered", func() bool {
		_, err := os.Stat(s.forwardDonePath(rooms[0], mainRole))
		return err == nil
	})
	if got := hookEvent(t, map[string]any{"hook_event_name": "SessionStart", "session_id": threads[0], "source": "resume"}); got != "" {
		t.Fatalf("SessionStart after the end printed %q", got)
	}
}

func TestCodexHookNeedsPluginData(t *testing.T) {
	t.Setenv("PLUGIN_DATA", "")
	if got := hookEvent(t, map[string]any{"hook_event_name": "SessionStart", "session_id": testThread}); got != "" {
		t.Fatalf("hook without PLUGIN_DATA printed %q", got)
	}
}

func TestCodexHookBindsOnlyItsOwnStart(t *testing.T) {
	fakeCodex(t)
	repo := testRepo(t)
	t.Setenv("PLUGIN_DATA", t.TempDir())
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	started, err := invoke(repo, "", "start", "owned", "--agent", "codex")
	if err != nil {
		t.Fatal(err)
	}
	endWhenDone(t, repo, s, "owned")
	post := func(thread, command string, response any) string {
		return hookEvent(t, map[string]any{"hook_event_name": "PostToolUse", "session_id": thread, "tool_input": map[string]any{"command": command}, "tool_response": response})
	}
	for command, response := range map[string]any{
		"peer status owned":           started,                                                          // not a start
		"peer start other":            started,                                                          // printed a room it did not start
		"peer start owned":            "peer: run this command inside the shared Git checkout",          // failed
		"peer start owned --agent cx": strings.Replace(started, `"started_at":"`, `"started_at":"1`, 1), // not this room's start
		"echo peer start owned":       started,                                                          // peer is not the command
		"peer start owned && peer invite owned reader --as main --agent claude": started, // not a plain start
	} {
		if got := post(testThread, command, response); got != "" {
			t.Fatalf("%q bound a room: %s", command, got)
		}
	}
	if ok, _ := s.forwarding("owned", mainRole); ok {
		t.Fatal("a forward started without a peer start")
	}
	// Only tool_input.command counts, not other fields of the call.
	if got := hookEvent(t, map[string]any{"hook_event_name": "PostToolUse", "session_id": testThread,
		"tool_input": map[string]any{"command": "peer status owned", "description": "peer start owned"}, "tool_response": started}); got != "" {
		t.Fatalf("a description bound a room: %s", got)
	}
	// Another session's forward holds the room: this session gets no note,
	// and that forward stays.
	other := "01a10e5d-434f-7333-8e0a-dd119451f39c"
	if got := post(other, "peer start owned --agent codex", started); got == "" {
		t.Fatal("the other session's start bound nothing")
	}
	if got := post(testThread, "peer start owned --agent codex", started); got != "" {
		t.Fatalf("a second session was told it gets the room: %s", got)
	}
	if h, ok, _ := s.forwardHolder("owned", mainRole); !ok || h.Thread != other {
		t.Fatalf("holder after the other SessionEnd = %+v, %v", h, ok)
	}
}

func TestCodexHookKeepsBindingsOfParallelStarts(t *testing.T) {
	fakeCodex(t)
	repo := testRepo(t)
	data := t.TempDir()
	t.Setenv("PLUGIN_DATA", data)
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, name := range []string{"par-a", "par-b"} {
		started, err := invoke(repo, "", "start", name, "--agent", "codex")
		if err != nil {
			t.Fatal(err)
		}
		endWhenDone(t, repo, s, name)
		event, _ := json.Marshal(map[string]any{"hook_event_name": "PostToolUse", "session_id": testThread, "tool_input": map[string]any{"command": "peer start " + name}, "tool_response": started})
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := codexHook(bytes.NewReader(event), io.Discard); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	bindings, err := readBindings(filepath.Join(data, "sessions", testThread))
	if err != nil || len(bindings) != 2 {
		t.Fatalf("bindings after two parallel starts = %+v, %v", bindings, err)
	}
}
