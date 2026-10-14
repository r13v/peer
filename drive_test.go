package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const driveThread = "01a12f00-0000-7000-8000-000000000001"

// fakeTurns puts a codex on PATH that records each turn in dir/turn-N:
// its arguments on the first line, their line breaks made spaces, then its
// stdin. Each turn reports
// driveThread and completes, unless dir/fail exists: then it fails like a
// model at capacity. With dir/cut it exits 0 without completing the turn.
func fakeTurns(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
d="` + dir + `"
n=$(ls "$d" | grep -c '^turn-')
f="$d/turn-$((n + 1))"
printf '%s\n' "$*" | tr '\n' ' ' > "$f.tmp"
echo >> "$f.tmp"
cat >> "$f.tmp"
mv "$f.tmp" "$f"
echo '{"type":"thread.started","thread_id":"` + driveThread + `"}'
if [ -f "$d/fail" ]; then echo '{"type":"turn.failed","error":{"message":"Selected model is at capacity"}}'; exit 1; fi
if [ -f "$d/cut" ]; then exit 0; fi
echo '{"type":"turn.completed","usage":{}}'
`
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	old := drivePoll
	drivePoll = 10 * time.Millisecond
	t.Cleanup(func() { drivePoll = old })
	return dir
}

// turnRecord waits for the fake agent's turn n and returns its arguments
// and stdin.
func turnRecord(t *testing.T, dir string, n int) (string, string) {
	t.Helper()
	path := filepath.Join(dir, "turn-"+strconv.Itoa(n))
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil {
			args, stdin, _ := strings.Cut(string(b), "\n")
			return strings.TrimSpace(args), stdin
		}
	}
	t.Fatalf("turn %d did not run", n)
	return "", ""
}

// unread is how many messages wait for role in room id.
func unread(t *testing.T, repo, id, role string) int {
	t.Helper()
	got, err := invoke(repo, "", "status", id)
	if err != nil {
		t.Fatal(err)
	}
	var v session
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatalf("status: %v: %s", err, got)
	}
	return v.member(role).Unread
}

// TestDriveWakesMemberWithEachBatch runs a member whose messages peer
// delivers: it is idle between turns, resumes its session with each batch,
// and is the role's only reader.
func TestDriveWakesMemberWithEachBatch(t *testing.T) {
	dir := fakeTurns(t)
	repo := testRepo(t)
	v := startRoom(t, repo, "wake")
	if _, err := invoke(repo, "", "invite", v.ID, "critic", "--as", "main", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.drive(context.Background(), v.ID, "critic", "edge cases", new(syncBuffer)) }()

	if _, err := invoke(repo, "", "wait", v.ID, "--as", "critic", "--timeout", "0"); err == nil || !strings.Contains(err.Error(), "peer delivers room") {
		t.Fatalf("a second reader of the role was allowed: %v", err)
	}
	if err := s.drive(context.Background(), v.ID, "critic", "", new(syncBuffer)); err == nil || !strings.Contains(err.Error(), "already delivers") {
		t.Fatalf("a second drive of the role was allowed: %v", err)
	}
	// Without messages the member has nothing to answer, so no turn runs.
	time.Sleep(100 * time.Millisecond)
	if files, _ := filepath.Glob(filepath.Join(dir, "turn-*")); len(files) != 0 {
		t.Fatalf("a turn ran without messages: %q", files)
	}

	// The first batch starts the session together with the prompt.
	if _, err := invoke(repo, "check the parser", "send", v.ID, "--as", "main", "--to", "critic"); err != nil {
		t.Fatal(err)
	}
	args, _ := turnRecord(t, dir, 1)
	if strings.Contains(args, "resume") || !strings.Contains(args, "edge cases") || !strings.Contains(args, "Do not run peer wait") || !strings.Contains(args, "check the parser") {
		t.Fatalf("first turn is not the member's prompt with the batch: %s", args)
	}
	if _, err := invoke(repo, "and the tokens", "send", v.ID, "--as", "main", "--to", "critic"); err != nil {
		t.Fatal(err)
	}
	args, stdin := turnRecord(t, dir, 2)
	if !strings.Contains(args, " resume ") || !strings.HasSuffix(args, driveThread+" -") || !strings.Contains(stdin, "and the tokens") {
		t.Fatalf("the batch did not resume the session: %s / %s", args, stdin)
	}
	for deadline := time.Now().Add(5 * time.Second); unread(t, repo, v.ID, "critic") != 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a delivered batch stayed unread")
		}
	}

	// A failed turn stops the member and leaves its batch unread, for the
	// role invited again.
	if err := os.WriteFile(filepath.Join(dir, "fail"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "and the lexer", "send", v.ID, "--as", "main", "--to", "critic"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("drive went on after a failed turn")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drive did not stop after a failed turn")
	}
	if n := unread(t, repo, v.ID, "critic"); n != 1 {
		t.Fatalf("the failed turn's batch: unread %d, want 1", n)
	}
}

// TestDriveNeedsCompletedTurn keeps a batch unread when the agent exits
// with status 0 but never reports that its turn completed.
func TestDriveNeedsCompletedTurn(t *testing.T) {
	dir := fakeTurns(t)
	repo := testRepo(t)
	v := startRoom(t, repo, "cut")
	if _, err := invoke(repo, "", "invite", v.ID, "critic", "--as", "main", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.drive(context.Background(), v.ID, "critic", "", new(syncBuffer)) }()
	if _, err := invoke(repo, "start", "send", v.ID, "--as", "main", "--to", "critic"); err != nil {
		t.Fatal(err)
	}
	turnRecord(t, dir, 1)
	if err := os.WriteFile(filepath.Join(dir, "cut"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "review it", "send", v.ID, "--as", "main", "--to", "critic"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "without completing") {
			t.Fatalf("drive = %v; want the incomplete turn", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drive went on after an incomplete turn")
	}
	if n := unread(t, repo, v.ID, "critic"); n != 1 {
		t.Fatalf("the incomplete turn's batch: unread %d, want 1", n)
	}
}

// TestResultEndsTurn checks that only a successful result completes a turn,
// so a failed Claude or Copilot turn leaves its batch unread.
func TestResultEndsTurn(t *testing.T) {
	for events, want := range map[string]bool{
		`{"type":"result","subtype":"success","is_error":false}`: true,
		`{"type":"result","subtype":"success","is_error":true}`:  false,
		`{"type":"assistant","message":{}}`:                      false,
		`{"type":"result","sessionId":"s","exitCode":0}`:         true,
		`{"type":"result","sessionId":"s","exitCode":1}`:         false,
		`{"type":"result","sessionId":"s","usage":{}}`:           false,
		`{"type":"assistant.turn_end","data":{"turnId":"0"}}`:    false,
	} {
		if _, got := copyEvents(strings.NewReader(events+"\n"), new(syncBuffer)); got != want {
			t.Errorf("%s: completed = %v, want %v", events, got, want)
		}
	}
}

func TestDriveStopsWhenRoomEnds(t *testing.T) {
	dir := fakeTurns(t)
	repo := testRepo(t)
	v := startRoom(t, repo, "short")
	if _, err := invoke(repo, "", "invite", v.ID, "critic", "--as", "main", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.drive(context.Background(), v.ID, "critic", "", new(syncBuffer)) }()
	if _, err := invoke(repo, "", "end", v.ID, "--as", "main"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drive outlived the room")
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "turn-*")); len(files) != 0 {
		t.Fatalf("the room's end ran a turn: %q", files)
	}
}

// TestEndWaitsForRunningTurn lets a member finish the turn it runs, so its
// answer reaches the room, but not past endGrace.
func TestEndWaitsForRunningTurn(t *testing.T) {
	repo := testRepo(t)
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	old := endGrace
	t.Cleanup(func() { endGrace = old })
	hold := func(id string) *os.File {
		t.Helper()
		f, err := os.OpenFile(s.turnLockPath(id, "reader"), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			t.Fatal(err)
		}
		return f
	}

	endGrace = 10 * time.Second
	v := startRoom(t, repo, "busy")
	turn := hold(v.ID)
	done := make(chan error, 1)
	go func() { _, err := invoke(repo, "", "end", v.ID, "--as", "main"); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("end did not wait for the turn: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := invoke(repo, "done: review closed", "send", v.ID, "--as", "reader"); err != nil {
		t.Fatalf("the running turn's answer was refused: %v", err)
	}
	turn.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	endGrace = 50 * time.Millisecond
	v = startRoom(t, repo, "stuck")
	defer hold(v.ID).Close()
	if _, err := invoke(repo, "", "end", v.ID, "--as", "main"); err != nil {
		t.Fatalf("end did not give up on a long turn: %v", err)
	}
}

func TestMemberResumeRepeatsLimits(t *testing.T) {
	s := &store{dir: "/store/room"}
	argv, err := memberArgs(s, member{Agent: "codex", Model: "m1", Effort: "low"}, "/repo", "", "T", true)
	if err != nil {
		t.Fatal(err)
	}
	// codex exec takes the working directory and sandbox only before resume.
	resume := slices.Index(argv, "resume")
	for _, flag := range []string{"-C", "-s", "--add-dir"} {
		if i := slices.Index(argv, flag); i < 0 || i > resume {
			t.Errorf("%s is not before resume: %q", flag, argv)
		}
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{"approval_policy=never", "network_access=true", "-m m1", "model_reasoning_effort=low"} {
		if !strings.Contains(joined, want) {
			t.Errorf("resume lacks %s: %s", want, joined)
		}
	}
	if !slices.Equal(argv[len(argv)-2:], []string{"T", "-"}) {
		t.Errorf("resume does not read its prompt from stdin: %q", argv)
	}

	argv, err = memberArgs(s, member{Agent: "claude"}, "/repo", "", "S", true)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(argv, " ")
	for _, want := range []string{"--resume S", "--settings", "--tools", "--permission-mode dontAsk"} {
		if !strings.Contains(joined, want) {
			t.Errorf("claude resume lacks %s: %s", want, joined)
		}
	}
	if slices.Contains(argv, "--") {
		t.Errorf("claude resume takes a prompt argument: %s", joined)
	}
	argv, _ = memberArgs(s, member{Agent: "claude"}, "/repo", "p", "S", false)
	if joined = strings.Join(argv, " "); !strings.HasSuffix(joined, "--session-id S -- p") {
		t.Errorf("claude's first turn does not name its session: %s", joined)
	}

	// Copilot resumes with the same --session-id and reads the batch from
	// stdin, so a resumed turn has no -p.
	argv, err = memberArgs(s, member{Agent: "copilot", Model: "m1", Effort: "low"}, "/repo", "", "S", true)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(argv, " ")
	for _, want := range []string{"-C /repo", "--session-id S", "--sandbox", "--add-dir /store/room", "--excluded-tools create edit", "--deny-tool write", "--model m1", "--reasoning-effort low"} {
		if !strings.Contains(joined, want) {
			t.Errorf("copilot resume lacks %s: %s", want, joined)
		}
	}
	if slices.Contains(argv, "-p") {
		t.Errorf("copilot resume takes a prompt argument: %s", joined)
	}
}

func TestExitReasonIsFromLatestRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reader.log")
	log := `{"type":"error","message":"old failure"}
[peer] exited with status 1
{"type":"item.completed","item":{"type":"command_execution","command":"false","exit_code":1,"status":"failed"}}
peer: codex turn failed: exit status 1
[peer] exited with status 1
`
	if err := os.WriteFile(path, []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	if got := exitReason(path); got != "codex turn failed: exit status 1" {
		t.Fatalf("exitReason = %q", got)
	}
}

func TestHelpAndSendHints(t *testing.T) {
	repo := testRepo(t)
	outside := t.TempDir()
	for _, args := range [][]string{{"help"}, {"--help"}, {"send", "x", "--help"}, {"version", "--help"}, {"skills", "-h"}, {"board", "--help"}} {
		for _, cwd := range []string{repo, outside} {
			if got, err := invoke(cwd, "", args...); err != nil || !strings.HasPrefix(got, "usage:") {
				t.Fatalf("%q in %s = %q, %v; want the usage", args, cwd, got, err)
			}
		}
	}
	v := startRoom(t, repo, "hints")
	if _, err := invoke(repo, "", "send", v.ID, "--as", "main", "--text", "--help"); err != nil {
		t.Fatalf("--help as the text of a message: %v", err)
	}
	for _, args := range [][]string{{"send", v.ID, "--as", "main", "--message", "hi"}, {"send", v.ID, "--as", "main", "hi"}} {
		if _, err := invoke(repo, "", args...); err == nil || !strings.Contains(err.Error(), "--text TEXT or on stdin") {
			t.Fatalf("%q: %v; want a hint", args, err)
		}
	}
}

// TestStoppedDriveLetsAgentCleanUp checks that a stopped drive sends the
// agent TERM rather than KILL, so the agent can stop the commands it ran
// outside the member's process group, as Copilot does.
func TestStoppedDriveLetsAgentCleanUp(t *testing.T) {
	dir := fakeTurns(t)
	// The agent's child stands for a command it runs outside the member's
	// group; the agent stops it only on TERM.
	script := "#!/bin/sh\nsleep 30 >/dev/null 2>&1 &\nchild=$!\necho $child > \"" + dir + "/child\"\ntrap 'kill $child; wait $child; exit 0' TERM\necho > \"" + dir + "/turn-1\"\nwait\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	repo := testRepo(t)
	v := startRoom(t, repo, "stop")
	if _, err := invoke(repo, "", "invite", v.ID, "critic", "--as", "main", "--agent", "codex"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.drive(ctx, v.ID, "critic", "", new(syncBuffer)) }()
	if _, err := invoke(repo, "start", "send", v.ID, "--as", "main", "--to", "critic"); err != nil {
		t.Fatal(err)
	}
	turnRecord(t, dir, 1)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drive outlived its stop")
	}
	b, err := os.ReadFile(filepath.Join(dir, "child"))
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	if err := syscall.Kill(child, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("the agent's child outlived the stop: the agent got no TERM to clean up with")
	}
}
