// Command peer lets coding agents work on one Git checkout through a
// shared message store, and shows their rooms in a terminal UI.
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
)

//go:embed instructions/flow.md
var flowInstructions []byte

//go:embed instructions/member.md
var memberInstructions []byte

//go:embed instructions/main.md
var mainInstructions []byte

//go:embed instructions/worker.md
var workerInstructions []byte

const usage = "usage: peer, peer --version, peer update, peer skills flow|main|member|worker, peer start NAME, peer join|invite|kick ID ROLE, peer send|wait|end ID --as ROLE, peer send ID --as ROLE --text TEXT, peer wait ID --as ROLE --timeout DURATION, peer status [ID], peer history, peer watch [ID], peer log ID [--json], or peer memberlog ID ROLE [--follow]"

// version is set at release build time.
var version = "dev"

// waitTimeout is wait's timeout without --timeout; tests replace it so
// that wait does not block.
var waitTimeout = forever

func main() {
	cwd, err := os.Getwd()
	if err == nil {
		err = run(os.Args[1:], os.Stdin, os.Stdout, cwd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "peer:", err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out io.Writer, cwd string) error {
	if len(args) == 0 {
		return picker(in, out, cwd)
	}
	if args[0] == "--version" || args[0] == "version" {
		if len(args) != 1 {
			return errors.New("usage: peer --version")
		}
		_, err := fmt.Fprintln(out, version)
		return err
	}
	if args[0] == "update" {
		if len(args) != 1 {
			return errors.New("usage: peer update")
		}
		return update(in, out)
	}
	if args[0] == "stamp" { // hidden: startMember pipes a member's output through it
		return stamp(in, out)
	}
	if args[0] == "skills" {
		docs := map[string][]byte{"flow": flowInstructions, "main": mainInstructions, "member": memberInstructions, "worker": workerInstructions}
		if len(args) != 2 || docs[args[1]] == nil {
			return errors.New("usage: peer skills flow|main|member|worker")
		}
		_, err := out.Write(docs[args[1]])
		return err
	}
	s, err := openStore(cwd)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonLog := fs.Bool("json", false, "print the transcript as NDJSON")
	follow := fs.Bool("follow", false, "follow the member log while the room is active")
	actor := fs.String("as", "", "sender's role")
	to := fs.String("to", everyone, "recipient role for send")
	agent := fs.String("agent", "", "app behind the participant")
	brief := fs.String("brief", "", "extra instructions for invite")
	worker := fs.Bool("worker", false, "let the invited member edit files")
	worktree := fs.Bool("worktree", false, "give the invited worker its own worktree")
	model := fs.String("model", "", "model for the invited member's CLI")
	var text *string // send reads stdin unless --text is given
	fs.Func("text", "message text for send instead of stdin", func(v string) error {
		text = &v
		return nil
	})
	timeout := waitTimeout
	fs.Func("timeout", "how long wait waits; 0 does not wait", func(v string) error {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return errors.New("give a duration with a unit, such as 30s, 5m or 100ms; 0 does not wait, and without --timeout wait waits until a message comes")
		}
		timeout = d
		return nil
	})
	// The room name or ID and, for join and invite, the role come first,
	// as in peer join ID ROLE; Go's flag parsing would stop at them, so
	// they are taken off before the flags.
	rest, id, role := args[1:], "", ""
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		id, rest = rest[0], rest[1:]
	}
	if (args[0] == "join" || args[0] == "invite" || args[0] == "kick" || args[0] == "memberlog") && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		role, rest = rest[0], rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	needID := func(cmd string) error {
		if id == "" {
			return fmt.Errorf("usage: peer %s ID; list active rooms with peer status", cmd)
		}
		return nil
	}
	needRole := func(cmd string) error {
		if id == "" || role == "" {
			return fmt.Errorf("usage: peer %s", cmd)
		}
		return checkRole(role)
	}
	switch args[0] {
	case "log":
		if err := needID("log"); err != nil {
			return err
		}
		if *jsonLog {
			return s.logJSON(id, out)
		}
		return s.log(id, newPrinter(out, s.repo))
	case "watch", "memberlog":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGPIPE)
		defer stop()
		if args[0] == "watch" {
			return streamError(s.watch(ctx, id, out))
		}
		if id == "" || role == "" {
			return errors.New("usage: peer memberlog ID ROLE [--follow]")
		}
		if err := checkName(role); err != nil {
			return err
		}
		return streamError(s.memberlog(ctx, id, role, *follow, out))
	case "start":
		if !roomName.MatchString(id) {
			return errors.New("usage: peer start NAME [--agent NAME]; the room NAME is 1-40 characters: a-z, 0-9 or -, starting with a letter")
		}
		if err := checkAgent(*agent); err != nil {
			return err
		}
		_, err := s.start(id, *agent, out)
		return err
	case "join":
		if err := needRole("join ID ROLE [--agent NAME]"); err != nil {
			return err
		}
		if err := checkAgent(*agent); err != nil {
			return err
		}
		return s.join(id, member{Role: role, Agent: *agent}, out)
	case "invite":
		if err := needRole("invite ID ROLE --as main --agent codex|claude|pi [--worker [--worktree]] [--model MODEL] [--brief TEXT]"); err != nil {
			return err
		}
		if !slices.Contains(inviteAgents, *agent) {
			return errors.New("invite launches codex, claude or pi; to add another agent, give it a join prompt")
		}
		if *worktree && !*worker {
			return errors.New("--worktree is for a worker; add --worker")
		}
		return s.invite(id, *actor, member{Role: role, Agent: *agent, Worker: *worker, Model: *model}, *brief, *worktree, out)
	case "kick":
		if err := needRole("kick ID ROLE --as main|user"); err != nil {
			return err
		}
		if *actor != mainRole && *actor != human {
			return errors.New("only main or user can kick; run peer kick ID ROLE --as main|user")
		}
		note, err := s.kick(id, role, *actor)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "peer:", note)
		return s.status(id, out)
	case "status":
		return s.status(id, out)
	case "send":
		if err := needID("send"); err != nil {
			return err
		}
		if err := checkName(*actor); err != nil {
			return err
		}
		if *to != everyone {
			if err := checkName(*to); err != nil {
				return err
			}
		}
		if text == nil {
			b, err := io.ReadAll(io.LimitReader(in, 64*1024+1))
			if err != nil {
				return err
			}
			body := string(b)
			text = &body
		}
		msg, err := messageText(*text)
		if err != nil {
			return err
		}
		return s.send(id, *actor, *to, msg, out)
	case "wait":
		if err := needID("wait"); err != nil {
			return err
		}
		if err := checkName(*actor); err != nil {
			return err
		}
		return s.wait(id, *actor, timeout, out)
	case "end":
		if err := needID("end"); err != nil {
			return err
		}
		if err := checkName(*actor); err != nil {
			return err
		}
		return s.end(id, *actor, out)
	case "history":
		return s.history(out)
	default:
		return errors.New(usage)
	}
}

