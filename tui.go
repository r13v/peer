package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type key int

const (
	keyOther key = iota
	keyUp
	keyDown
	keyEnter
	keyEsc
	keyQuit
	keyTab
)

const (
	altScreenOn  = "\x1b[?1049h\x1b[?25l"
	altScreenOff = "\x1b[?25h\x1b[?1049l"
	clearScreen  = "\x1b[H\x1b[2J"
	recentLimit  = 10
)

var errQuit = errors.New("quit")

// entry is a session with the store that holds it.
type entry struct {
	s *store
	v session
}

// picker lists active sessions in every checkout, then recent ones in
// this checkout. Enter follows the selected session on the normal screen,
// so its transcript stays in the terminal's scrollback; Esc returns here.
func picker(in io.Reader, out io.Writer, cwd string) error {
	fin, ok := in.(*os.File)
	fout, ok2 := out.(*os.File)
	if !ok || !ok2 || !isTerminal(fin.Fd()) || !isTerminal(fout.Fd()) {
		return errors.New(usage)
	}
	local, _ := openStore(cwd) // nil outside a Git checkout
	restore, err := makeRaw(fin.Fd())
	if err != nil {
		return err
	}
	defer restore()
	keys := readKeys(fin)
	fmt.Fprint(out, altScreenOn)
	defer func() { fmt.Fprint(out, altScreenOff) }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	selected := ""
	for {
		entries, err := listEntries(local)
		if err != nil {
			return err
		}
		i := 0
		for j, e := range entries {
			if e.v.ID+e.s.dir == selected {
				i = j
			}
		}
		width, height := termSize(fout.Fd())
		render(out, entries, i, local, width, height)
		select {
		case <-tick.C:
		case k, ok := <-keys:
			switch {
			case !ok || k == keyQuit:
				return nil
			case k == keyUp && i > 0:
				i--
			case k == keyDown && i < len(entries)-1:
				i++
			case k == keyEnter && len(entries) > 0:
				fmt.Fprint(out, altScreenOff)
				err := view(entries[i], keys, out)
				fmt.Fprint(out, altScreenOn)
				if errors.Is(err, errQuit) {
					return nil
				}
				if err != nil {
					return err
				}
			}
			if len(entries) > 0 {
				selected = entries[i].v.ID + entries[i].s.dir
			}
		}
	}
}

// listEntries returns active sessions across checkouts, newest first,
// followed by the latest ended sessions in local, if any. Unreadable
// stores are skipped so one bad entry does not hide the rest.
func listEntries(local *store) ([]entry, error) {
	repos, err := reposDir()
	if err != nil {
		return nil, err
	}
	dirs, err := os.ReadDir(repos)
	if err != nil {
		return nil, err
	}
	var entries []entry
	for _, d := range dirs {
		s := &store{dir: filepath.Join(repos, d.Name())}
		if v, err := s.current(); err == nil && v.EndedAt == "" {
			s.repo = v.Repo
			entries = append(entries, entry{s, v})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].v.StartedAt > entries[j].v.StartedAt })
	if local == nil {
		return entries, nil
	}
	sessions, _ := local.sessions()
	n := 0
	for _, v := range sessions {
		if v.EndedAt != "" && n < recentLimit {
			entries = append(entries, entry{local, v})
			n++
		}
	}
	return entries, nil
}

// render draws the list, scrolled so the selected row stays on screen.
// Lines are cut to the width so none wraps and shifts the rows.
func render(out io.Writer, entries []entry, selected int, local *store, width, height int) {
	var lines []string
	at, section := 0, ""
	for i, e := range entries {
		title := "Active"
		if e.v.EndedAt != "" {
			title = "Recent in " + filepath.Base(local.repo)
		}
		if title != section {
			section = title
			lines = append(lines, "", title)
		}
		marker := "  "
		if i == selected {
			marker, at = "› ", len(lines)
		}
		lines = append(lines, marker+fmt.Sprintf("%-20.20s  ", filepath.Base(e.v.Repo))+e.s.summary(e.v))
	}
	if len(entries) == 0 {
		lines = append(lines, "", "No sessions. Start one with /peer in an agent chat.")
	}
	rows := max(height-1, 1) // the first line is the key help
	first := min(max(at-rows+1, 0), max(len(lines)-rows, 0))
	var b strings.Builder
	b.WriteString(clearScreen + cut("peer  ↑/↓ select · Enter open · Esc back · q quit", width))
	for _, line := range lines[first:min(first+rows, len(lines))] {
		b.WriteString("\n" + cut(line, width))
	}
	io.WriteString(out, b.String())
}

// cut shortens s to fit in width columns, counting one per rune.
func cut(s string, width int) string {
	if r := []rune(s); len(r) >= width {
		return string(r[:max(width-1, 0)])
	}
	return s
}

