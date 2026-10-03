package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// realStartMember is the hook TestMain replaces.
var realStartMember func([]string, string, []string, string, string) (int, error)

// realStopMember is the hook tests that stub stopMember restore.
var realStopMember func(int, string) error

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "stamp" { // startMember runs the test binary as peer
		if err := stamp(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	// A member that runs these tests inherits PEER_REPO, which would send
	// every fixture checkout to the member's room store.
	os.Unsetenv("PEER_REPO")
	realStartMember, realStopMember = startMember, stopMember
	startMember = func([]string, string, []string, string, string) (int, error) { return 0, nil }
	waitTimeout = time.Nanosecond
	os.Exit(m.Run())
}

func testRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q", repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	t.Setenv("PEER_HOME", filepath.Join(t.TempDir(), "data"))
	return repo
}

func invoke(repo, input string, args ...string) (string, error) {
	var out bytes.Buffer
	err := run(args, strings.NewReader(input), &out, repo)
	return out.String(), err
}

func TestRoleInstructions(t *testing.T) {
	cwd := t.TempDir() // Skill docs must work before a checkout or session exists.
	for role, want := range map[string]string{"flow": "peer skills worker", "member": "peer wait ID --as ROLE", "writer": "peer wait ID --as writer", "worker": "done:"} {
		got, err := invoke(cwd, "", "skills", role)
		if err != nil || !strings.HasPrefix(got, "# ") || !strings.Contains(got, want) {
			t.Fatalf("%s instructions unavailable: %s, %v", role, got, err)
		}
	}
	if _, err := invoke(cwd, "", "skills", "unknown"); err == nil {
		t.Fatal("unknown role accepted")
	}
}

func TestVersion(t *testing.T) {
	cwd := t.TempDir() // Works outside a Git checkout.
	for _, arg := range []string{"--version", "version"} {
		got, err := invoke(cwd, "", arg)
		if err != nil || got != version+"\n" {
			t.Fatalf("%s: %q, %v", arg, got, err)
		}
	}
	if _, err := invoke(cwd, "", "--version", "extra"); err == nil {
		t.Fatal("extra argument accepted")
	}
}

func TestInstallScriptVerifiesArchive(t *testing.T) {
	for _, tc := range []struct{ kernel, machine, archive string }{
		{"Darwin", "arm64", "peer_darwin_arm64.tar.gz"},
		{"Linux", "aarch64", "peer_linux_arm64.tar.gz"},
		{"Linux", "x86_64", "peer_linux_amd64.tar.gz"},
	} {
		dir := t.TempDir()
		assets := filepath.Join(dir, "assets")
		stage := filepath.Join(dir, "stage")
		fakeBin := filepath.Join(dir, "fake-bin")
		for _, path := range []string{assets, stage, fakeBin} {
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(stage, "peer"), []byte("verified binary"), 0600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("tar", "-czf", filepath.Join(assets, tc.archive), "-C", stage, "peer").CombinedOutput(); err != nil {
			t.Fatalf("package: %v: %s", err, out)
		}
		data, err := os.ReadFile(filepath.Join(assets, tc.archive))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if err := os.WriteFile(filepath.Join(assets, "checksums.txt"), []byte(fmt.Sprintf("%x  %s\n", sum, tc.archive)), 0600); err != nil {
			t.Fatal(err)
		}
		uname := fmt.Sprintf("#!/bin/sh\n[ \"$1\" = -s ] && echo %s || echo %s\n", tc.kernel, tc.machine)
		if err := os.WriteFile(filepath.Join(fakeBin, "uname"), []byte(uname), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte("#!/bin/sh\ncp \"$PEER_TEST_ASSETS/${2##*/}\" \"$4\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, "bin")
		env := append(os.Environ(), "HOME="+filepath.Join(dir, "home"), "PEER_INSTALL_DIR="+bin, "PEER_TEST_ASSETS="+assets, "PATH="+fakeBin+":"+os.Getenv("PATH"))
		runInstall := func() ([]byte, error) {
			cmd := exec.Command("sh", "scripts/install.sh")
			cmd.Env = env
			return cmd.CombinedOutput()
		}
		if out, err := runInstall(); err != nil {
			t.Fatalf("%s %s install: %v: %s", tc.kernel, tc.machine, err, out)
		}
		got, err := os.ReadFile(filepath.Join(bin, "peer"))
		if err != nil || string(got) != "verified binary" {
			t.Fatalf("missing verified CLI: %s, %v", got, err)
		}
		if err := os.WriteFile(filepath.Join(assets, tc.archive), []byte("corrupt"), 0600); err != nil {
			t.Fatal(err)
		}
		if out, err := runInstall(); err == nil || !strings.Contains(string(out), "checksum mismatch") {
			t.Fatalf("corrupt archive accepted: %s, %v", out, err)
		}
		got, err = os.ReadFile(filepath.Join(bin, "peer"))
		if err != nil || string(got) != "verified binary" {
			t.Fatalf("failed update replaced working binary: %s, %v", got, err)
		}
	}
}

func TestUpdateSkipsHomebrew(t *testing.T) {
	dir := t.TempDir()
	if _, err := invoke(dir, "", "update", "extra"); err == nil {
		t.Fatal("extra argument accepted")
	}
	for _, sub := range []string{"Caskroom/peer/0.1.2", "Cellar/peer/0.1.2/bin", "local/bin"} {
		target := filepath.Join(dir, sub, "peer")
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, nil, 0700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "peer")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		got, err := installDir(link)
		resolved, _ := filepath.EvalSymlinks(filepath.Dir(target))
		if sub == "local/bin" {
			if err != nil || got != resolved {
				t.Fatalf("%s: %q, %v", sub, got, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "brew upgrade") {
			t.Fatalf("%s: replaced a Homebrew copy: %q, %v", sub, got, err)
		}
	}
}

func TestMemberLogLine(t *testing.T) {
	for line, want := range map[string][]logEntry{
		"codex plain progress\n":             {{Kind: logRaw, Text: "codex plain progress"}},
		`{"type":"system","subtype":"init"}`: nil,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Reviewing"},{"type":"tool_use","name":"Bash","input":{"command":"peer wait --as claude"}}]}}`: {{Kind: logText, Text: "Reviewing"}, {Kind: logTool, Text: "Bash peer wait --as claude"}},
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"main.go"}}]}}`:                                                {{Kind: logTool, Text: `Read {"file_path":"main.go"}`}},
		`{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"text","text":"no such file"}],"is_error":true}]}}`:                              {{Kind: logOutput, Text: "no such file", Failed: true}},
		`{"type":"result","result":"done"}`: nil,
		`{"type":"result","subtype":"error_max_turns","is_error":true,"errors":["Reached maximum turns"]}`:                                                             {{Kind: logRaw, Text: "result: Reached maximum turns", Failed: true}},
		`{"type":"result","subtype":"error_during_execution","is_error":true}`:                                                                                         {{Kind: logRaw, Text: "result: error_during_execution", Failed: true}},
		`{"type":"item.started","item":{"type":"command_execution","command":"ls"}}`:                                                                                   nil,
		`{"type":"item.completed","item":{"type":"agent_message","text":"ok"}}`:                                                                                        {{Kind: logText, Text: "ok"}},
		`{"type":"item.completed","item":{"type":"command_execution","command":"ls /x","aggregated_output":"ls: /x: No such file\n","exit_code":1,"status":"failed"}}`: {{Kind: logTool, Text: "ls /x", Failed: true}, {Kind: logOutput, Text: "ls: /x: No such file"}},
		`{"type":"error","message":"stream disconnected"}`:                                                                                                             {{Kind: logRaw, Text: "stream disconnected", Failed: true}},
		`{"type":"turn.completed","usage":{}}`:                                                                                                                         nil,
		`{"type":"thread.started","thread_id":"t"}`:                                                                                                                    nil,
		`{"type":"item.completed","item":{"type":"new_kind"}}`:                                                                                                         {{Kind: logRaw, Text: `{"type":"item.completed","item":{"type":"new_kind"}}`}},
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hi"}}`:                                                                         nil,
		`{"type":"tool_execution_end","toolName":"bash","result":{"content":[{"type":"text","text":"hi"}]},"isError":false}`:                                           nil,
		`{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"prompt"}]}}`:                                                                 nil,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Reviewing"},{"type":"toolCall","name":"bash","arguments":{"command":"peer wait"}}],"stopReason":"toolUse"}}`: {{Kind: logText, Text: "Reviewing"}, {Kind: logTool, Text: "bash peer wait"}},
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","name":"read","arguments":{"path":"main.go"}}]}}`:                                                                {{Kind: logTool, Text: `read {"path":"main.go"}`}},
		`{"type":"message_end","message":{"role":"toolResult","toolName":"bash","content":[{"type":"text","text":"hi\n\nCommand exited with code 1"}],"isError":true}}`:                                     {{Kind: logOutput, Text: "hi\n\nCommand exited with code 1", Failed: true}},
		`{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"429 rate limited"}}`:                                                                         {{Kind: logRaw, Text: "429 rate limited", Failed: true}},
		`{"type":"result","is_error":true,"result":"boom"}`: {{Kind: logRaw, Text: "result: boom", Failed: true}},
		`{"type":"new_event","x":1}`:                        {{Kind: logRaw, Text: `{"type":"new_event","x":1}`}},
	} {
		if got := memberLogLine([]byte(line)); !slices.Equal(got, want) {
			t.Fatalf("memberLogLine(%q) = %+v, want %+v", line, got, want)
		}
	}
}

