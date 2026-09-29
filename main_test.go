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
	for _, role := range []string{"reader", "writer"} {
		got, err := invoke(cwd, "", "skills", role)
		if err != nil || !strings.HasPrefix(got, "# ") || !strings.Contains(got, "peer wait --as YOUR_NAME") {
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
	if _, err := invoke(repo, "", "wait", "--as", "codex", "--timeout", "0s"); err == nil {
		t.Fatal("nonparticipant read a message")
	}
	if got, err := invoke(repo, "", "wait", "--as", "claude", "--timeout", "0s"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("writer consumed its own message: %s, %v", got, err)
	}
	got, err := invoke(repo, "", "wait", "--as", "copilot", "--timeout", "0s")
	if err != nil || !strings.Contains(got, `"text":"proposal"`) {
		t.Fatalf("reviewer missed proposal: %s, %v", got, err)
	}
	if got, err := invoke(repo, "", "wait", "--as", "copilot", "--timeout", "0s"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("message delivered twice: %s, %v", got, err)
	}
	if _, err := invoke(repo, "check line 12", "send", "--as", "copilot"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", "--as", "claude", "--timeout", "0s"); err != nil || !strings.Contains(got, `"text":"check line 12"`) {
		t.Fatalf("writer missed review: %s, %v", got, err)
	}
	log, err := invoke(repo, "", "log")
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
	if got, err := invoke(repo, "", "wait", "--as", "copilot", "--timeout", "0s"); err != nil || !strings.Contains(got, `"text":"review complete"`) {
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
	var sessions []session
	if err := json.Unmarshal([]byte(history), &sessions); err != nil || len(sessions) != 2 {
		t.Fatalf("old session missing: %s, %v", history, err)
	}
	oldLog, err := invoke(repo, "", "log", "--session", first.ID)
	if err != nil || !strings.Contains(oldLog, "proposal") {
		t.Fatalf("old transcript missing: %s, %v", oldLog, err)
	}
}

func TestOpenReader(t *testing.T) {
	var links []string
	openURL = func(link string) error { links = append(links, link); return nil }
	t.Cleanup(func() { openURL = func(link string) error { return exec.Command("open", link).Run() } })
	for _, tc := range []struct{ app, host, pathKey, promptKey string }{
		{"codex", "threads", "path", "prompt"},
		{"claude", "code", "folder", "q"},
	} {
		links = nil
		repo := filepath.Join(t.TempDir(), "my repo & co")
		if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		t.Setenv("PEER_HOME", filepath.Join(t.TempDir(), "data"))
		if _, err := invoke(repo, "", "start", "--writer", "claude", "--reader", "codex", "--open-reader", "slack"); err == nil || len(links) != 0 {
			t.Fatal("unknown app accepted")
		}
		if _, err := invoke(repo, "", "status"); err == nil {
			t.Fatal("session started despite an unknown app")
		}
		started, err := invoke(repo, "", "start", "--writer", "claude", "--reader", "codex", "--open-reader", tc.app)
		var v session
		if err != nil || json.Unmarshal([]byte(started), &v) != nil {
			t.Fatalf("start output is not one session: %q, %v", started, err)
		}
		if len(links) != 1 {
			t.Fatalf("want one %s link, got %q", tc.app, links)
		}
		u, err := url.Parse(links[0])
		if err != nil || u.Scheme != tc.app || u.Host != tc.host || u.Path != "/new" {
			t.Fatalf("wrong %s link: %s, %v", tc.app, links[0], err)
		}
		resolved, _ := filepath.EvalSymlinks(repo)
		prompt := u.Query().Get(tc.promptKey)
		if u.Query().Get(tc.pathKey) != resolved || !strings.Contains(prompt, "participant codex") || strings.HasPrefix(prompt, "/") {
			t.Fatalf("wrong %s query: %v", tc.app, u.Query())
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
	if _, err := invoke(repo, "", "wait", "--as", "../copilot", "--timeout", "0s"); err == nil {
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
	done := make(chan struct {
		text string
		err  error
	}, 1)
	go func() {
		text, err := invoke(repo, "", "wait", "--as", "codex-review", "--timeout", "2s")
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
		got, err := invoke(repo, "", "log", "--follow")
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
		t.Fatal("log --follow kept running after the session ended")
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
	if got, err := invoke(repo, "", "log", "--follow", "--session", old.ID); err != nil || !strings.Contains(got, "old task") {
		t.Fatalf("pinned follow of an ended session: %q, %v", got, err)
	}
	done := make(chan string)
	go func() {
		got, err := invoke(repo, "", "log", "--follow")
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
		t.Fatal("log --follow did not pick up the next session")
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
	if _, err := invoke(repo, "", "wait", "--as", "codex", "--timeout", "0s"); err != nil {
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

func TestLegacyStoreMigrates(t *testing.T) {
	home := filepath.Join(t.TempDir(), "data")
	parent := t.TempDir()
	repo := filepath.Join(parent, "My App")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	t.Setenv("PEER_HOME", home)
	real, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte(real))
	legacy := filepath.Join(home, "repos", fmt.Sprintf("%x", key[:8]))
	old := session{ID: "0123456789abcdef", Repo: real, Writer: "claude", Reader: "codex", StartedAt: "2026-09-01T10:00:00Z", EndedAt: "2026-09-01T10:30:00Z"}
	if err := os.MkdirAll(filepath.Join(legacy, "sessions", old.ID), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(legacy, "sessions", old.ID, "session.json"), old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "sessions", old.ID, "messages.jsonl"), []byte(`{"id":"1","at":"2026-09-01T10:01:00Z","from":"claude","to":"codex","text":"legacy hello"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "active"), []byte(old.ID+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := invoke(repo, "", "log", "--session", old.ID)
	if err != nil || !strings.Contains(got, "legacy hello") {
		t.Fatalf("legacy session unreachable: %q, %v", got, err)
	}
	want := filepath.Join(home, "repos", fmt.Sprintf("my-app-%x", key[:8]))
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("store not renamed to %s: %v", want, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy store still present: %v", err)
	}
	started, err := invoke(repo, "", "start", "--writer", "claude", "--reader", "codex")
	if err != nil {
		t.Fatal(err)
	}
	var v session
	if err := json.Unmarshal([]byte(started), &v); err != nil || !sessionID.MatchString(v.ID) {
		t.Fatalf("new session ID %q is not time-based: %v", v.ID, err)
	}
	history, err := invoke(repo, "", "history")
	if err != nil || !strings.Contains(history, old.ID) || !strings.Contains(history, v.ID) {
		t.Fatalf("history lost a session: %s, %v", history, err)
	}
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "status"); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("conflicting legacy and new stores were not reported: %v", err)
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
	if ids[0] == ids[1] || !validID(ids[0]) || !validID(ids[1]) || validID("../x") {
		t.Fatalf("session IDs %q are not distinct and valid", ids)
	}
}