// update reruns the latest release's installer over this binary.
func update(in io.Reader, out io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	target, err := installDir(executable)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "peer-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "install.sh")
	cmd := exec.Command("curl", "-fsSL", "https://github.com/r13v/peer/releases/latest/download/install.sh", "-o", script)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download latest installer: %w", err)
	}
	cmd = exec.Command("sh", script)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "PEER_INSTALL_DIR="+target)
	return cmd.Run()
}

// installDir is the directory update replaces executable in. Homebrew owns
// its copies, so replacing one would leave brew with a version it did not
// install.
func installDir(executable string) (string, error) {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	for _, part := range strings.Split(filepath.ToSlash(resolved), "/") {
		if part == "Caskroom" || part == "Cellar" {
			return "", errors.New("installed with Homebrew; update it with brew upgrade --cask peer")
		}
	}
	return filepath.Dir(resolved), nil
}

const (
	ansiReset   = "\x1b[0m"
	ansiDim     = "\x1b[2m"
	ansiMain    = "\x1b[1;36m"
	ansiReader  = "\x1b[1;35m"
	ansiHuman   = "\x1b[1;33m"
	ansiLinkEnd = "\x1b]8;;\x1b\\"
)

var (
	// pathRef matches file-like tokens such as main.go, ui/src/a.ts:42 or /abs/b.go:3:7.
	pathRef    = regexp.MustCompile(`(?:/|\.{1,2}/)?(?:[\w.@-]+/)*[\w@-][\w.@-]*\.[A-Za-z]\w*(?::(\d+))?(?::\d+)?`)
	codeSpan   = regexp.MustCompile("`[^`\n]+`")
	boldSpan   = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	listMarker = regexp.MustCompile(`^(\s*)([-*]|\d+\.)(\s)`)
)