func TestStartMemberStampsOutputAndKeepsStatus(t *testing.T) {
	dir := t.TempDir()
	logPath, exitPath := filepath.Join(dir, "member.log"), filepath.Join(dir, "member.exit")
	if _, err := realStartMember([]string{"sh", "-c", "echo out; echo err >&2; printf tail; exit 3"}, dir, nil, logPath, exitPath); err != nil {
		t.Fatal(err)
	}
	var data []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		data, _ = os.ReadFile(logPath)
		if bytes.Contains(data, []byte("[peer] exited")) {
			break
		}
	}
	if status, _ := os.ReadFile(exitPath); strings.TrimSpace(string(status)) != "3" {
		t.Fatalf("exit status %q, want 3", status)
	}
	var got []string
	for _, line := range strings.SplitAfter(strings.TrimSuffix(string(data), "\n"), "\n") {
		at, rest := stamped([]byte(line))
		if at.IsZero() {
			t.Fatalf("line not stamped: %q", line)
		}
		got = append(got, strings.TrimSuffix(string(rest), "\n"))
	}
	if want := []string{"out", "err", "tail[peer] exited with status 3"}; !slices.Equal(got, want) {
		t.Fatalf("log lines %q, want %q", got, want)
	}
}

func TestDefaultHome(t *testing.T) {
	repo := testRepo(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PEER_HOME", "")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".peer", "repos"); filepath.Dir(s.dir) != want {
		t.Fatalf("store directory = %q, want under %q", s.dir, want)
	}
}

func TestLinks(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "my #repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PEER_EDITOR_URL", "vscode://file/{path}:{line}")
	p := &printer{repo: repo, color: true}
	got := p.links("see main.go:12, not missing.go or https://example.com/x")
	want := "see \x1b]8;;vscode://file/" + strings.ReplaceAll(strings.ReplaceAll(filepath.Join(repo, "main.go"), " ", "%20"), "#", "%23") + ":12\x1b\\main.go:12\x1b]8;;\x1b\\, not missing.go or https://example.com/x"
	if got != want {
		t.Fatalf("links:\n got %q\nwant %q", got, want)
	}
}

func TestNewPrinterIgnoresDevNull(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if p := newPrinter(f, t.TempDir()); p.color {
		t.Fatal("/dev/null was treated as a terminal")
	}
}

func TestBodyMarkdown(t *testing.T) {
	p := &printer{repo: t.TempDir(), color: true}
	got := p.body("1. use `peer` **now**\n- done")
	want := "  \x1b[2m1.\x1b[22m use \x1b[33m`peer`\x1b[39m \x1b[1mnow\x1b[22m\n  \x1b[2m-\x1b[22m done"
	if got != want {
		t.Fatalf("body:\n got %q\nwant %q", got, want)
	}
}

