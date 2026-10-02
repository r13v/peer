package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// realStartMember and realAppRunning are the hooks TestMain replaces.
var (
	realStartMember func([]string, string, string, string) error
	realAppRunning  func(string) bool
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "stamp" { // startMember runs the test binary as peer
		if err := stamp(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	openURL = func(string) error { return nil }
	realAppRunning = appRunning
	appRunning = func(string) bool { return false }
	realStartMember = startMember
	startMember = func([]string, string, string, string) error { return nil }
	waitTimeout = 0
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
	for role, want := range map[string]string{"flow": "peer skills writer", "member": "peer wait ID --as ROLE", "writer": "peer wait ID --as writer"} {
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

func TestAppRunningOffMacOS(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS asks the app itself")
	}
	if realAppRunning("codex") {
		t.Fatal("reported a desktop app off macOS")
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
		`{"type":"new_event","x":1}`:                                                                                                                                   {{Kind: logRaw, Text: `{"type":"new_event","x":1}`}},
	} {
		if got := memberLogLine([]byte(line)); !slices.Equal(got, want) {
			t.Fatalf("memberLogLine(%q) = %+v, want %+v", line, got, want)
		}
	}
}

func TestStartMemberStampsOutputAndKeepsStatus(t *testing.T) {
	dir := t.TempDir()
	logPath, exitPath := filepath.Join(dir, "member.log"), filepath.Join(dir, "member.exit")
	if err := realStartMember([]string{"sh", "-c", "echo out; echo err >&2; printf tail; exit 3"}, dir, logPath, exitPath); err != nil {
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
	if got, want := p.author(v, writer, true), "\x1b[1;36mwriter\x1b[0m \x1b[2mclaude\x1b[0m"; got != want {
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
	if err != nil || !strings.Contains(log, "proposal") || !strings.Contains(log, "check line 12") || !strings.Contains(log, "writer claude → all") || !strings.Contains(log, "peer → writer\n") {
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
	if got, _ := invoke(repo, "", "wait", v.ID, "--as", "writer"); !strings.Contains(got, `"text":"test-expert · copilot joined"`) {
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
		if err := realStartMember([]string{"sh", "-c", fmt.Sprintf("exit %d", status)}, repo, s.logPath(v.ID, role), s.exitPath(v.ID, role)); err != nil {
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
	var links []string
	var launched [][]string
	var logs []string
	running := false
	fail := false
	openURL = func(link string) error { links = append(links, link); return nil }
	appRunning = func(string) bool { return running }
	startMember = func(argv []string, _, logPath, _ string) error {
		if fail {
			return errors.New("not installed")
		}
		launched, logs = append(launched, argv), append(logs, logPath)
		return nil
	}
	t.Cleanup(func() {
		openURL = func(string) error { return nil }
		appRunning = func(string) bool { return false }
		startMember = func([]string, string, string, string) error { return nil }
	})
	for _, tc := range []struct {
		agent, host, pathKey, promptKey string
		headed, running                 bool
	}{
		{"codex", "threads", "path", "prompt", true, true},
		{"claude", "code", "folder", "q", true, true},
		{"codex", "", "", "", false, true},
		{"codex", "", "", "", true, false},
		{"claude", "", "", "", false, false},
	} {
		links, launched, logs, running = nil, nil, nil, tc.running
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
		if tc.headed {
			args = append(args, "--headed")
		}
		if out, err := invoke(repo, "", args...); err != nil || !strings.Contains(out, `"role":"test-expert","agent":"`+tc.agent+`"`) {
			t.Fatalf("invite: %q, %v", out, err)
		}
		resolved, _ := filepath.EvalSymlinks(repo)
		if tc.host == "" {
			if len(links) != 0 || len(launched) != 1 || launched[0][0] != tc.agent || filepath.Base(logs[0]) != "test-expert.log" || filepath.Base(filepath.Dir(logs[0])) != v.ID {
				t.Fatalf("%+v: want one headless %s, got links %q, argv %q, logs %q", tc, tc.agent, links, launched, logs)
			}
			prompt := launched[0][len(launched[0])-1]
			if !strings.Contains(prompt, "the test-expert in peer room "+v.ID) || !strings.Contains(prompt, "edge cases") || !strings.Contains(prompt, "Nobody reads this chat") {
				t.Fatalf("wrong headless prompt: %q", prompt)
			}
			if tc.agent == "codex" && !strings.Contains(strings.Join(launched[0], " "), "-C "+resolved) {
				t.Fatalf("codex runs outside the checkout: %q", launched[0])
			}
			continue
		}
		if len(links) != 1 || len(launched) != 0 {
			t.Fatalf("want one %s link, got %q, argv %q", tc.agent, links, launched)
		}
		u, err := url.Parse(links[0])
		if err != nil || u.Scheme != tc.agent || u.Host != tc.host || u.Path != "/new" {
			t.Fatalf("wrong %s link: %s, %v", tc.agent, links[0], err)
		}
		prompt := u.Query().Get(tc.promptKey)
		if u.Query().Get(tc.pathKey) != resolved || !strings.Contains(prompt, "the test-expert in peer room "+v.ID) || strings.HasPrefix(prompt, "/") || strings.Contains(prompt, "Nobody reads") {
			t.Fatalf("wrong %s query: %v", tc.agent, u.Query())
		}
	}
	repo := testRepo(t)
	v := startRoom(t, repo, "rules")
	for _, args := range [][]string{
		{"invite", v.ID, "docs", "--as", "reader", "--agent", "codex"},
		{"invite", v.ID, "docs", "--as", "writer", "--agent", "copilot"},
		{"invite", v.ID, "reader", "--as", "writer", "--agent", "codex"},
		{"invite", v.ID, "--as", "writer", "--agent", "codex"},
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
	running = true
	openURL = func(string) error { return errors.New("no app") }
	if _, err := invoke(repo, "", "invite", v.ID, "ux", "--as", "writer", "--agent", "claude", "--headed"); err == nil || !strings.Contains(err.Error(), "no app") {
		t.Fatalf("failed desktop launch reported %v", err)
	}
	openURL = func(string) error { return nil }
	if _, err := invoke(repo, "", "invite", v.ID, "ux", "--as", "writer", "--agent", "claude", "--headed"); err != nil {
		t.Fatalf("role not free after a failed desktop launch: %v", err)
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

func TestWaitReceivesLaterMessage(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "later")
	waitTimeout = 2 * time.Second
	t.Cleanup(func() { waitTimeout = 0 })
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
	argv, err := memberArgs(&store{dir: "/store/room"}, "claude", "p")
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
		}
	}
	if err := json.Unmarshal([]byte(argv[i+1]), &settings); err != nil {
		t.Fatal(err)
	}
	sb := settings.Sandbox
	if !sb.Enabled || !sb.FailIfUnavailable || sb.AllowUnsandboxedCommands == nil || *sb.AllowUnsandboxedCommands || !sb.AutoAllowBashIfSandboxed || !slices.Equal(sb.Filesystem.AllowWrite, []string{"/store/room"}) {
		t.Errorf("sandbox settings = %+v", sb)
	}
	allowed := argv[slices.Index(argv, "--allowedTools")+1 : slices.Index(argv, "--")]
	for _, tool := range argv {
		if tool == "Edit" || tool == "Write" || tool == "NotebookEdit" {
			t.Errorf("argv grants %s", tool)
		}
	}
	if slices.Contains(allowed, "Bash") {
		t.Error("Bash is allowed outright, not only in the sandbox")
	}
}

func TestUnknownAgentHasNoLaunch(t *testing.T) {
	if _, err := memberArgs(&store{}, "copilot", "p"); err == nil {
		t.Fatal("memberArgs accepted copilot")
	}
	if _, err := memberLink("/repo", "copilot", "p"); err == nil {
		t.Fatal("memberLink accepted copilot")
	}
}
