package main

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// memberPrompt starts an invited member's chat. Nobody reads it, so the
// member is told to keep waiting on its own.
func memberPrompt(v session, m member, brief string) string {
	prompt := fmt.Sprintf("Use the peer skill. You are the %s in peer room %s in this checkout; you have already joined, so do not run peer join.", m.Role, v.ID)
	switch {
	case m.Worktree != "":
		prompt += fmt.Sprintf(" You are a worker in your own worktree %s, your working directory, which started from commit %s. Follow peer skills worker.", m.Worktree, m.Base)
	case m.Worker:
		prompt += " You are a worker in the shared checkout. Follow peer skills worker."
	default:
		prompt += " Follow peer skills member."
	}
	if brief != "" {
		prompt += " Your focus: " + brief
	}
	return prompt + " Nobody reads this chat: do not ask the user anything, run peer and git directly rather than through wrapper commands, and keep calling peer wait until main ends the session. Once peer reports that the session has ended, stop."
}

// inviteAgents are the agents peer invite launches.
var inviteAgents = []string{"claude", "codex", "pi"}

// claudeTools are a Claude member's tools: everything but the edit tools.
// Its subagents get no more. A worker gets all tools.
var claudeTools = []string{"Bash", "Read", "Grep", "Glob", "Skill", "WebFetch", "WebSearch", "Task", "TaskCreate", "TaskGet", "TaskList", "TaskUpdate", "TaskStop", "LSP", "ToolSearch"}

// piTools are a pi member's tools: its built-in tools but edit and write.
// A worker gets pi's default tools and its extensions' tools.
var piTools = "read,grep,find,ls,bash"

// withDefaults gives a member invited without a model the latest model of
// its CLI at medium effort, since the user's settings may pin an older one:
// for Claude the latest Opus, which the alias names, and for Codex the
// latest Sol model it lists. Pi keeps its own default.
func withDefaults(m member) member {
	if m.Model != "" {
		return m
	}
	switch m.Agent {
	case "claude":
		m.Model, m.Effort = "opus", "medium"
	case "codex":
		if m.Model = latestCodexModel("-sol"); m.Model != "" {
			m.Effort = "medium"
		}
	}
	return m
}

// memberArgs runs m's CLI without a chat window in dir, the checkout or
// m's worktree. A worker runs without limits: no sandbox, no approvals,
// all tools and the checkout's settings. Codex and Claude run a member's
// shell commands, with the network, in a sandbox that by default writes
// only dir, temp directories and the peer store. Claude allows Bash
// outright, because dontAsk denies the commands that its sandbox does not
// auto-allow, such as heredocs; the sandbox still confines them. That
// sandbox covers only Bash, so a Claude member also gets no MCP servers
// and skips the checkout's settings, which could widen the sandbox. Pi has
// no sandbox: a member skips the checkout's settings, but its shell keeps
// the user's permissions.
func memberArgs(s *store, m member, dir, prompt string) ([]string, error) {
	var argv []string
	switch m.Agent {
	case "codex":
		argv = []string{"codex", "exec", "--json", "-C", dir}
		if m.Worker {
			argv = append(argv, "--dangerously-bypass-approvals-and-sandbox")
		} else {
			argv = append(argv, "-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=true", "--add-dir", s.dir, "-c", "approval_policy=never")
		}
		if m.Model != "" {
			argv = append(argv, "-m", m.Model)
		}
		if m.Effort != "" {
			argv = append(argv, "-c", "model_reasoning_effort="+m.Effort)
		}
		return append(argv, prompt), nil
	case "claude":
		argv = []string{"claude", "-p", "--verbose", "--output-format", "stream-json", "--permission-prompts", "none"}
		if m.Worker {
			argv = append(argv, "--permission-mode", "bypassPermissions")
		} else {
			sandbox := map[string]any{"sandbox": map[string]any{
				"enabled":                  true,
				"failIfUnavailable":        true,
				"allowUnsandboxedCommands": false,
				"autoAllowBashIfSandboxed": true,
				"filesystem":               map[string]any{"allowWrite": []string{s.dir}},
				"network":                  map[string]any{"allowedDomains": []string{"*"}, "allowLocalBinding": true},
			}}
			settings, _ := json.Marshal(sandbox)
			argv = append(argv, "--permission-mode", "dontAsk", "--strict-mcp-config", "--setting-sources", "user", "--settings", string(settings))
		}
		if m.Model != "" {
			argv = append(argv, "--model", m.Model)
		}
		if m.Effort != "" {
			argv = append(argv, "--effort", m.Effort)
		}
		if !m.Worker {
			argv = append(argv, "--tools")
			argv = append(argv, claudeTools...)
			argv = append(argv, "--allowedTools")
			argv = append(argv, claudeTools...)
		}
		return append(argv, "--", prompt), nil
	case "pi":
		argv = []string{"pi", "-p", "--mode", "json", "--no-session"}
		if m.Worker {
			argv = append(argv, "--approve")
		} else {
			argv = append(argv, "--no-approve", "--tools", piTools)
		}
		if m.Model != "" {
			argv = append(argv, "--model", m.Model)
		}
		return append(argv, "--", prompt), nil
	}
	return nil, fmt.Errorf("peer cannot launch %s", m.Agent)
}