func TestAuthorDimsAgent(t *testing.T) {
	p := &printer{repo: t.TempDir(), color: true}
	v := session{Members: []member{{Role: writer, Agent: "claude"}, {Role: "reader"}}}
	if got, want := p.author(v, writer, true), "\x1b[1;36mwriter\x1b[0m \x1b[2m(claude)\x1b[0m"; got != want {
		t.Fatalf("sender:\n got %q\nwant %q", got, want)
	}
	if got, want := p.author(v, writer, false), "\x1b[1;36mwriter\x1b[0m"; got != want {
		t.Fatalf("recipient:\n got %q\nwant %q", got, want)
	}
	if got, want := p.author(v, "reader", true), "\x1b[1;35mreader\x1b[0m"; got != want {
		t.Fatalf("no agent:\n got %q\nwant %q", got, want)
	}
}

// startRoom starts a room as the writer, joins a reader to it, and takes
// the join notice off the writer's queue.
func startRoom(t *testing.T, repo, name string) session {
	t.Helper()
	started, err := invoke(repo, "", "start", name, "--agent", "claude")
	var v session
	if err != nil || json.Unmarshal([]byte(started), &v) != nil {
		t.Fatalf("start %s: %q, %v", name, started, err)
	}
	joined, err := invoke(repo, "", "join", v.ID, "reader")
	if err != nil || json.Unmarshal([]byte(joined), &v) != nil {
		t.Fatalf("join reader: %q, %v", joined, err)
	}
	if got, err := invoke(repo, "", "wait", v.ID, "--as", "writer"); err != nil || !strings.Contains(got, `"from":"peer"`) {
		t.Fatalf("writer missed the join notice: %s, %v", got, err)
	}
	return v
}

func TestPairSession(t *testing.T) {
	repo := testRepo(t)
	first := startRoom(t, repo, "csv-export")
	if first.ID != "csv-export" || strings.Join(first.members(), ",") != "writer,reader" || first.Members[0].Agent != "claude" {
		t.Fatalf("wrong room: %+v", first)
	}
	id := first.ID
	if _, err := invoke(repo, "proposal", "send", id, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "proposal", "send", "--as", "writer"); err == nil {
		t.Fatal("send without a room ID accepted")
	}
	if _, err := invoke(repo, "intrusion", "send", id, "--as", "codex"); err == nil {
		t.Fatal("nonparticipant sent a message")
	}
	if _, err := invoke(repo, "", "wait", id, "--as", "codex"); err == nil {
		t.Fatal("nonparticipant read a message")
	}
	if got, err := invoke(repo, "", "wait", id, "--as", "writer"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("writer consumed its own message: %s, %v", got, err)
	}
	got, err := invoke(repo, "", "wait", id, "--as", "reader")
	if err != nil || !strings.Contains(got, `"text":"proposal"`) {
		t.Fatalf("reviewer missed proposal: %s, %v", got, err)
	}
	if got, err := invoke(repo, "", "wait", id, "--as", "reader"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("message delivered twice: %s, %v", got, err)
	}
	if _, err := invoke(repo, "check line 12", "send", id, "--as", "reader"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", id, "--as", "writer"); err != nil || !strings.Contains(got, `"text":"check line 12"`) {
		t.Fatalf("writer missed review: %s, %v", got, err)
	}
	if _, err := invoke(repo, "", "log"); err == nil {
		t.Fatal("log without an ID accepted")
	}
	log, err := invoke(repo, "", "log", id)
	if err != nil || !strings.Contains(log, "proposal") || !strings.Contains(log, "check line 12") || !strings.Contains(log, "writer (claude) → all") || !strings.Contains(log, "peer → writer\n") {
		t.Fatalf("transcript incomplete: %s, %v", log, err)
	}
	if _, err := invoke(repo, "", "end", id, "--as", "reader"); err == nil {
		t.Fatal("reviewer ended writer's session")
	}
	if _, err := invoke(repo, "review complete", "send", id, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", id, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", id, "--as", "reader"); err != nil || !strings.Contains(got, `"text":"review complete"`) {
		t.Fatalf("closing message lost when session ended: %s, %v", got, err)
	}
	if _, err := invoke(repo, "too late", "send", id, "--as", "reader"); err == nil {
		t.Fatal("send accepted after end")
	}
	if _, err := invoke(repo, "", "join", id, "late"); err == nil {
		t.Fatal("join accepted after end")
	}
	if again := startRoom(t, repo, "csv-export"); again.ID != "csv-export-2" {
		t.Fatalf("ended room name reused: %s", again.ID)
	}
	history, err := invoke(repo, "", "history")
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(history), "\n")
	if len(rows) != 2 || !strings.HasPrefix(rows[0], "csv-export-2 ") || !strings.HasSuffix(rows[0], "active") || !strings.HasPrefix(rows[1], id+" ") || !strings.Contains(rows[1], "writer, reader    4 msgs") || !strings.HasSuffix(rows[1], "ended") {
		t.Fatalf("unexpected history:\n%s", history)
	}
}

func TestMembersJoinAndAddress(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "team")
	if _, err := invoke(repo, "task", "send", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "join", v.ID, "reader"); err == nil || !strings.Contains(err.Error(), "reader-2") {
		t.Fatalf("taken role joined again: %v", err)
	}
	for _, role := range []string{writer, human, system, "Bad", "../x"} {
		if _, err := invoke(repo, "", "join", v.ID, role); err == nil {
			t.Fatalf("role %q joined", role)
		}
	}
	joined, err := invoke(repo, "", "join", v.ID, "test-expert", "--agent", "copilot")
	if err != nil || !strings.Contains(joined, `"role":"test-expert","agent":"copilot"`) {
		t.Fatalf("join: %s, %v", joined, err)
	}
	if got, _ := invoke(repo, "", "wait", v.ID, "--as", "writer"); !strings.Contains(got, `"text":"test-expert · copilot, readonly joined"`) {
		t.Fatalf("writer missed the join notice: %s", got)
	}
	// A late member replays what was sent to everyone before it joined.
	if got, _ := invoke(repo, "", "wait", v.ID, "--as", "test-expert"); !strings.Contains(got, `"text":"task"`) {
		t.Fatalf("late member missed the history: %s", got)
	}
	if _, err := invoke(repo, "tests only", "send", v.ID, "--as", "writer", "--to", "test-expert"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{{"--as", "writer", "--to", "writer"}, {"--as", "writer", "--to", "nobody"}} {
		if _, err := invoke(repo, "x", append([]string{"send", v.ID}, bad...)...); err == nil {
			t.Fatalf("send %q accepted", bad)
		}
	}
	if got, _ := invoke(repo, "", "wait", v.ID, "--as", "test-expert"); !strings.Contains(got, `"text":"tests only"`) {
		t.Fatalf("addressed message lost: %s", got)
	}
	if got, _ := invoke(repo, "", "wait", v.ID, "--as", "reader"); !strings.Contains(got, `"text":"task"`) {
		t.Fatalf("reader missed the broadcast: %s", got)
	}
	if got, _ := invoke(repo, "", "wait", v.ID, "--as", "reader"); !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("reader got a message addressed to another: %s", got)
	}
	if _, err := invoke(repo, "all hear", "send", v.ID, "--as", "test-expert"); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{writer, "reader"} {
		if got, _ := invoke(repo, "", "wait", v.ID, "--as", role); !strings.Contains(got, `"text":"all hear"`) {
			t.Fatalf("%s missed a member's broadcast: %s", role, got)
		}
	}
	if got, _ := invoke(repo, "", "wait", v.ID, "--as", "test-expert"); !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("sender got its own broadcast: %s", got)
	}
}

