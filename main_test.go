package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
		if err != nil || !strings.HasPrefix(got, "# ") || !strings.Contains(got, "peer wait --as YOUR_AGENT") {
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
	started, err := invoke(repo, "", "start", "--as", "codex")
	if err != nil {
		t.Fatal(err)
	}
	var first session
	if err := json.Unmarshal([]byte(started), &first); err != nil || first.Writer != "codex" || first.Reviewer != "claude" {
		t.Fatalf("wrong writer assignment: %s, %v", started, err)
	}
	if _, err := invoke(repo, "", "start", "--as", "claude"); err == nil {
		t.Fatal("a second writer could start in the same checkout")
	}
	if _, err := invoke(repo, "proposal", "send", "--as", "codex"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", "--as", "codex", "--timeout", "0s"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("writer consumed its own message: %s, %v", got, err)
	}
	got, err := invoke(repo, "", "wait", "--as", "claude", "--timeout", "0s")
	if err != nil || !strings.Contains(got, `"text":"proposal"`) {
		t.Fatalf("reviewer missed proposal: %s, %v", got, err)
	}
	if got, err := invoke(repo, "", "wait", "--as", "claude", "--timeout", "0s"); err != nil || !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("message delivered twice: %s, %v", got, err)
	}
	if _, err := invoke(repo, "check line 12", "send", "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", "--as", "codex", "--timeout", "0s"); err != nil || !strings.Contains(got, `"text":"check line 12"`) {
		t.Fatalf("writer missed review: %s, %v", got, err)
	}
	log, err := invoke(repo, "", "log")
	if err != nil || !strings.Contains(log, "proposal") || !strings.Contains(log, "check line 12") {
		t.Fatalf("transcript incomplete: %s, %v", log, err)
	}
	if _, err := invoke(repo, "", "end", "--as", "claude"); err == nil {
		t.Fatal("reviewer ended writer's session")
	}
	if _, err := invoke(repo, "review complete", "send", "--as", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", "--as", "codex"); err != nil {
		t.Fatal(err)
	}
	if got, err := invoke(repo, "", "wait", "--as", "claude", "--timeout", "0s"); err != nil || !strings.Contains(got, `"text":"review complete"`) {
		t.Fatalf("closing message lost when session ended: %s, %v", got, err)
	}
	if _, err := invoke(repo, "too late", "send", "--as", "claude"); err == nil {
		t.Fatal("send accepted after end")
	}
	if _, err := invoke(repo, "", "start", "--as", "claude"); err != nil {
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

func TestWaitReceivesLaterMessage(t *testing.T) {
	repo := testRepo(t)
	if _, err := invoke(repo, "", "start", "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct {
		text string
		err  error
	}, 1)
	go func() {
		text, err := invoke(repo, "", "wait", "--as", "codex", "--timeout", "2s")
		done <- struct {
			text string
			err  error
		}{text, err}
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := invoke(repo, "ready for review", "send", "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || !strings.Contains(result.text, `"text":"ready for review"`) {
		t.Fatalf("wait did not wake: %s, %v", result.text, result.err)
	}
}
