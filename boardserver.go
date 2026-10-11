package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/r13v/peer/internal/board"
	"github.com/r13v/peer/internal/boardhttp"
	"github.com/r13v/peer/web"
)

// boardStatusMsg tells the TUI what became of the board server.
type boardStatusMsg boardhttp.Status

// startBoards keeps the board server available while the TUI runs, with
// cwd registered as a workspace; stop ends it and waits for the shutdown,
// so the next TUI can take server.lock over.
func startBoards(cwd string, send func(tea.Msg)) (stop func()) {
	home, err := peerHome()
	if err != nil {
		go send(boardStatusMsg{Err: err})
		return func() {}
	}
	cfg := boardhttp.Config{
		Port:      board.Port(),
		PeerHome:  home,
		Version:   version,
		Checkouts: boardCheckouts,
		SPA:       web.Dist(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		boardhttp.Run(ctx, cfg, cwd, func(st boardhttp.Status) { send(boardStatusMsg(st)) })
	}()

	return func() {
		cancel()
		<-done
	}
}

// boardCheckouts lists every checkout whose store is known, and the
// checkouts of workspaces. It serves GET /api/catalog, so it only reads.
func boardCheckouts(workspaces []string) ([]boardhttp.Checkout, error) {
	home, err := peerHome()
	if err != nil {
		return nil, err
	}
	repos := filepath.Join(home, "repos")
	dirs, err := os.ReadDir(repos)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var out []boardhttp.Checkout
	for _, d := range dirs {
		s := &store{dir: filepath.Join(repos, d.Name())}
		sessions := readSessions(s.dir)
		repo, err := readCheckout(s.dir)
		if err != nil && len(sessions) > 0 {
			repo = sessions[0].Repo // a store opened before checkout.json existed
		}
		if repo == "" {
			continue
		}
		c := boardhttp.Checkout{Path: repo, Store: s.dir}
		for _, v := range sessions {
			c.Rooms = append(c.Rooms, boardhttp.Room{ID: v.ID, Active: v.EndedAt == "", StartedAt: v.StartedAt, EndedAt: v.EndedAt, Boards: v.Boards})
		}
		out = append(out, c)
	}
	for _, ws := range workspaces {
		s, err := locateStore(ws)
		if err != nil {
			continue // not a Git checkout: its boards are listed as outside
		}
		known := slices.ContainsFunc(out, func(c boardhttp.Checkout) bool { return c.Store == s.dir })
		if !known {
			out = append(out, boardhttp.Checkout{Path: s.repo, Store: s.dir})
		}
	}

	return out, nil
}

// readSessions lists a store's sessions newest first, as stored: unlike
// store.sessions it does not refresh active ones, which takes the lock and
// may write. An active room may still show active after its main stopped.
func readSessions(dir string) []session {
	s := &store{dir: dir}
	entries, _ := os.ReadDir(filepath.Join(dir, "sessions"))
	var sessions []session
	for _, d := range entries {
		if !d.IsDir() || !sessionID.MatchString(d.Name()) {
			continue
		}
		if v, err := s.session(d.Name()); err == nil {
			sessions = append(sessions, v)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].StartedAt > sessions[j].StartedAt })

	return sessions
}

// readBoards describes the room's boards for its card.
func readBoards(paths []string) []board.Ref {
	refs := make([]board.Ref, len(paths))
	for i, p := range paths {
		refs[i] = board.NewRef(p)
	}

	return refs
}

// boardLine is a board on a room card: its title linked to its page, or
// why it cannot open.
func boardLine(r board.Ref, width int) string {
	name := r.Title
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(r.Path), ".board")
	}
	if r.Error != nil {
		reason := "unreadable"
		if r.Error.Kind == board.KindNotFound {
			reason = "missing"
		}
		return ansi.Truncate(dim.Render("    ▦ "+name+" ")+failedMark.Render(reason), width, "…")
	}
	name = ansi.Truncate(name, max(width-6, 1), "…")

	return dim.Render("    ▦ ") + "\x1b]8;;" + r.URL + "\x1b\\" + blue.Render(name) + ansiLinkEnd
}

// boardOpenedMsg tells whether the browser got a board page.
type boardOpenedMsg struct{ err error }

// boardsPage is the page b opens: the room's only board, or else the home
// page, which lists the boards by room.
func boardsPage(boards []board.Ref, home string) string {
	if len(boards) == 1 && boards[0].Error == nil {
		return boards[0].URL
	}

	return home
}

// openBrowser opens url in the system browser.
func openBrowser(url string) tea.Cmd {
	return func() tea.Msg {
		name := "xdg-open"
		if runtime.GOOS == "darwin" {
			name = "open"
		}

		return boardOpenedMsg{exec.Command(name, url).Run()}
	}
}

// boardsDue says whether a poll should read the room's boards again: when
// its list changed, and otherwise as often as the rooms list.
func boardsDue(r room, paths []string) bool {
	known := make([]string, len(r.boards))
	for i, b := range r.boards {
		known[i] = b.Path
	}

	return !slices.Equal(known, paths) || time.Since(r.boardsAt) >= roomsEvery
}