func TestMemberExit(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "crash")
	if _, err := invoke(repo, "", "join", v.ID, "tests"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "task", "send", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	exit := func(role string, status int) {
		t.Helper()
		if _, err := realStartMember([]string{"sh", "-c", fmt.Sprintf("exit %d", status)}, repo, nil, s.logPath(v.ID, role), s.exitPath(v.ID, role)); err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("%d\n", status)
		deadline := time.Now().Add(3 * time.Second)
		for {
			if b, err := os.ReadFile(s.exitPath(v.ID, role)); err == nil && string(b) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("wrapper wrote no exit status")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	exit("tests", 1)
	for _, want := range []string{`"text":"tests joined"`, `"text":"tests exited with status 1"`} {
		if got, err := invoke(repo, "", "wait", v.ID, "--as", "writer"); err != nil || !strings.Contains(got, want) {
			t.Fatalf("writer got %s, %v; want %s", got, err, want)
		}
	}
	if _, err := invoke(repo, "", "join", v.ID, "tests"); err != nil {
		t.Fatalf("exited member not replaceable: %v", err)
	}
	if _, err := os.Stat(s.exitPath(v.ID, "tests")); !os.IsNotExist(err) {
		t.Fatalf("stale exit file kept: %v", err)
	}
	if _, err := invoke(repo, "", "wait", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	// Rooms stay open when every member has exited; the writer ends them.
	exit("reader", 2)
	if got, err := invoke(repo, "", "wait", v.ID, "--as", "writer"); err != nil || !strings.Contains(got, "reader exited with status 2") {
		t.Fatalf("writer missed the reader exit: %s, %v", got, err)
	}
	if _, err := invoke(repo, "", "wait", v.ID, "--as", "reader"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "join", v.ID, "reader"); err != nil {
		t.Fatalf("sole exited member not replaceable: %v", err)
	}
	if got, _ := invoke(repo, "", "wait", v.ID, "--as", "reader"); !strings.Contains(got, `"text":"task"`) {
		t.Fatalf("replacement did not replay the room: %s", got)
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
}

func TestInvite(t *testing.T) {
	var launched [][]string
	var dirs, logs []string
	var envs [][]string
	fail := false
	startMember = func(argv []string, dir string, env []string, logPath, _ string) (int, error) {
		if fail {
			return 0, errors.New("not installed")
		}
		launched, dirs, envs, logs = append(launched, argv), append(dirs, dir), append(envs, env), append(logs, logPath)
		return 0, nil
	}
	t.Cleanup(func() { startMember = func([]string, string, []string, string, string) (int, error) { return 0, nil } })
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	models := `{"models":[{"slug":"gpt-6-sol","visibility":"list","priority":3},{"slug":"gpt-7-sol","visibility":"hide","priority":0},{"slug":"gpt-6.1-sol","visibility":"list","priority":1},{"slug":"gpt-6-astra","visibility":"list","priority":0}]}`
	if err := os.WriteFile(filepath.Join(codexHome, "models_cache.json"), []byte(models), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		agent, model, focus string
		worker              bool
	}{
		{"codex", "", "Follow peer skills member.", false},
		{"claude", "opus", "Follow peer skills member.", false},
		{"claude", "", "Follow peer skills member.", false},
		{"pi", "", "a worker in the shared checkout", true},
		{"codex", "gpt-5", "a worker in the shared checkout", true},
	} {
		launched, dirs, envs, logs = nil, nil, nil, nil
		repo := filepath.Join(t.TempDir(), "my repo & co")
		if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		t.Setenv("PEER_HOME", filepath.Join(t.TempDir(), "data"))
		var v session
		if started, err := invoke(repo, "", "start", "review"); err != nil || json.Unmarshal([]byte(started), &v) != nil {
			t.Fatalf("start output is not one session: %q, %v", started, err)
		}
		args := []string{"invite", v.ID, "test-expert", "--as", "writer", "--agent", tc.agent, "--brief", "edge cases"}
		if tc.worker {
			args = append(args, "--worker")
		}
		if tc.model != "" {
			args = append(args, "--model", tc.model)
		}
		if out, err := invoke(repo, "", args...); err != nil || !strings.Contains(out, `"role":"test-expert","agent":"`+tc.agent+`"`) {
			t.Fatalf("invite: %q, %v", out, err)
		}
		resolved, _ := filepath.EvalSymlinks(repo)
		if len(launched) != 1 || launched[0][0] != tc.agent || dirs[0] != resolved || !slices.Equal(envs[0], []string{"PEER_REPO=" + resolved}) || filepath.Base(logs[0]) != "test-expert.log" || filepath.Base(filepath.Dir(logs[0])) != v.ID {
			t.Fatalf("%+v: want one %s in the checkout, got argv %q, dirs %q, env %q, logs %q", tc, tc.agent, launched, dirs, envs, logs)
		}
		joined := strings.Join(launched[0], " ")
		prompt := launched[0][len(launched[0])-1]
		if !strings.Contains(prompt, "the test-expert in peer room "+v.ID) || !strings.Contains(prompt, tc.focus) || !strings.Contains(prompt, "edge cases") || !strings.Contains(prompt, "Nobody reads this chat") {
			t.Fatalf("wrong prompt: %q", prompt)
		}
		if tc.agent == "codex" && !strings.Contains(joined, "-C "+resolved) {
			t.Fatalf("codex runs outside the checkout: %q", launched[0])
		}
		if tc.model != "" && !strings.Contains(joined, " "+tc.model+" ") {
			t.Fatalf("model %s not passed: %q", tc.model, launched[0])
		}
		if tc.agent == "claude" && tc.model == "" && !strings.Contains(joined, " --model opus --effort medium ") {
			t.Fatalf("claude does not default to opus at medium effort: %q", launched[0])
		}
		if tc.agent == "codex" && tc.model == "" && !strings.Contains(joined, " -m gpt-6.1-sol -c model_reasoning_effort=medium ") {
			t.Fatalf("codex does not default to the latest sol at medium effort: %q", launched[0])
		}
	}
	repo := testRepo(t)
	v := startRoom(t, repo, "rules")
	for _, args := range [][]string{
		{"invite", v.ID, "docs", "--as", "reader", "--agent", "codex"},
		{"invite", v.ID, "docs", "--as", "writer", "--agent", "copilot"},
		{"invite", v.ID, "reader", "--as", "writer", "--agent", "codex"},
		{"invite", v.ID, "--as", "writer", "--agent", "codex"},
		{"invite", v.ID, "docs", "--as", "writer", "--agent", "codex", "--headed"},
		{"invite", v.ID, "docs", "--as", "writer", "--agent", "codex", "--worktree"},
		{"invite", v.ID, "docs", "--as", "writer", "--agent", "codex", "--worker", "--worktree"}, // no commit yet
	} {
		if _, err := invoke(repo, "", args...); err == nil {
			t.Fatalf("%q accepted", args)
		}
	}
	fail = true
	if _, err := invoke(repo, "", "invite", v.ID, "docs", "--as", "writer", "--agent", "codex"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("failed launch reported %v", err)
	}
	if got, _ := invoke(repo, "", "status", v.ID); !strings.Contains(got, `"role":"docs","agent":"codex","exited":true`) {
		t.Fatalf("failed launch left docs active: %s", got)
	}
	fail = false
	if _, err := invoke(repo, "", "invite", v.ID, "docs", "--as", "writer", "--agent", "codex"); err != nil {
		t.Fatalf("role not free after a failed launch: %v", err)
	}
}

// TestWorktreeWorker runs a worker in its own worktree: it reaches the
// writer's room through PEER_REPO, and a worker invited again in its role
// finds the edits left behind.
func TestWorktreeWorker(t *testing.T) {
	var dir string
	var env []string
	startMember = func(_ []string, d string, e []string, _, _ string) (int, error) { dir, env = d, e; return 0, nil }
	t.Cleanup(func() { startMember = func([]string, string, []string, string, string) (int, error) { return 0, nil } })
	repo := testRepo(t)
	if out, err := exec.Command("git", "-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "base").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	v := startRoom(t, repo, "split")
	if _, err := invoke(repo, "", "invite", v.ID, "api", "--as", "writer", "--agent", "claude", "--worker", "--worktree"); err != nil {
		t.Fatal(err)
	}
	s, _ := openStore(repo)
	cur, _ := s.load(v.ID)
	m := cur.member("api")
	head, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if m == nil || !m.Worker || m.Worktree != dir || m.Base != strings.TrimSpace(string(head)) || !strings.HasPrefix(m.Branch, "peer/") || !strings.HasSuffix(m.Branch, "/"+v.ID+"/api") {
		t.Fatalf("worker = %+v, launched in %s", m, dir)
	}
	if branch, _ := exec.Command("git", "-C", dir, "branch", "--show-current").Output(); strings.TrimSpace(string(branch)) != m.Branch {
		t.Fatalf("worktree is on %q, want %s", branch, m.Branch)
	}
	// The worker's peer commands run in its worktree with the env it got.
	if !slices.Equal(env, []string{"PEER_REPO=" + s.repo}) {
		t.Fatalf("worker env = %q", env)
	}
	t.Setenv("PEER_REPO", s.repo)
	if _, err := invoke(dir, "hello", "send", v.ID, "--as", "api"); err != nil {
		t.Fatalf("worker cannot reach its room: %v", err)
	}
	t.Setenv("PEER_REPO", "")
	if got, _ := invoke(repo, "", "log", v.ID); !strings.Contains(got, "hello") {
		t.Fatalf("writer's room lacks the worker's message: %s", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "api.go"), []byte("package api\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.exitPath(v.ID, "api"), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "invite", v.ID, "api", "--as", "writer", "--agent", "codex", "--worker"); err == nil {
		t.Fatal("worktree role invited again into the shared checkout")
	}
	if _, err := invoke(repo, "", "invite", v.ID, "api", "--as", "writer", "--agent", "codex", "--worker", "--worktree"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "api.go")); err != nil || dir != m.Worktree {
		t.Fatalf("invited again in %s, edits lost: %v", dir, err)
	}
	// A worktree switched to another branch is not reused, and stays.
	if out, err := exec.Command("git", "-C", dir, "switch", "-q", "-c", "other").CombinedOutput(); err != nil {
		t.Fatalf("git switch: %v: %s", err, out)
	}
	if err := os.WriteFile(s.exitPath(v.ID, "api"), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dir = ""
	if _, err := invoke(repo, "", "invite", v.ID, "api", "--as", "writer", "--agent", "codex", "--worker", "--worktree"); err == nil || dir != "" {
		t.Fatalf("reused a worktree on another branch: %v, launched in %q", err, dir)
	}
	if _, err := os.Stat(filepath.Join(m.Worktree, "api.go")); err != nil {
		t.Fatalf("refused reuse lost the edits: %v", err)
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Worktree); err != nil {
		t.Fatalf("end removed the worktree: %v", err)
	}
}

func TestParticipantNames(t *testing.T) {
	repo := testRepo(t)
	if _, err := invoke(repo, "", "start", "pair", "--agent", "../claude"); err == nil {
		t.Fatal("invalid agent accepted")
	}
	v := startRoom(t, repo, "pair")
	if _, err := invoke(repo, "", "wait", v.ID, "--as", "../reader"); err == nil {
		t.Fatal("unsafe participant name accepted")
	}
}

func TestRoomsAreIsolated(t *testing.T) {
	repo := testRepo(t)
	a := startRoom(t, repo, "task")
	b := startRoom(t, repo, "task")
	if a.ID != "task" || b.ID != "task-2" {
		t.Fatalf("same name gave rooms %q and %q", a.ID, b.ID)
	}
	if _, err := invoke(repo, "for a", "send", a.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", b.ID, "--as", "reader"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("room b got room a's message: %s, %v", got, err)
	}
	if got, err := invoke(repo, "", "wait", a.ID, "--as", "reader"); err != nil || !strings.Contains(got, `"text":"for a"`) {
		t.Fatalf("room a lost its message: %s, %v", got, err)
	}
	if _, err := invoke(repo, "", "end", a.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "still here", "send", b.ID, "--as", "writer"); err != nil {
		t.Fatalf("ending room a closed room b: %v", err)
	}
	status, err := invoke(repo, "", "status")
	if err != nil || strings.Count(status, "\n") != 1 || !strings.Contains(status, `"id":"task-2"`) {
		t.Fatalf("status should list only the active room: %q, %v", status, err)
	}
	long := strings.Repeat("a", 40)
	startRoom(t, repo, long)
	if v := startRoom(t, repo, long); v.ID != long+"-2" {
		t.Fatalf("long name collision: %q", v.ID)
	} else if _, err := invoke(repo, "hi", "send", v.ID, "--as", "writer"); err != nil {
		t.Fatalf("suffixed long room unusable: %v", err)
	}
	for _, name := range []string{"", "../x", "Task", "1task", long + "a", "a/b"} {
		if _, err := invoke(repo, "", "start", name); err == nil {
			t.Fatalf("invalid room name %q accepted", name)
		}
	}
	if _, err := invoke(repo, "", "wait", "../task", "--as", "reader"); err == nil {
		t.Fatal("unsafe room ID accepted")
	}
}

func TestCloseRoom(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "stuck")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.close(v.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.close(v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "wait", v.ID, "--as", "reader"); err == nil || !strings.Contains(err.Error(), "closed in peer") {
		t.Fatalf("reader kept waiting in a closed room: %v", err)
	}
	if got, _ := invoke(repo, "", "status", v.ID); !strings.Contains(got, `"ended_reason":"closed in peer"`) {
		t.Fatalf("status hides the reason: %q", got)
	}
}

func TestWaitWithoutTimeoutWaitsForMessageOrEnd(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "patient")
	if _, err := invoke(repo, "", "wait", v.ID, "--as", "reader", "--timeout", "-1s"); err == nil {
		t.Fatal("wait accepted a negative timeout")
	}
	done := make(chan error, 1)
	var text string
	go func() {
		var err error
		text, err = invoke(repo, "", "wait", v.ID, "--as", "reader", "--timeout", "0")
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err := invoke(repo, "ready", "send", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || !strings.Contains(text, `"text":"ready"`) {
		t.Fatalf("wait --timeout 0 = %q, %v; want the message", text, err)
	}
	go func() {
		_, err := invoke(repo, "", "wait", v.ID, "--as", "reader", "--timeout", "0")
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err := invoke(repo, "", "end", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("wait --timeout 0 in an ended room: %v", err)
	}
}

func TestFailedDeliveryKeepsMessage(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "retry")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.post(v.ID, "reader", "once"); err != nil {
		t.Fatal(err)
	}
	if err := s.wait(v.ID, "reader", time.Nanosecond, failingWriter{}); err == nil {
		t.Fatal("wait hid the write error")
	}
	var out bytes.Buffer
	if err := s.wait(v.ID, "reader", time.Nanosecond, &out); err != nil || !strings.Contains(out.String(), `"text":"once"`) {
		t.Fatalf("wait after a failed write = %q, %v", out.String(), err)
	}
	if m, err := s.nextMessage(v.ID, "reader"); err != nil || m != nil {
		t.Fatalf("delivered message came again: %+v, %v", m, err)
	}
}

// failingWriter fails every write, as a closed stdout does.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("stdout closed") }

func TestWaitReceivesLaterMessage(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "later")
	waitTimeout = 2 * time.Second
	t.Cleanup(func() { waitTimeout = time.Nanosecond })
	done := make(chan struct {
		text string
		err  error
	}, 1)
	go func() {
		text, err := invoke(repo, "", "wait", v.ID, "--as", "reader")
		done <- struct {
			text string
			err  error
		}{text, err}
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := invoke(repo, "ready for review", "send", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || !strings.Contains(result.text, `"text":"ready for review"`) {
		t.Fatalf("wait did not wake: %s, %v", result.text, result.err)
	}
}

func TestWaitMarksCursorBeforeFirstMessage(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "cursor")
	if _, err := invoke(repo, "", "wait", v.ID, "--as", "reader"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "sessions", v.ID, "cursor-reader")); err != nil {
		t.Fatalf("wait before the first message left no cursor: %v", err)
	}
}

func TestPickerLists(t *testing.T) {
	repo := testRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("git", "init", "-q", other).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	v := startRoom(t, repo, "done")
	if _, err := invoke(repo, "", "end", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	for range 11 {
		v := startRoom(t, other, "old")
		if _, err := invoke(other, "", "end", v.ID, "--as", "writer"); err != nil {
			t.Fatal(err)
		}
	}
	startRoom(t, other, "one")
	startRoom(t, other, "two")
	if _, err := invoke(repo, ""); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
		t.Fatalf("picker ran without a terminal: %v", err)
	}
	entries, err := listEntries(map[string]int{})
	if err != nil || len(entries) != 14 || entries[0].v.ID != "two" || entries[1].v.ID != "one" || entries[2].v.ID != "old-11" || entries[13].v.ID != "done" {
		t.Fatalf("want active rooms, then every ended one, newest first: %+v, %v", entries, err)
	}
}

func TestPickerCachesEndedCounts(t *testing.T) {
	repo := testRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("git", "init", "-q", other).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	v := startRoom(t, repo, "same")
	startRoom(t, other, "same")
	counts := map[string]int{}
	if _, err := listEntries(counts); err != nil || len(counts) != 0 {
		t.Fatalf("active counts were cached: %v, %v", counts, err)
	}
	if _, err := invoke(repo, "last words", "send", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	entries, err := listEntries(counts)
	if err != nil || len(entries) != 2 || entries[1].count != 2 || len(counts) != 1 {
		t.Fatalf("the ended room's count is not its last: %+v, %v", entries, err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "sessions", v.ID, "messages.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if entries, err = listEntries(counts); err != nil || entries[1].count != 2 || entries[0].count != 1 {
		t.Fatalf("an ended count was reread, or leaked to the other room named same: %+v, %v", entries, err)
	}
}

func TestPostFromUser(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "post")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ to, text string }{{everyone, "to all"}, {"reader", "to reader"}} {
		if err := s.post(v.ID, p.to, p.text); err != nil {
			t.Fatal(err)
		}
	}
	for as, want := range map[string][]string{writer: {"to all"}, "reader": {"to all", "to reader"}} {
		var got []string
		for {
			m, err := s.nextMessage(v.ID, as)
			if err != nil {
				t.Fatal(err)
			}
			if m == nil {
				break
			}
			if m.From != human {
				t.Fatalf("%s got a message from %q", as, m.From)
			}
			got = append(got, m.Text)
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("%s got %q, want %q", as, got, want)
		}
	}
	for _, p := range []struct{ to, text string }{{"codex", "hi"}, {everyone, "  "}, {everyone, strings.Repeat("a", 64*1024+1)}, {everyone, "\xff"}} {
		if err := s.post(v.ID, p.to, p.text); err == nil {
			t.Fatalf("post to %q with %d bytes succeeded", p.to, len(p.text))
		}
	}
	if err := s.end(v.ID, writer, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if err := s.post(v.ID, everyone, "late"); err == nil {
		t.Fatal("post to an ended room succeeded")
	}
}

func TestClaudeMemberIsSandboxedWithoutEditTools(t *testing.T) {
	argv, err := memberArgs(&store{dir: "/store/room"}, member{Agent: "claude"}, "/repo", "p")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{"--strict-mcp-config", "--setting-sources user"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv lacks %s: %s", want, joined)
		}
	}
	i := slices.Index(argv, "--settings")
	if i < 0 {
		t.Fatalf("argv lacks --settings: %s", joined)
	}
	var settings struct {
		Sandbox struct {
			Enabled, FailIfUnavailable, AutoAllowBashIfSandboxed bool
			AllowUnsandboxedCommands                             *bool
			Filesystem                                           struct{ AllowWrite []string }
			Network                                              struct {
				AllowedDomains    []string
				AllowLocalBinding bool
			}
		}
	}
	if err := json.Unmarshal([]byte(argv[i+1]), &settings); err != nil {
		t.Fatal(err)
	}
	sb := settings.Sandbox
	if !sb.Enabled || !sb.FailIfUnavailable || sb.AllowUnsandboxedCommands == nil || *sb.AllowUnsandboxedCommands || !sb.AutoAllowBashIfSandboxed || !slices.Equal(sb.Filesystem.AllowWrite, []string{"/store/room"}) ||
		!slices.Equal(sb.Network.AllowedDomains, []string{"*"}) || !sb.Network.AllowLocalBinding {
		t.Errorf("sandbox settings = %+v", sb)
	}
	allowed := argv[slices.Index(argv, "--allowedTools")+1 : slices.Index(argv, "--")]
	for _, tool := range argv {
		if tool == "Edit" || tool == "Write" || tool == "NotebookEdit" {
			t.Errorf("argv grants %s", tool)
		}
	}
	if !slices.Contains(allowed, "Bash") {
		t.Error("Bash is not allowed, so dontAsk denies the commands the sandbox does not auto-allow")
	}
}

func TestCodexMemberHasNetwork(t *testing.T) {
	argv, err := memberArgs(&store{dir: "/store/room"}, member{Agent: "codex"}, "/repo", "p")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(argv, "sandbox_workspace_write.network_access=true") {
		t.Fatalf("codex runs without the network: %q", argv)
	}
}

func TestPiMemberHasNoEditTools(t *testing.T) {
	argv, err := memberArgs(&store{dir: "/store/room"}, member{Agent: "pi"}, "/repo", "p")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{"-p --mode json", "--no-session", "--no-approve", "--tools read,grep,find,ls,bash", "-- p"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv lacks %s: %s", want, joined)
		}
	}
}

func TestUnknownAgentHasNoLaunch(t *testing.T) {
	if _, err := memberArgs(&store{}, member{Agent: "copilot"}, "/repo", "p"); err == nil {
		t.Fatal("memberArgs accepted copilot")
	}
}

func TestWorkerHasNoLimits(t *testing.T) {
	for agent, c := range map[string]struct{ want, banned []string }{
		"claude": {[]string{"--permission-mode bypassPermissions"}, []string{"dontAsk", "--settings", "--tools", "--allowedTools", "--strict-mcp-config", "--setting-sources"}},
		"codex":  {[]string{"--dangerously-bypass-approvals-and-sandbox"}, []string{"workspace-write", "--add-dir"}},
		"pi":     {[]string{"--approve"}, []string{"--no-approve", "--tools"}},
	} {
		argv, err := memberArgs(&store{dir: "/store/room"}, member{Agent: agent, Worker: true}, "/repo", "p")
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(argv, " ")
		for _, want := range c.want {
			if !strings.Contains(joined, want) {
				t.Errorf("%s worker argv lacks %s: %s", agent, want, joined)
			}
		}
		for _, banned := range c.banned {
			if slices.Contains(argv, banned) {
				t.Errorf("%s worker argv limits it with %s: %s", agent, banned, joined)
			}
		}
	}
}

func TestAppNamesModelEffortAndMode(t *testing.T) {
	for _, tc := range []struct {
		m    member
		want string
	}{
		{member{Role: writer, Agent: "claude"}, "claude"},
		{member{Role: "driver", Agent: "codex", Model: "gpt-6.1-sol", Effort: "medium"}, "codex/gpt-6.1-sol/medium, readonly"},
		{member{Role: "api", Agent: "claude", Model: "haiku", Worker: true}, "claude/haiku, worker"},
	} {
		if got := tc.m.app(); got != tc.want {
			t.Errorf("%+v: got %q, want %q", tc.m, got, tc.want)
		}
	}
}

// TestKick removes a member that joined itself: it can no longer act or be
// addressed, and its role is not reused.
func TestKick(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "kick")
	for _, args := range [][]string{
		{"kick", v.ID, "reader", "--as", "reader"},
		{"kick", v.ID, "writer", "--as", "writer"},
		{"kick", v.ID, "ghost", "--as", "writer"},
	} {
		if _, err := invoke(repo, "", args...); err == nil {
			t.Fatalf("%q accepted", args)
		}
	}
	for range 2 { // a second kick only retries the stop
		if got, err := invoke(repo, "", "kick", v.ID, "reader", "--as", "writer"); err != nil || !strings.Contains(got, `"role":"reader","kicked":true,"kicked_by":"writer"`) {
			t.Fatalf("kick: %s, %v", got, err)
		}
	}
	if got, err := invoke(repo, "", "wait", v.ID, "--as", "writer"); err != nil || !strings.Contains(got, `"text":"reader was kicked by writer"`) {
		t.Fatalf("writer missed the kick: %s, %v", got, err)
	}
	if got, _ := invoke(repo, "", "wait", v.ID, "--as", "writer"); !strings.Contains(got, "timeout") {
		t.Fatalf("second kick notified again: %s", got)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"reader wait":    errOf(invoke(repo, "", "wait", v.ID, "--as", "reader")),
		"reader send":    errOf(invoke(repo, "hi", "send", v.ID, "--as", "reader")),
		"send to reader": errOf(invoke(repo, "hi", "send", v.ID, "--as", "writer", "--to", "reader")),
		"post to reader": s.post(v.ID, "reader", "hi"),
		"join reader":    errOf(invoke(repo, "", "join", v.ID, "reader")),
		"invite reader":  errOf(invoke(repo, "", "invite", v.ID, "reader", "--as", "writer", "--agent", "codex")),
	} {
		if err == nil || !strings.Contains(err.Error(), "kicked") {
			t.Fatalf("%s: %v, want a kicked error", name, err)
		}
	}
	if _, err := invoke(repo, "", "join", v.ID, "reader-2"); err != nil {
		t.Fatal(err)
	}
}

func errOf(_ string, err error) error { return err }

// groupGone reports whether the process group that pid led has ended.
func groupGone(pid int) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
			return true
		}
	}
	return false
}

// launchIgnoringTerm starts a member wrapper whose agent ignores TERM, so
// only KILL stops it, and reaps the wrapper, which the test process owns.
func launchIgnoringTerm(t *testing.T, s *store, id, role string) int {
	t.Helper()
	pid, err := realStartMember([]string{"sh", "-c", `trap "" TERM; sleep 60 & wait`}, s.repo, nil, s.logPath(id, role), s.exitPath(id, role))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = syscall.Wait4(pid, nil, 0, nil) }()
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	return pid
}

