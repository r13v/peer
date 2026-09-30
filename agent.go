package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// openURL is replaced in tests.
var openURL = func(link string) error { return exec.Command("open", link).Run() }

// memberPrompt starts an invited member's chat. A headless member has
// nobody to ask, so it is told to keep waiting on its own.
func memberPrompt(v session, role, brief string, headless bool) string {
	prompt := fmt.Sprintf("Use the peer skill. You are the %s in peer room %s in this checkout; you have already joined, so do not run peer join. Follow peer skills member.", role, v.ID)
	if brief != "" {
		prompt += " Your focus: " + brief
	}
	if headless {
		prompt += " Nobody reads this chat: do not ask the user anything, run peer and git directly rather than through wrapper commands, and keep calling peer wait until the writer ends the session. Once peer reports that the session has ended, stop."
	}
	return prompt
}

// appBundles maps each agent with a desktop app to its macOS bundle ID.
var appBundles = map[string]string{"codex": "com.openai.codex", "claude": "com.anthropic.claudefordesktop"}

// appRunning reports whether agent's desktop app is open without
// launching it; it is replaced in tests. The desktop apps and their deep
// links exist only on macOS, so other systems report false and run the
// member headless.
var appRunning = func(agent string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	out, err := exec.Command("osascript", "-e", `application id "`+appBundles[agent]+`" is running`).Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// memberArgs runs the agent's CLI without a chat window. Codex's sandbox
// also lets it write the peer store outside the checkout; Claude gets no
// edit tools and only peer and read-only git in Bash.
func memberArgs(s *store, agent, prompt string) ([]string, error) {
	switch agent {
	case "codex":
		return []string{"codex", "exec", "--json", "-C", s.repo, "-s", "workspace-write", "--add-dir", s.dir, "-c", "approval_policy=never", prompt}, nil
	case "claude":
		return []string{"claude", "-p", "--verbose", "--output-format", "stream-json", "--permission-mode", "dontAsk", "--permission-prompts", "none", "--tools", "Bash", "Read", "Grep", "Glob", "Skill", "--allowedTools", "Skill", "Bash(peer:*)", "Bash(git diff:*)", "Bash(git status:*)", "Bash(git log:*)", "Bash(git show:*)", "Read", "Grep", "Glob", "--", prompt}, nil
	}
	return nil, fmt.Errorf("peer cannot launch %s", agent)
}

// startMember runs argv in dir in its own process session, so it
// outlives the writer's command, and appends its output and exit status
// to logPath. The status also goes to exitPath, which load turns into the
// member leaving; it is replaced in tests.
var startMember = func(argv []string, dir, logPath, exitPath string) error {
	if _, err := exec.LookPath(argv[0]); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	// The status is taken inside the group: after the pipe, $? is stamp's.
	script := `exit_file=$1; self=$2; shift 2; { "$@"; status=$?; echo "[peer] exited with status $status"; echo "$status" > "$exit_file"; } 2>&1 | "$self" stamp`
	cmd := exec.Command("sh", append([]string{"-c", script, "sh", exitPath, self}, argv...)...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// stamp copies in to out line by line, each prefixed with the time it
// was read and a tab, so the log shows when a member wrote each line.
func stamp(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if line[len(line)-1] != '\n' {
				line = append(line, '\n')
			}
			if _, werr := fmt.Fprintf(out, "%s\t%s", time.Now().UTC().Format(time.RFC3339Nano), line); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// memberLink builds a desktop deep link that opens a new codex or claude
// chat in repo with prompt prefilled. Neither app submits the prompt by itself.
func memberLink(repo, agent, prompt string) (string, error) {
	switch agent {
	case "codex":
		return (&url.URL{Scheme: "codex", Host: "threads", Path: "/new", RawQuery: url.Values{"path": {repo}, "prompt": {prompt}}.Encode()}).String(), nil
	case "claude":
		return (&url.URL{Scheme: "claude", Host: "code", Path: "/new", RawQuery: url.Values{"folder": {repo}, "q": {prompt}}.Encode()}).String(), nil
	}
	return "", fmt.Errorf("%s has no desktop link", agent)
}

// invite adds m to room id for its writer and launches m's agent in a
// desktop chat when headed and the app is open, otherwise headless.
func (s *store) invite(id, as string, m member, brief string, headed bool, out io.Writer) error {
	if as != writer {
		return errors.New("only the writer can invite; run peer invite ID ROLE --as writer")
	}
	var v session
	err := s.locked(func() error {
		var err error
		v, err = s.add(id, m)
		return err
	})
	if err != nil {
		return err
	}
	// failed marks the member exited, so the role can be invited again.
	failed := func() {
		_ = s.locked(func() error {
			cur, err := s.load(v.ID)
			if err != nil || cur.EndedAt != "" || cur.member(m.Role) == nil {
				return err
			}
			cur.member(m.Role).Exited = true
			if err := writeJSON(s.sessionPath(v.ID), cur); err != nil {
				return err
			}
			_, err = s.appendMessage(cur, system, writer, m.Role+" failed to start")
			return err
		})
	}
	if headed && appRunning(m.Agent) {
		link, err := memberLink(s.repo, m.Agent, memberPrompt(v, m.Role, brief, false))
		if err == nil {
			err = openURL(link)
		}
		if err != nil {
			failed()
			return fmt.Errorf("opening %s failed: %w; invite %s again", m.Agent, err, m.Role)
		}
		return json.NewEncoder(out).Encode(v)
	}
	if headed {
		why := m.Agent + " is not open"
		if runtime.GOOS != "darwin" {
			why = "desktop chats open only on macOS"
		}
		fmt.Fprintf(os.Stderr, "peer: %s, so %s runs headless\n", why, m.Role)
	}
	logPath := s.logPath(v.ID, m.Role)
	argv, err := memberArgs(s, m.Agent, memberPrompt(v, m.Role, brief, true))
	if err == nil {
		err = startMember(argv, s.repo, logPath, s.exitPath(v.ID, m.Role))
	}
	if err != nil {
		failed()
		return fmt.Errorf("launching %s failed: %w", m.Agent, err)
	}
	fmt.Fprintf(os.Stderr, "peer: %s runs headless; watch it in peer, or read %s\n", m.Role, logPath)
	return json.NewEncoder(out).Encode(v)
}