// view follows one session until Esc. An ended session shows its
// transcript and summary and stays until Esc too. When the reader runs
// headless, Tab switches between the transcript and the reader's log.
func view(e entry, keys <-chan key, out io.Writer) error {
	p := newPrinter(out, e.s.repo, true)
	readerLog := filepath.Join(e.s.dir, "sessions", e.v.ID, "reader.log")
	_, err := os.Stat(readerLog)
	headless, help := err == nil, " · Esc back ──"
	if headless {
		help = " · Tab reader log · Esc back ──"
	}
	fmt.Fprintf(out, "\n%s\n\n", p.paint(ansiDim, "── "+filepath.Base(e.v.Repo)+" "+e.v.ID+help))
	showLog := false
	var stop chan struct{}
	var done chan error
	follow := func() {
		stop, done = make(chan struct{}), make(chan error, 1)
		if showLog {
			go func(stop <-chan struct{}, done chan<- error) { done <- followReaderLog(readerLog, p, stop) }(stop, done)
		} else {
			go func(stop <-chan struct{}, done chan<- error) { done <- e.s.log(e.v.ID, true, p, stop) }(stop, done)
		}
	}
	halt := func() {
		close(stop)
		if done != nil {
			<-done
		}
	}
	follow()
	for {
		select {
		case err := <-done:
			if err != nil {
				return err
			}
			done = nil // the session ended; keep showing it until Esc
		case k, ok := <-keys:
			if ok && k == keyTab && headless {
				halt()
				showLog = !showLog
				title := "transcript"
				if showLog {
					title = e.v.Reader + " log"
				}
				fmt.Fprintf(out, "\n%s\n\n", p.paint(ansiDim, "── "+title+" · Tab switch · Esc back ──"))
				follow()
				continue
			}
			if ok && k != keyEsc && k != keyQuit {
				continue
			}
			halt()
			fmt.Fprintln(out)
			if !ok || k == keyQuit {
				return errQuit
			}
			return nil
		}
	}
}

// followReaderLog prints the headless reader's log as it grows until
// stop is closed. Only complete lines are printed.
func followReaderLog(path string, p *printer, stop <-chan struct{}) error {
	var offset int64
	for {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return err
		}
		r := bufio.NewReader(f)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				break
			}
			offset += int64(len(line))
			if text := readerLogLine(line); text != "" {
				fmt.Fprintln(p.out, p.links(text))
			}
		}
		f.Close()
		select {
		case <-stop:
			return nil
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// readerLogLine turns one log line into display text. Codex writes plain
// text; Claude writes stream-json events, of which only its text, tool
// calls and final result are shown.
func readerLogLine(line []byte) string {
	var ev struct {
		Type    string `json:"type"`
		Result  string `json:"result"`
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &ev) != nil || ev.Type == "" {
		return strings.TrimRight(string(line), "\r\n")
	}
	switch ev.Type {
	case "assistant":
		var parts []string
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				parts = append(parts, c.Text)
			case "tool_use":
				var in struct {
					Command string `json:"command"`
				}
				arg := string(c.Input)
				if json.Unmarshal(c.Input, &in) == nil && in.Command != "" {
					arg = in.Command
				}
				parts = append(parts, "→ "+c.Name+" "+cut(arg, 200))
			}
		}
		return strings.Join(parts, "\n")
	case "result":
		return "result: " + ev.Result
	}
	return ""
}

// readKeys decodes key presses from a raw terminal. A lone Esc is told
// apart from arrow sequences (ESC [ A, ESC O A) by a short pause.
func readKeys(f *os.File) <-chan key {
	raw := make(chan byte)
	go func() {
		b := make([]byte, 1)
		for {
			if _, err := f.Read(b); err != nil {
				close(raw)
				return
			}
			raw <- b[0]
		}
	}()
	keys := make(chan key)
	go func() {
		defer close(keys)
		for b := range raw {
			k := keyOther
			switch b {
			case 'k':
				k = keyUp
			case 'j':
				k = keyDown
			case '\r', '\n':
				k = keyEnter
			case '\t':
				k = keyTab
			case 'q', 3: // 3 is Ctrl-C
				k = keyQuit
			case 0x1b:
				k = keyEsc
				select {
				case b, ok := <-raw:
					if !ok {
						return
					}
					k = keyOther
					if b == '[' || b == 'O' {
						// Give up on a truncated sequence instead of
						// swallowing the next key press.
						select {
						case c := <-raw:
							if c == 'A' {
								k = keyUp
							} else if c == 'B' {
								k = keyDown
							}
						case <-time.After(50 * time.Millisecond):
						}
					}
				case <-time.After(50 * time.Millisecond):
				}
			}
			keys <- k
		}
	}()
	return keys
}