func TestKickStopsLaunchedMember(t *testing.T) {
	stopGrace = 300 * time.Millisecond
	t.Cleanup(func() { stopGrace = 3 * time.Second })
	repo := testRepo(t)
	v := startRoom(t, repo, "stop")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	pid := launchIgnoringTerm(t, s, v.ID, "reader")
	if err := s.launched(v.ID, "reader", pid); err != nil {
		t.Fatal(err)
	}
	note, err := s.kick(v.ID, "reader", human)
	if err != nil || !strings.Contains(note, "process stopped") {
		t.Fatalf("kick: %q, %v", note, err)
	}
	if !groupGone(pid) {
		t.Fatal("the member's process group outlived the kick")
	}
	if got, _ := invoke(repo, "", "wait", v.ID, "--as", "writer"); !strings.Contains(got, "reader was kicked by user") {
		t.Fatalf("writer missed the kick: %s", got)
	}
	if got, _ := invoke(repo, "", "wait", v.ID, "--as", "writer"); !strings.Contains(got, "timeout") {
		t.Fatalf("a kicked member was reported again: %s", got)
	}
}

// TestKickWhileStarting kicks a member between its invite and its launch;
// the invite then stops the process it started.
func TestKickWhileStarting(t *testing.T) {
	stopGrace = 300 * time.Millisecond
	repo := testRepo(t)
	v := startRoom(t, repo, "race")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	pid := 0
	startMember = func([]string, string, []string, string, string) (int, error) {
		pid = launchIgnoringTerm(t, s, v.ID, "docs")
		if _, err := s.kick(v.ID, "docs", writer); err != nil {
			t.Fatal(err)
		}
		return pid, nil
	}
	t.Cleanup(func() {
		startMember = func([]string, string, []string, string, string) (int, error) { return 0, nil }
		stopGrace = 3 * time.Second
	})
	if _, err := invoke(repo, "", "invite", v.ID, "docs", "--as", "writer", "--agent", "codex"); err == nil || !strings.Contains(err.Error(), "kicked while it started") {
		t.Fatalf("invite: %v", err)
	}
	if !groupGone(pid) {
		t.Fatal("a member kicked while starting kept running")
	}
	// A later kick retries the stop on the same process.
	if got, _ := s.refresh(v.ID); got.member("docs").PID != pid {
		t.Fatalf("the kicked member lost its PID: %+v", got.member("docs"))
	}
}

