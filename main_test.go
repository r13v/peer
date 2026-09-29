package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	openURL = func(string) error { return nil }
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
	for role, want := range map[string]string{"flow": "peer skills writer", "reader": "peer wait --as YOUR_NAME", "writer": "peer wait --as YOUR_NAME"} {
		got, err := invoke(cwd, "", "skills", role)
		if err != nil || !strings.HasPrefix(got, "# ") || !strings.Contains(got, want) {
			t.Fatalf("%s instructions unavailable: %s, %v", role, got, err)
		}
	}
	if _, err := invoke(cwd, "", "skills", "unknown"); err == nil {
		t.Fatal("unknown role accepted")
	}
}

func TestInstallScriptVerifiesArchive(t *testing.T) {
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
	archive := "peer-darwin-arm64.tar.gz"
	if out, err := exec.Command("tar", "-czf", filepath.Join(assets, archive), "-C", stage, "peer").CombinedOutput(); err != nil {
		t.Fatalf("package: %v: %s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(assets, archive))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(assets, "checksums.txt"), []byte(fmt.Sprintf("%x  %s\n", sum, archive)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "uname"), []byte("#!/bin/sh\n[ \"$1\" = -s ] && echo Darwin || echo arm64\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "curl"), []byte("#!/bin/sh\ncp \"$PEER_TEST_ASSETS/${2##*/}\" \"$4\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	bin := filepath.Join(dir, "bin")
	env := append(os.Environ(), "HOME="+home, "PEER_INSTALL_DIR="+bin, "PEER_TEST_ASSETS="+assets, "PATH="+fakeBin+":"+os.Getenv("PATH"))
	runInstall := func() ([]byte, error) {
		cmd := exec.Command("sh", "scripts/install.sh")
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	if out, err := runInstall(); err != nil {
		t.Fatalf("install: %v: %s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(bin, "peer"))
	if err != nil || string(got) != "verified binary" {
		t.Fatalf("missing verified CLI: %s, %v", got, err)
	}
	for _, app := range []string{".claude", ".codex"} {
		path := filepath.Join(home, app, "skills/peer/SKILL.md")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("CLI installer created skill at %s: %v", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("managed by npx"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := runInstall(); err != nil {
		t.Fatalf("update: %v: %s", err, out)
	}
	for _, app := range []string{".claude", ".codex"} {
		path := filepath.Join(home, app, "skills/peer/SKILL.md")
		got, err := os.ReadFile(path)
		if err != nil || string(got) != "managed by npx" {
			t.Fatalf("CLI update changed skill at %s: %s, %v", path, got, err)
		}
	}
	if err := os.WriteFile(filepath.Join(assets, archive), []byte("corrupt"), 0600); err != nil {
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

func TestPairSession(t *testing.T) {
	repo := testRepo(t)
	started, err := invoke(repo, "", "start", "--writer", "claude", "--reader", "copilot")
	if err != nil {
		t.Fatal(err)
	}
	var first session
	if err := json.Unmarshal([]byte(started), &first); err != nil || first.Writer != "claude" || first.Reader != "copilot" {
		t.Fatalf("wrong writer assignment: %s, %v", started, err)
	}
	if _, err := invoke(repo, "", "start", "--writer", "copilot", "--reader", "codex"); err == nil {
		t.Fatal("a second writer could start in the same checkout")
	}
	if _, err := invoke(repo, "proposal", "send", "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "intrusion", "send", "--as", "codex"); err == nil {
		t.Fatal("nonparticipant sent a message")
	}
	if _, err := invoke(repo, "", "wait", "--as", "codex"); err == nil {
		t.Fatal("nonparticipant read a message")
	}
	if got, err := invoke(repo, "", "wait", "--as", "claude"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("writer consumed its own message: %s, %v", got, err)
	}
	got, err := invoke(repo, "", "wait", "--as", "copilot")
	if err != nil || !strings.Contains(got, `"text":"proposal"`) {
		t.Fatalf("reviewer missed proposal: %s, %v", got, err)
	}
	if got, err := invoke(repo, "", "wait", "--as", "copilot"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("message delivered twice: %s, %v", got, err)
	}
	if _, err := invoke(repo, "check line 12", "send", "--as", "copilot"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", "--as", "claude"); err != nil || !strings.Contains(got, `"text":"check line 12"`) {
		t.Fatalf("writer missed review: %s, %v", got, err)
	}
	if _, err := invoke(repo, "", "log"); err == nil {
		t.Fatal("log without an ID accepted")
	}
	log, err := invoke(repo, "", "log", first.ID)
	if err != nil || !strings.Contains(log, "proposal") || !strings.Contains(log, "check line 12") {
		t.Fatalf("transcript incomplete: %s, %v", log, err)
	}
	if _, err := invoke(repo, "", "end", "--as", "copilot"); err == nil {
		t.Fatal("reviewer ended writer's session")
	}
	if _, err := invoke(repo, "review complete", "send", "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", "--as", "copilot"); err != nil || !strings.Contains(got, `"text":"review complete"`) {
		t.Fatalf("closing message lost when session ended: %s, %v", got, err)
	}
	if _, err := invoke(repo, "too late", "send", "--as", "copilot"); err == nil {
		t.Fatal("send accepted after end")
	}
	if _, err := invoke(repo, "", "start", "--writer", "copilot", "--reader", "codex"); err != nil {
		t.Fatal(err)
	}
	history, err := invoke(repo, "", "history")
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(history), "\n")
	if len(rows) != 2 || !strings.HasSuffix(rows[0], "active") || !strings.HasPrefix(rows[1], first.ID+" ") || !strings.Contains(rows[1], "claude→copilot    3 msgs") || !strings.HasSuffix(rows[1], "ended") {
		t.Fatalf("unexpected history:\n%s", history)
	}
	oldLog, err := invoke(repo, "", "log", first.ID)
	if err != nil || !strings.Contains(oldLog, "proposal") {
		t.Fatalf("old transcript missing: %s, %v", oldLog, err)
	}
}

func TestOpenReader(t *testing.T) {
	var links []string
	openURL = func(link string) error { links = append(links, link); return nil }
	t.Cleanup(func() { openURL = func(string) error { return nil } })
	for _, tc := range []struct{ writer, reader, host, pathKey, promptKey string }{
		{"claude", "codex", "threads", "path", "prompt"},
		{"codex", "claude", "code", "folder", "q"},
		{"claude", "copilot", "", "", ""},
	} {
		links = nil
		repo := filepath.Join(t.TempDir(), "my repo & co")
		if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		t.Setenv("PEER_HOME", filepath.Join(t.TempDir(), "data"))
		started, err := invoke(repo, "", "start", "--writer", tc.writer, "--reader", tc.reader)
		var v session
		if err != nil || json.Unmarshal([]byte(started), &v) != nil {
			t.Fatalf("start output is not one session: %q, %v", started, err)
		}
		if tc.host == "" {
			if len(links) != 0 {
				t.Fatalf("opened a link for %s: %q", tc.reader, links)
			}
			continue
		}
		if len(links) != 1 {
			t.Fatalf("want one %s link, got %q", tc.reader, links)
		}
		u, err := url.Parse(links[0])
		if err != nil || u.Scheme != tc.reader || u.Host != tc.host || u.Path != "/new" {
			t.Fatalf("wrong %s link: %s, %v", tc.reader, links[0], err)
		}
		resolved, _ := filepath.EvalSymlinks(repo)
		prompt := u.Query().Get(tc.promptKey)
		if u.Query().Get(tc.pathKey) != resolved || !strings.Contains(prompt, "participant "+tc.reader) || strings.HasPrefix(prompt, "/") {
			t.Fatalf("wrong %s query: %v", tc.reader, u.Query())
		}
	}
}

func TestParticipantNames(t *testing.T) {
	repo := testRepo(t)
	for _, pair := range [][2]string{{"claude", "claude"}, {"../claude", "codex"}, {"claude", "copilot/other"}} {
		if _, err := invoke(repo, "", "start", "--writer", pair[0], "--reader", pair[1]); err == nil {
			t.Fatalf("invalid pair accepted: %q, %q", pair[0], pair[1])
		}
	}
	if _, err := invoke(repo, "", "start", "--writer", "claude", "--reader", "copilot"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "wait", "--as", "../copilot"); err == nil {
		t.Fatal("unsafe participant name accepted")
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

func TestWaitReceivesLaterMessage(t *testing.T) {
	repo := testRepo(t)
	if _, err := invoke(repo, "", "start", "--writer", "codex-main", "--reader", "codex-review"); err != nil {
		t.Fatal(err)
	}
	waitTimeout = 2 * time.Second
	t.Cleanup(func() { waitTimeout = 0 })
	done := make(chan struct {
		text string
		err  error
	}, 1)
	go func() {
		text, err := invoke(repo, "", "wait", "--as", "codex-review")
		done <- struct {
			text string
			err  error
		}{text, err}
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := invoke(repo, "ready for review", "send", "--as", "codex-main"); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || !strings.Contains(result.text, `"text":"ready for review"`) {
		t.Fatalf("wait did not wake: %s, %v", result.text, result.err)
	}
}

func TestFollowStopsWhenSessionEnds(t *testing.T) {
	repo := testRepo(t)
	if _, err := invoke(repo, "", "start", "--writer", "claude", "--reader", "codex"); err != nil {
		t.Fatal(err)
	}
	done := make(chan string)
	go func() {
		got, err := invoke(repo, "", "follow")
		if err != nil {
			t.Error(err)
		}
		done <- got
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err := invoke(repo, "see main.go:3", "send", "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if !strings.Contains(got, "claude → codex\nsee main.go:3\n") || !strings.Contains(got, "session ended") || !strings.Contains(got, "1 message (claude 1, codex 0) in ") || strings.Contains(got, "\x1b") {
			t.Fatalf("unexpected follow output: %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("follow kept running after the session ended")
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

func TestFollowWaitsForNextSession(t *testing.T) {
	repo := testRepo(t)
	started, err := invoke(repo, "", "start", "--writer", "claude", "--reader", "codex")
	if err != nil {
		t.Fatal(err)
	}
	var old session
	if err := json.Unmarshal([]byte(started), &old); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "old task", "send", "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "log", old.ID); err != nil || !strings.Contains(got, "old task") {
		t.Fatalf("log of an ended session: %q, %v", got, err)
	}
	done := make(chan string)
	go func() {
		got, err := invoke(repo, "", "follow")
		if err != nil {
			t.Error(err)
		}
		done <- got
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err := invoke(repo, "", "start", "--writer", "codex", "--reader", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "new task", "send", "--as", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", "--as", "codex"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if !strings.HasPrefix(got, "waiting for a session to start") || !strings.Contains(got, "new task") || strings.Contains(got, "old task") {
			t.Fatalf("unexpected follow output: %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("follow did not pick up the next session")
	}
}

func TestWaitMarksCursorBeforeFirstMessage(t *testing.T) {
	repo := testRepo(t)
	started, err := invoke(repo, "", "start", "--writer", "claude", "--reader", "codex")
	if err != nil {
		t.Fatal(err)
	}
	var v session
	if err := json.Unmarshal([]byte(started), &v); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "wait", "--as", "codex"); err != nil {
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

func TestNewPrinterIgnoresDevNull(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if p := newPrinter(f, t.TempDir(), true); p.color || p.live {
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

func TestSessionIDsInSameSecond(t *testing.T) {
	repo := testRepo(t)
	var ids []string
	for i := 0; i < 2; i++ {
		started, err := invoke(repo, "", "start", "--writer", "claude", "--reader", "codex")
		if err != nil {
			t.Fatal(err)
		}
		var v session
		if err := json.Unmarshal([]byte(started), &v); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID)
		if _, err := invoke(repo, "", "end", "--as", "claude"); err != nil {
			t.Fatal(err)
		}
	}
	if ids[0] == ids[1] || !sessionID.MatchString(ids[0]) || !sessionID.MatchString(ids[1]) || sessionID.MatchString("../x") {
		t.Fatalf("session IDs %q are not distinct and valid", ids)
	}
}

func TestPickerLists(t *testing.T) {
	repo := testRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("git", "init", "-q", other).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if _, err := invoke(repo, "", "start", "--writer", "claude", "--reader", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(other, "", "start", "--writer", "codex", "--reader", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, ""); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
		t.Fatalf("picker ran without a terminal: %v", err)
	}
	local, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := listEntries(local)
	if err != nil || len(entries) != 2 || entries[0].v.Writer != "codex" || entries[0].v.EndedAt != "" || entries[1].v.Writer != "claude" || entries[1].v.EndedAt == "" {
		t.Fatalf("want the active session in other, then the ended one here: %+v, %v", entries, err)
	}
	if entries, err := listEntries(nil); err != nil || len(entries) != 1 {
		t.Fatalf("outside a checkout, want only active sessions: %+v, %v", entries, err)
	}
}

func TestReadKeys(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	keys := readKeys(r)
	for _, tc := range []struct {
		in   string
		want key
	}{{"\x1b[A", keyUp}, {"\x1bOB", keyDown}, {"\x1b[", keyOther}, {"q", keyQuit}, {"\x1b", keyEsc}, {"\r", keyEnter}, {"j", keyDown}, {"\x03", keyQuit}} {
		w.WriteString(tc.in)
		if got := <-keys; got != tc.want {
			t.Fatalf("%q: got key %d, want %d", tc.in, got, tc.want)
		}
	}
	w.Close()
	if _, ok := <-keys; ok {
		t.Fatal("keys stayed open after input closed")
	}
}

func TestRenderKeepsSelectionVisible(t *testing.T) {
	repo := testRepo(t)
	for i := 0; i < 6; i++ {
		if _, err := invoke(repo, "", "start", "--writer", "claude", "--reader", "codex"); err != nil {
			t.Fatal(err)
		}
		if _, err := invoke(repo, "", "end", "--as", "claude"); err != nil {
			t.Fatal(err)
		}
	}
	local, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := listEntries(local)
	if err != nil || len(entries) != 6 {
		t.Fatalf("want 6 entries: %d, %v", len(entries), err)
	}
	var out bytes.Buffer
	render(&out, entries, 5, local, 30, 4)
	lines := strings.Split(strings.TrimPrefix(out.String(), clearScreen), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[3], "› ") {
		t.Fatalf("selected row not on a 4-line screen: %q", lines)
	}
	for _, line := range lines {
		if len([]rune(line)) >= 30 {
			t.Fatalf("line not cut to width: %q", line)
		}
	}
}
