package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// realStartReader is the launcher TestMain replaces.
var realStartReader func([]string, string, string) error

func TestMain(m *testing.M) {
	openURL = func(string) error { return nil }
	appRunning = func(string) bool { return false }
	realStartReader = startReader
	startReader = func([]string, string, string) error { return nil }
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
	for role, want := range map[string]string{"flow": "peer skills writer", "reader": "peer wait ID --as YOUR_NAME", "writer": "peer wait ID --as YOUR_NAME"} {
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

func TestReaderLogLine(t *testing.T) {
	for line, want := range map[string]string{
		"codex plain progress\n":             "codex plain progress",
		`{"type":"system","subtype":"init"}`: "",
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Reviewing"},{"type":"tool_use","name":"Bash","input":{"command":"peer wait --as claude"}}]}}`: "Reviewing\n→ Bash peer wait --as claude",
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"main.go"}}]}}`:                                                `→ Read {"file_path":"main.go"}`,
		`{"type":"result","result":"done"}`: "result: done",
	} {
		if got := readerLogLine([]byte(line)); got != want {
			t.Fatalf("readerLogLine(%q) = %q, want %q", line, got, want)
		}
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

func startRoom(t *testing.T, repo, name, writer, reader string) session {
	t.Helper()
	started, err := invoke(repo, "", "start", name, "--writer", writer, "--reader", reader)
	var v session
	if err != nil || json.Unmarshal([]byte(started), &v) != nil {
		t.Fatalf("start %s: %q, %v", name, started, err)
	}
	return v
}

func TestPairSession(t *testing.T) {
	repo := testRepo(t)
	first := startRoom(t, repo, "csv-export", "claude", "copilot")
	if first.ID != "csv-export" || first.Writer != "claude" || first.Reader != "copilot" {
		t.Fatalf("wrong room: %+v", first)
	}
	id := first.ID
	if _, err := invoke(repo, "proposal", "send", id, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "proposal", "send", "--as", "claude"); err == nil {
		t.Fatal("send without a room ID accepted")
	}
	if _, err := invoke(repo, "intrusion", "send", id, "--as", "codex"); err == nil {
		t.Fatal("nonparticipant sent a message")
	}
	if _, err := invoke(repo, "", "wait", id, "--as", "codex"); err == nil {
		t.Fatal("nonparticipant read a message")
	}
	if got, err := invoke(repo, "", "wait", id, "--as", "claude"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("writer consumed its own message: %s, %v", got, err)
	}
	got, err := invoke(repo, "", "wait", id, "--as", "copilot")
	if err != nil || !strings.Contains(got, `"text":"proposal"`) {
		t.Fatalf("reviewer missed proposal: %s, %v", got, err)
	}
	if got, err := invoke(repo, "", "wait", id, "--as", "copilot"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("message delivered twice: %s, %v", got, err)
	}
	if _, err := invoke(repo, "check line 12", "send", id, "--as", "copilot"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", id, "--as", "claude"); err != nil || !strings.Contains(got, `"text":"check line 12"`) {
		t.Fatalf("writer missed review: %s, %v", got, err)
	}
	if _, err := invoke(repo, "", "log"); err == nil {
		t.Fatal("log without an ID accepted")
	}
	log, err := invoke(repo, "", "log", id)
	if err != nil || !strings.Contains(log, "proposal") || !strings.Contains(log, "check line 12") {
		t.Fatalf("transcript incomplete: %s, %v", log, err)
	}
	if _, err := invoke(repo, "", "end", id, "--as", "copilot"); err == nil {
		t.Fatal("reviewer ended writer's session")
	}
	if _, err := invoke(repo, "review complete", "send", id, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", id, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", id, "--as", "copilot"); err != nil || !strings.Contains(got, `"text":"review complete"`) {
		t.Fatalf("closing message lost when session ended: %s, %v", got, err)
	}
	if _, err := invoke(repo, "too late", "send", id, "--as", "copilot"); err == nil {
		t.Fatal("send accepted after end")
	}
	if again := startRoom(t, repo, "csv-export", "copilot", "codex"); again.ID != "csv-export-2" {
		t.Fatalf("ended room name reused: %s", again.ID)
	}
	history, err := invoke(repo, "", "history")
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(history), "\n")
	if len(rows) != 2 || !strings.HasPrefix(rows[0], "csv-export-2 ") || !strings.HasSuffix(rows[0], "active") || !strings.HasPrefix(rows[1], id+" ") || !strings.Contains(rows[1], "claude→copilot    3 msgs") || !strings.HasSuffix(rows[1], "ended") {
		t.Fatalf("unexpected history:\n%s", history)
	}
}

func TestRoomsAreIsolated(t *testing.T) {
	repo := testRepo(t)
	a := startRoom(t, repo, "task", "claude", "codex")
	b := startRoom(t, repo, "task", "claude", "codex")
	if a.ID != "task" || b.ID != "task-2" {
		t.Fatalf("same name gave rooms %q and %q", a.ID, b.ID)
	}
	if _, err := invoke(repo, "for a", "send", a.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", b.ID, "--as", "codex"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("room b got room a's message: %s, %v", got, err)
	}
	if got, err := invoke(repo, "", "wait", a.ID, "--as", "codex"); err != nil || !strings.Contains(got, `"text":"for a"`) {
		t.Fatalf("room a lost its message: %s, %v", got, err)
	}
	if _, err := invoke(repo, "", "end", a.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "still here", "send", b.ID, "--as", "claude"); err != nil {
		t.Fatalf("ending room a closed room b: %v", err)
	}
	status, err := invoke(repo, "", "status")
	if err != nil || strings.Count(status, "\n") != 1 || !strings.Contains(status, `"id":"task-2"`) {
		t.Fatalf("status should list only the active room: %q, %v", status, err)
	}
	long := strings.Repeat("a", 40)
	startRoom(t, repo, long, "claude", "codex")
	if v := startRoom(t, repo, long, "claude", "codex"); v.ID != long+"-2" {
		t.Fatalf("long name collision: %q", v.ID)
	} else if _, err := invoke(repo, "hi", "send", v.ID, "--as", "claude"); err != nil {
		t.Fatalf("suffixed long room unusable: %v", err)
	}
	for _, name := range []string{"", "../x", "Task", "1task", long + "a", "a/b"} {
		if _, err := invoke(repo, "", "start", name, "--writer", "claude", "--reader", "codex"); err == nil {
			t.Fatalf("invalid room name %q accepted", name)
		}
	}
	if _, err := invoke(repo, "", "wait", "../task", "--as", "codex"); err == nil {
		t.Fatal("unsafe room ID accepted")
	}
}

func TestReaderExitEndsRoom(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "crash", "claude", "codex")
	if _, err := invoke(repo, "task", "send", v.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(s.dir, "sessions", v.ID, "reader.log")
	if err := realStartReader([]string{"sh", "-c", "exit 3"}, repo, logPath); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if b, err := os.ReadFile(filepath.Join(s.dir, "sessions", v.ID, readerExit)); err == nil && string(b) == "3\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("wrapper wrote no exit status")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := invoke(repo, "more", "send", v.ID, "--as", "claude"); err == nil || !strings.Contains(err.Error(), "codex exited with status 3") {
		t.Fatalf("first send after the reader exited was accepted: %v", err)
	}
	if got, err := invoke(repo, "", "wait", v.ID, "--as", "codex"); err != nil || !strings.Contains(got, `"text":"task"`) {
		t.Fatalf("queued message lost when the reader exited: %s, %v", got, err)
	}
	if _, err := invoke(repo, "", "wait", v.ID, "--as", "claude"); err == nil || !strings.Contains(err.Error(), "codex exited with status 3") {
		t.Fatalf("writer kept waiting on an exited reader: %v", err)
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "claude"); err == nil {
		t.Fatal("end replaced the first ending")
	}
	if history, _ := invoke(repo, "", "history"); !strings.Contains(history, "ended: codex exited with status 3") {
		t.Fatalf("history hides the reason: %q", history)
	}
}

func TestCloseRoom(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "stuck", "claude", "codex")
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
	if _, err := invoke(repo, "", "wait", v.ID, "--as", "codex"); err == nil || !strings.Contains(err.Error(), "closed in peer") {
		t.Fatalf("reader kept waiting in a closed room: %v", err)
	}
	if got, _ := invoke(repo, "", "status", v.ID); !strings.Contains(got, `"ended_reason":"closed in peer"`) {
		t.Fatalf("status hides the reason: %q", got)
	}
}

func TestOpenReader(t *testing.T) {
	var links []string
	var launched [][]string
	var logs []string
	running := false
	openURL = func(link string) error { links = append(links, link); return nil }
	appRunning = func(string) bool { return running }
	startReader = func(argv []string, _, logPath string) error {
		launched, logs = append(launched, argv), append(logs, logPath)
		return nil
	}
	t.Cleanup(func() {
		openURL = func(string) error { return nil }
		appRunning = func(string) bool { return false }
		startReader = func([]string, string, string) error { return nil }
	})
	for _, tc := range []struct {
		writer, reader, host, pathKey, promptKey string
		headed, running                          bool
	}{
		{"claude", "codex", "threads", "path", "prompt", true, true},
		{"codex", "claude", "code", "folder", "q", true, true},
		{"claude", "codex", "", "", "", false, true},
		{"claude", "codex", "", "", "", true, false},
		{"codex", "claude", "", "", "", false, false},
		{"claude", "copilot", "", "", "", true, true},
	} {
		links, launched, logs, running = nil, nil, nil, tc.running
		repo := filepath.Join(t.TempDir(), "my repo & co")
		if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		t.Setenv("PEER_HOME", filepath.Join(t.TempDir(), "data"))
		args := []string{"start", "review", "--writer", tc.writer, "--reader", tc.reader}
		if tc.headed {
			args = append(args, "--headed")
		}
		started, err := invoke(repo, "", args...)
		var v session
		if err != nil || json.Unmarshal([]byte(started), &v) != nil {
			t.Fatalf("start output is not one session: %q, %v", started, err)
		}
		resolved, _ := filepath.EvalSymlinks(repo)
		if tc.reader == "copilot" {
			if len(links)+len(launched) != 0 {
				t.Fatalf("started copilot: %q %q", links, launched)
			}
			continue
		}
		if tc.host == "" {
			if len(links) != 0 || len(launched) != 1 || launched[0][0] != tc.reader || filepath.Base(logs[0]) != "reader.log" || filepath.Base(filepath.Dir(logs[0])) != v.ID {
				t.Fatalf("%+v: want one headless %s, got links %q, argv %q, logs %q", tc, tc.reader, links, launched, logs)
			}
			prompt := launched[0][len(launched[0])-1]
			if !strings.Contains(prompt, "participant "+tc.reader) || !strings.Contains(prompt, "room "+v.ID) || !strings.Contains(prompt, "Nobody reads this chat") {
				t.Fatalf("wrong headless prompt: %q", prompt)
			}
			if tc.reader == "codex" && !strings.Contains(strings.Join(launched[0], " "), "-C "+resolved) {
				t.Fatalf("codex runs outside the checkout: %q", launched[0])
			}
			continue
		}
		if len(links) != 1 || len(launched) != 0 {
			t.Fatalf("want one %s link, got %q, argv %q", tc.reader, links, launched)
		}
		u, err := url.Parse(links[0])
		if err != nil || u.Scheme != tc.reader || u.Host != tc.host || u.Path != "/new" {
			t.Fatalf("wrong %s link: %s, %v", tc.reader, links[0], err)
		}
		prompt := u.Query().Get(tc.promptKey)
		if u.Query().Get(tc.pathKey) != resolved || !strings.Contains(prompt, "participant "+tc.reader) || !strings.Contains(prompt, "room "+v.ID) || strings.HasPrefix(prompt, "/") {
			t.Fatalf("wrong %s query: %v", tc.reader, u.Query())
		}
	}
}

func TestParticipantNames(t *testing.T) {
	repo := testRepo(t)
	for _, pair := range [][2]string{{"claude", "claude"}, {"../claude", "codex"}, {"claude", "copilot/other"}} {
		if _, err := invoke(repo, "", "start", "pair", "--writer", pair[0], "--reader", pair[1]); err == nil {
			t.Fatalf("invalid pair accepted: %q, %q", pair[0], pair[1])
		}
	}
	v := startRoom(t, repo, "pair", "claude", "copilot")
	if _, err := invoke(repo, "", "wait", v.ID, "--as", "../copilot"); err == nil {
		t.Fatal("unsafe participant name accepted")
	}
}

func TestWaitReceivesLaterMessage(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "later", "codex-main", "codex-review")
	waitTimeout = 2 * time.Second
	t.Cleanup(func() { waitTimeout = 0 })
	done := make(chan struct {
		text string
		err  error
	}, 1)
	go func() {
		text, err := invoke(repo, "", "wait", v.ID, "--as", "codex-review")
		done <- struct {
			text string
			err  error
		}{text, err}
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := invoke(repo, "ready for review", "send", v.ID, "--as", "codex-main"); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || !strings.Contains(result.text, `"text":"ready for review"`) {
		t.Fatalf("wait did not wake: %s, %v", result.text, result.err)
	}
}

func TestWaitMarksCursorBeforeFirstMessage(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "cursor", "claude", "codex")
	if _, err := invoke(repo, "", "wait", v.ID, "--as", "codex"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "sessions", v.ID, "cursor-codex")); err != nil {
		t.Fatalf("wait before the first message left no cursor: %v", err)
	}
}

func TestPickerLists(t *testing.T) {
	repo := testRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("git", "init", "-q", other).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	v := startRoom(t, repo, "done", "claude", "codex")
	if _, err := invoke(repo, "", "end", v.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	for range 11 {
		v := startRoom(t, other, "old", "codex", "claude")
		if _, err := invoke(other, "", "end", v.ID, "--as", "codex"); err != nil {
			t.Fatal(err)
		}
	}
	startRoom(t, other, "one", "codex", "claude")
	startRoom(t, other, "two", "codex", "claude")
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
	v := startRoom(t, repo, "same", "claude", "codex")
	startRoom(t, other, "same", "claude", "codex")
	counts := map[string]int{}
	if _, err := listEntries(counts); err != nil || len(counts) != 0 {
		t.Fatalf("active counts were cached: %v, %v", counts, err)
	}
	if _, err := invoke(repo, "last words", "send", v.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	entries, err := listEntries(counts)
	if err != nil || len(entries) != 2 || entries[1].count != 1 || len(counts) != 1 {
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
	if entries, err = listEntries(counts); err != nil || entries[1].count != 1 || entries[0].count != 0 {
		t.Fatalf("an ended count was reread, or leaked to the other room named same: %+v, %v", entries, err)
	}
}