// TestStopMemberSparesOtherProcesses never signals a group whose leader
// does not run the member's wrapper, as when its PID was reused.
func TestStopMemberSparesOtherProcesses(t *testing.T) {
	other := exec.Command("sleep", "30")
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // leads a group, like a member
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })
	if err := stopMember(other.Process.Pid, "/no/such/member.exit"); err == nil {
		t.Fatal("stopMember accepted a process it did not start")
	}
	if err := other.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("stopMember signalled another process: %v", err)
	}
	t.Setenv("PATH", "") // without ps the group cannot be confirmed
	if err := stopMember(other.Process.Pid, "/no/such/member.exit"); err == nil || !strings.Contains(err.Error(), "ps") {
		t.Fatalf("stopMember without ps: %v", err)
	}
}

// TestKickRetriesFailedStop keeps the member kicked when its process does
// not stop, and a second kick retries on the same process.
func TestKickRetriesFailedStop(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "retry")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.launched(v.ID, "reader", 4242); err != nil {
		t.Fatal(err)
	}
	var stopped []int
	stopMember = func(pid int, _ string) error {
		stopped = append(stopped, pid)
		if len(stopped) == 1 {
			return errors.New("not permitted")
		}
		return nil
	}
	t.Cleanup(func() { stopMember = realStopMember })
	if _, err := s.kick(v.ID, "reader", writer); err == nil || !strings.Contains(err.Error(), "not confirmed stopped") {
		t.Fatalf("failed stop reported %v", err)
	}
	if got, _ := s.refresh(v.ID); !got.member("reader").Kicked {
		t.Fatal("a failed stop undid the kick")
	}
	if note, err := s.kick(v.ID, "reader", writer); err != nil || !strings.Contains(note, "process stopped") || !slices.Equal(stopped, []int{4242, 4242}) {
		t.Fatalf("retry: %q, %v, stops %v", note, err, stopped)
	}
}