// latestCodexModel is the listed Codex model whose name ends in suffix and
// that Codex ranks first, read from the model list Codex caches. It is ""
// when Codex has no such list, and Codex then uses its own default.
func latestCodexModel(suffix string) string {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(user, ".codex")
	}
	data, err := os.ReadFile(filepath.Join(home, "models_cache.json"))
	if err != nil {
		return ""
	}
	var cache struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
			Priority   int    `json:"priority"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &cache) != nil {
		return ""
	}
	best, rank := "", 0
	for _, m := range cache.Models {
		if m.Visibility == "list" && strings.HasSuffix(m.Slug, suffix) && (best == "" || m.Priority < rank) {
			best, rank = m.Slug, m.Priority
		}
	}
	return best
}

// startMember runs argv in dir with env added to its environment, in its
// own process session, so it outlives main's command, and appends
// its output and exit status to logPath. The status also goes to
// exitPath, which load turns into the member leaving. It returns the
// wrapper's PID, which leads the member's process group; it is replaced in
// tests.
var startMember = func(argv []string, dir string, env []string, logPath, exitPath string) (int, error) {
	if _, err := exec.LookPath(argv[0]); err != nil {
		return 0, err
	}
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return 0, err
	}
	defer log.Close()
	// The status is taken inside the group: after the pipe, $? is stamp's.
	script := `exit_file=$1; self=$2; shift 2; { "$@"; status=$?; echo "[peer] exited with status $status"; echo "$status" > "$exit_file"; } 2>&1 | "$self" stamp`
	cmd := exec.Command("sh", append([]string{"-c", script, "sh", exitPath, self}, argv...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	return pid, cmd.Process.Release()
}

// stopGrace is how long stopMember waits for a group to go after each
// signal; it is replaced in tests.
var stopGrace = 3 * time.Second

// stopMember stops the process group that pid leads: the wrapper
// startMember ran for the member whose status goes to exitPath. Once the
// member exits, its number may go to another process, so it signals only a
// group whose leader ps shows running that wrapper, and a group whose
// leader is gone is reported, not signalled. Then it sends TERM, and KILL
// if the group outlives stopGrace. TERM may end the leader but not an agent
// that ignores it; KILL then relies on the group having existed since the
// check, polled every 50ms, as a group's ID is not reused while the group
// exists. If the leader is still there, it is checked again. The checks and
// the signals are not atomic, so this is a safeguard, not a guarantee. It
// is replaced in tests.
var stopMember = func(pid int, exitPath string) error {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		if gone(pid) {
			return nil
		}
		if err := ownGroup(pid, exitPath, sig == syscall.SIGKILL); err != nil {
			return err
		}
		if err := syscall.Kill(-pid, sig); err != nil && !gone(pid) {
			return err
		}
		for deadline := time.Now().Add(stopGrace); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if gone(pid) {
				return nil
			}
		}
	}
	return fmt.Errorf("process group %d outlived SIGKILL", pid)
}

// gone reports whether process group pid has no processes left.
func gone(pid int) bool {
	return errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH)
}

// ownGroup checks with ps that pid leads a group running the wrapper that
// writes exitPath, or, if leaderless, that the leader has exited.
func ownGroup(pid int, exitPath string, leaderless bool) error {
	out, err := exec.Command("ps", "-ww", "-o", "pgid=,command=", "-p", strconv.Itoa(pid)).Output()
	fields := strings.Fields(string(out))
	var exit *exec.ExitError
	switch {
	case len(fields) == 0 && err != nil && !errors.As(err, &exit):
		return fmt.Errorf("ps: %w", err)
	case len(fields) == 0 && leaderless:
		return nil
	case len(fields) == 0:
		return fmt.Errorf("process %d has exited, but its group still runs, so peer did not signal it", pid)
	case fields[0] != strconv.Itoa(pid) || !strings.Contains(string(out), exitPath):
		return fmt.Errorf("process %d now runs something else, so peer did not signal it", pid)
	}
	return nil
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

// worktree prepares m's linked worktree: it reuses the one left by an
// earlier member with the role, if it is still a worktree of this
// checkout on m's branch, or adds it on m's branch at m's base.
func (s *store) worktree(m member) error {
	if _, err := os.Stat(m.Worktree); err == nil {
		git := func(args ...string) string {
			out, _ := exec.Command("git", append([]string{"-C", m.Worktree}, args...)...).Output()
			return strings.TrimSpace(string(out))
		}
		top, _ := filepath.EvalSymlinks(m.Worktree)
		common, _ := exec.Command("git", "-C", s.repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
		if git("rev-parse", "--show-toplevel") != top || git("rev-parse", "--path-format=absolute", "--git-common-dir") != strings.TrimSpace(string(common)) || git("branch", "--show-current") != m.Branch {
			return fmt.Errorf("%s is no longer a worktree of this checkout on %s; fix or remove it, or use another role", m.Worktree, m.Branch)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.Worktree), 0700); err != nil {
		return err
	}
	if out, err := exec.Command("git", "-C", s.repo, "worktree", "add", "-q", "-b", m.Branch, m.Worktree, m.Base).CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %s", bytes.TrimSpace(out))
	}
	return nil
}

// invite adds m to room id for its main and launches m's agent
// headless, in its own worktree when isolated.
func (s *store) invite(id, as string, m member, brief string, isolated bool, out io.Writer) error {
	if as != mainRole {
		return errors.New("only main can invite; run peer invite ID ROLE --as main")
	}
	m = withDefaults(m)
	var v session
	err := s.locked(func() error {
		cur, err := s.load(id)
		if err != nil {
			return err
		}
		// A role keeps its workspace, so a worker invited again finds
		// the edits its predecessor left.
		if old := cur.member(m.Role); old != nil && old.Exited && !old.Kicked {
			if (old.Worktree != "") != isolated {
				return fmt.Errorf("%s worked in %s; invite it again with the same workspace, or use another role", m.Role, cmp.Or(old.Worktree, "the shared checkout"))
			}
			m.Worktree, m.Branch, m.Base = old.Worktree, old.Branch, old.Base
		}
		if isolated && m.Worktree == "" {
			head, err := exec.Command("git", "-C", s.repo, "rev-parse", "--verify", "HEAD^{commit}").Output()
			if err != nil {
				return errors.New("a worktree starts from a commit, and the checkout has none")
			}
			key := sha256.Sum256([]byte(s.repo))
			m.Worktree = filepath.Join(s.dir, "worktrees", id, m.Role)
			m.Branch = "peer/" + hex.EncodeToString(key[:4]) + "/" + id + "/" + m.Role
			m.Base = strings.TrimSpace(string(head))
			if dirty, _ := exec.Command("git", "-C", s.repo, "status", "--porcelain").Output(); len(dirty) > 0 {
				fmt.Fprintf(os.Stderr, "peer: uncommitted changes in the checkout stay out of %s's worktree\n", m.Role)
			}
		}
		v, err = s.add(id, m)
		return err
	})
	if err != nil {
		return err
	}
	logPath := s.logPath(v.ID, m.Role)
	dir := s.repo
	if m.Worktree != "" {
		dir = m.Worktree
		err = s.worktree(m)
	}
	var argv []string
	if err == nil {
		argv, err = memberArgs(s, m, dir, memberPrompt(v, m, brief))
	}
	pid := 0
	if err == nil {
		pid, err = startMember(argv, dir, []string{"PEER_REPO=" + s.repo}, logPath, s.exitPath(v.ID, m.Role))
	}
	if err != nil {
		// Marking the member exited frees the role to be invited again.
		_ = s.locked(func() error {
			cur, err := s.load(v.ID)
			if err != nil || cur.EndedAt != "" || cur.member(m.Role) == nil || cur.member(m.Role).Kicked {
				return err
			}
			cur.member(m.Role).Exited = true
			if err := writeJSON(s.sessionPath(v.ID), cur); err != nil {
				return err
			}
			_, err = s.appendMessage(cur, system, mainRole, m.Role+" failed to start")
			return err
		})
		return fmt.Errorf("launching %s failed: %w", m.Agent, err)
	}
	if err := s.launched(v.ID, m.Role, pid); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "peer: %s runs headless; watch it in peer, or read %s\n", m.Role, logPath)
	if m.Worktree != "" {
		fmt.Fprintf(os.Stderr, "peer: %s works in %s on branch %s; peer end keeps it\n", m.Role, m.Worktree, m.Branch)
	}
	return json.NewEncoder(out).Encode(v)
}

// launched records the PID of the member started in role, so kick can
// stop it, or stops the member at once if it was kicked while starting.
func (s *store) launched(id, role string, pid int) error {
	kicked := false
	err := s.locked(func() error {
		v, err := s.load(id)
		if err != nil {
			return err
		}
		// A member kicked while starting keeps its PID too, so a later
		// kick can retry stopping it.
		m := v.member(role)
		m.PID, kicked = pid, m.Kicked
		return writeJSON(s.sessionPath(id), v)
	})
	if err != nil || !kicked {
		return err
	}
	if err := stopMember(pid, s.exitPath(id, role)); err != nil {
		return fmt.Errorf("%s was kicked while it started, and its process %d is not confirmed stopped: %w", role, pid, err)
	}
	return fmt.Errorf("%s was kicked while it started; its process is stopped", role)
}
