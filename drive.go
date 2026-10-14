package main

import (
	"bufio"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// drivePoll is how often drive looks for the next batch while the member is
// idle; tests shorten it.
var drivePoll = 200 * time.Millisecond

// errRoomEnded stops drive before a turn: the room ended meanwhile.
var errRoomEnded = errors.New("room ended")

// turnLockPath is flocked by drive while the member's turn runs, so end can
// let the turn finish.
func (s *store) turnLockPath(id, role string) string {
	return filepath.Join(s.dir, "sessions", id, "turn-"+role+".lock")
}

// drive runs invited member role of room id: a turn for each batch of
// messages for it. The first batch starts the agent's session with the
// member's prompt, and each later one resumes the session. Between turns the member costs nothing. It holds the
// role's delivery for as long as it runs, so the role has one reader:
// wait refuses it. A batch is marked read once its turn succeeds; a turn
// that fails ends drive, and the member exits with the agent's error in its
// log. drive returns when the room ends or the member is kicked, without a
// turn for the room's last messages: nobody could read the member's answer.
func (s *store) drive(ctx context.Context, id, role, brief string, out io.Writer) error {
	var v session
	var m member
	var lock *os.File
	err := s.locked(func() error {
		var err error
		if v, err = s.load(id); err != nil {
			return err
		}
		if err := v.check(role); err != nil {
			return err
		}
		m = *v.member(role)
		f, isHeld, err := s.claimDelivery(v.ID, role, "")
		if isHeld {
			return fmt.Errorf("peer already delivers room %s for %s", v.ID, role)
		}
		lock = f
		return err
	})
	if err != nil {
		return err
	}
	defer lock.Close()
	dir := s.repo
	if m.Worktree != "" {
		dir = m.Worktree
	}
	// Claude and Copilot take the new session's ID; Codex names its thread
	// itself.
	session := ""
	if m.Agent == "claude" || m.Agent == "copilot" {
		session = newUUID()
	}
	isStarted := false
	for {
		b, from, next, err := s.readBatch(v.ID, role)
		if err != nil {
			return err
		}
		if b.Status == "ended" {
			return nil
		}
		if b.Status == "" {
			// An empty batch still moves the cursor past messages for others.
			if _, err := s.commitBatch(v.ID, role, from, next, false); err != nil {
				return err
			}
			if !sleepCtx(ctx, drivePoll) {
				return nil
			}
			continue
		}
		// The first batch starts the session together with the member's
		// prompt, so the member has no turn without messages to answer.
		text := forwardText(v.ID, role, "peer", b)
		argv, err := memberArgs(s, m, dir, "", session, true)
		if !isStarted {
			argv, err = memberArgs(s, m, dir, memberPrompt(v, m, brief, true)+"\n\n"+text, session, false)
			text = ""
		}
		if err != nil {
			return err
		}
		thread, err := s.turn(ctx, v.ID, role, argv, dir, text, out)
		if errors.Is(err, errRoomEnded) {
			return nil
		}
		if err != nil {
			return err
		}
		if !isStarted && m.Agent == "codex" {
			if thread == "" {
				return errors.New("codex did not report its thread, so peer cannot resume it")
			}
			session = thread
		}
		isStarted = true
		if _, err := s.commitBatch(v.ID, role, from, next, false); err != nil {
			return err
		}
	}
}

// turn runs one turn of role's agent: argv in dir, with prompt on stdin
// when it is not empty. The agent's output goes to out, the member's log.
// It returns the Codex thread the turn reported. A turn starts only while
// the room is active, and holds role's turn lock from then until it ends:
// end takes the store lock and waits while a turn lock is held, so a turn
// cannot start after end checked.
func (s *store) turn(ctx context.Context, id, role string, argv []string, dir, prompt string, out io.Writer) (string, error) {
	var lock *os.File
	err := s.locked(func() error {
		v, err := s.load(id)
		if err != nil {
			return err
		}
		if v.EndedAt != "" {
			return errRoomEnded
		}
		if err := v.check(role); err != nil {
			return err
		}
		f, err := os.OpenFile(s.turnLockPath(id, role), os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			return err
		}
		lock = f
		return nil
	})
	if err != nil {
		return "", err
	}
	defer lock.Close()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// A kick sends TERM to drive and the agent at once. The agent gets TERM
	// again and time to stop what it started outside the group: Copilot
	// runs each shell command in a session of its own, which only Copilot
	// stops. KILL follows before stopMember's.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = stopGrace / 2
	cmd.Dir = dir
	if prompt != "" {
		cmd.Stdin = strings.NewReader(prompt)
	}
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	thread, isCompleted := copyEvents(stdout, out)
	if err := cmd.Wait(); err != nil {
		return thread, fmt.Errorf("%s turn failed: %w", argv[0], err)
	}
	if !isCompleted {
		return thread, fmt.Errorf("%s turn ended without completing", argv[0])
	}

	return thread, nil
}

// copyEvents copies the agent's JSON events from in to out line by line.
// It returns the thread that codex exec --json names in thread.started, and
// whether the turn completed: Codex's turn.completed, or a result that is
// not an error: Claude's without is_error, or Copilot's, told by its
// sessionId, with exitCode 0. An
// exit status of 0 alone does not show it.
func copyEvents(in io.Reader, out io.Writer) (string, bool) {
	thread, isCompleted := "", false
	r := bufio.NewReader(in)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			_, _ = out.Write(line)
			var ev struct {
				Type      string `json:"type"`
				Thread    string `json:"thread_id"`
				IsError   bool   `json:"is_error"`
				SessionID string `json:"sessionId"` // only copilot's result
				ExitCode  *int   `json:"exitCode"`
			}
			if json.Unmarshal(line, &ev) == nil {
				switch ev.Type {
				case "thread.started":
					thread = cmp.Or(thread, ev.Thread)
				case "turn.completed":
					isCompleted = true
				case "turn.failed":
					isCompleted = false
				case "result":
					isCompleted = !ev.IsError
					if ev.SessionID != "" {
						isCompleted = ev.ExitCode != nil && *ev.ExitCode == 0
					}
				}
			}
		}
		if err != nil {
			return thread, isCompleted
		}
	}
}

// busyRoles lists the members of v whose turn runs now. The caller holds
// the store lock.
func (s *store) busyRoles(v session) []string {
	var busy []string
	for _, m := range v.Members {
		if m.Role == mainRole || m.Exited || m.Kicked {
			continue
		}
		f, err := os.OpenFile(s.turnLockPath(v.ID, m.Role), os.O_RDWR, 0)
		if err != nil {
			continue
		}
		if errors.Is(syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), syscall.EWOULDBLOCK) {
			busy = append(busy, m.Role)
		}
		f.Close() // closing drops the probe's own lock
	}
	return busy
}

// newUUID is a random version 4 UUID.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