// notify shows a desktop notification; it is replaced in tests.
var notify = func(title, text string) {
	if runtime.GOOS != "darwin" {
		return
	}
	// Pass text as arguments so it is never parsed as AppleScript.
	_ = exec.Command("osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", title, text).Run()
}

// printer renders the transcript for people. Colors and links need a
// terminal, so redirected logs stay plain; NO_COLOR turns them off.
type printer struct {
	out   io.Writer
	repo  string
	color bool
	day   string
}

func newPrinter(out io.Writer, repo string) *printer {
	f, ok := out.(*os.File)
	tty := ok && term.IsTerminal(f.Fd())
	color := tty && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	return &printer{out: out, repo: repo, color: color}
}

func (p *printer) paint(code, text string) string {
	if !p.color {
		return text
	}
	return code + text + ansiReset
}

func (p *printer) message(v session, m message) error {
	at, err := time.Parse(time.RFC3339Nano, m.At)
	if err != nil {
		return err
	}
	at = at.Local()
	if day := at.Format("Mon, 2 Jan 2006"); day != p.day {
		p.day = day
		if _, err := fmt.Fprintf(p.out, "%s\n\n", p.paint(ansiDim, "── "+day+" ──")); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(p.out, "%s  %s %s %s\n%s\n\n",
		p.paint(ansiDim, at.Format("15:04:05")),
		p.author(v, m.From, true), p.paint(ansiDim, "→"), p.author(v, m.To, false),
		p.body(m.Text))
	return err
}

// author names a message's sender or recipient; withAgent appends the
// member's app, dimmed, after its role.
func (p *printer) author(v session, name string, withAgent bool) string {
	code := ansiReader
	switch name {
	case everyone:
		return "all"
	case human:
		return p.paint(ansiHuman, name)
	case system:
		return p.paint(ansiDim, name)
	case mainRole:
		code = ansiMain
	}
	out := p.paint(code, name)
	if m := v.member(name); withAgent && m != nil && m.Agent != "" {
		out += " " + p.paint(ansiDim, "("+m.app()+")")
	}
	return out
}

// body indents the text and highlights `code`, **bold** and list markers.
func (p *printer) body(text string) string {
	if !p.color {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		line = listMarker.ReplaceAllString(line, "$1\x1b[2m$2\x1b[22m$3")
		line = codeSpan.ReplaceAllStringFunc(line, func(c string) string { return "\x1b[33m" + c + "\x1b[39m" })
		line = boldSpan.ReplaceAllString(line, "\x1b[1m$1\x1b[22m")
		lines[i] = "  " + p.links(line)
	}
	return strings.Join(lines, "\n")
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// links wraps paths to existing files in OSC 8 hyperlinks. PEER_EDITOR_URL
// sets the target, e.g. vscode://file/{path}:{line}; the default is file://{path}.
func (p *printer) links(text string) string {
	if !p.color {
		return text
	}
	tmpl := os.Getenv("PEER_EDITOR_URL")
	if tmpl == "" {
		tmpl = "file://{path}"
	}
	return pathRef.ReplaceAllStringFunc(text, func(ref string) string {
		file, line, _ := strings.Cut(ref, ":")
		line, _, _ = strings.Cut(line, ":")
		if !filepath.IsAbs(file) {
			file = filepath.Join(p.repo, file)
		}
		if st, err := os.Stat(file); err != nil || st.IsDir() {
			return ref
		}
		if line == "" {
			line = "1"
		}
		target := strings.NewReplacer("{path}", (&url.URL{Path: file}).EscapedPath(), "{line}", line).Replace(tmpl)
		return "\x1b]8;;" + target + "\x1b\\" + ref + ansiLinkEnd
	})
}
