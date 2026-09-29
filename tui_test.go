package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

func press(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

// loaded returns a model sized like a terminal that has read the rooms
// and the selected room once.
func loaded(t *testing.T, local *store) *model {
	t.Helper()
	m := newModel(local)
	t.Cleanup(m.zones.Close)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	for range 2 { // the first poll lists rooms, the second reads one
		m.Update(m.poll()())
	}
	return m
}

func TestTUIShowsTranscriptAndReaderLog(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "view", "claude", "codex")
	if _, err := invoke(repo, "proposal with `peer log`", "send", v.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "sessions", v.ID, "reader.log"), []byte("codex thinking\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tui := newModel(s)
	t.Cleanup(tui.zones.Close)
	tm := teatest.NewTestModel(t, tui, teatest.WithInitialTermSize(120, 40))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("proposal")) && bytes.Contains(b, []byte("codex thinking")) && bytes.Contains(b, []byte("1 message (claude 1, codex 0)"))
	}, teatest.WithDuration(5*time.Second))
	tm.Type("l/proposal")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Type("q")
	m := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(*model)
	if m.showLog || strings.Contains(m.render(), "codex log") {
		t.Fatal("l did not hide the reader log")
	}
	if m.chat.query != "proposal" || len(m.chat.matches) != 1 || m.chat.cur != 0 {
		t.Fatalf("search did not find the message: %q %+v %d", m.chat.query, m.chat.matches, m.chat.cur)
	}
}

func TestPollDropsReadsOfAnotherRoom(t *testing.T) {
	repo := testRepo(t)
	a := startRoom(t, repo, "a", "claude", "codex")
	startRoom(t, repo, "b", "claude", "codex")
	if _, err := invoke(repo, "only in a", "send", a.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	if m.room.e.v.ID != "b" {
		t.Fatalf("newest room not selected first: %s", m.room.e.v.ID)
	}
	m.choose(1)
	late := m.poll() // reads a
	m.choose(0)
	m.Update(late())
	if m.room.e.v.ID != "b" || m.room.loaded || len(m.room.msgs) != 0 {
		t.Fatalf("a read of a replaced room was shown: %+v", m.room)
	}
}

func TestSelectionFollowsRoomAcrossCheckouts(t *testing.T) {
	repo := testRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	if out, err := exec.Command("git", "init", "-q", other).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	startRoom(t, repo, "same", "claude", "codex")
	startRoom(t, other, "same", "claude", "codex")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	m.choose(1)
	want := m.room.e.key()
	m.setRooms([]entry{m.rooms[1], m.rooms[0]})
	if m.sel != 0 || m.room.e.key() != want {
		t.Fatalf("selection moved to the other room named same: %d", m.sel)
	}
}

func TestCloseRoomFromList(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "stuck", "claude", "codex")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	_, cmd := m.Update(press('x'))
	if cmd == nil {
		t.Fatal("x did nothing on an active room")
	}
	m.Update(cmd())
	for range 2 {
		m.Update(m.poll()())
	}
	if v, err = s.session(v.ID); err != nil || v.EndedReason != "closed in peer" {
		t.Fatalf("room not closed: %+v, %v", v, err)
	}
	if !strings.Contains(m.render(), "closed in peer") {
		t.Fatal("the list does not say why the room ended")
	}
}

func TestReadLinesLeavesPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	if lines, off, ok, err := readLines(path, 0); err != nil || ok || len(lines) != 0 || off != 0 {
		t.Fatalf("missing file: %q %d %v %v", lines, off, ok, err)
	}
	if err := os.WriteFile(path, []byte("a\nb"), 0600); err != nil {
		t.Fatal(err)
	}
	lines, off, _, err := readLines(path, 0)
	if err != nil || len(lines) != 1 || off != 2 {
		t.Fatalf("partial line read: %q %d %v", lines, off, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("c\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if lines, _, _, err = readLines(path, off); err != nil || len(lines) != 1 || string(lines[0]) != "bc\n" {
		t.Fatalf("finished line: %q %v", lines, err)
	}
}

func TestPaneSearchSkipsEscapes(t *testing.T) {
	p := newPane()
	p.vp.SetWidth(40)
	p.vp.SetHeight(2)
	p.setLines([]string{"\x1b[1mщи\x1b[0m foo", "x", "y", "Foo foo"})
	p.search("foo")
	if len(p.matches) != 3 || p.matches[0] != (match{0, 3, 6}) || p.matches[1] != (match{3, 0, 3}) {
		t.Fatalf("lowercase search should ignore case and escapes: %+v", p.matches)
	}
	p.search("Foo")
	if len(p.matches) != 1 {
		t.Fatalf("a capital should match case: %+v", p.matches)
	}
	p.vp.GotoTop()
	p.search("foo")
	p.jump(-1)
	if p.cur != 2 || p.vp.YOffset() == 0 {
		t.Fatalf("N did not wrap to the last hit and scroll to it: %d %d", p.cur, p.vp.YOffset())
	}
}

func TestRoomListKeepsSelectionVisible(t *testing.T) {
	repo := testRepo(t)
	for range 8 {
		v := startRoom(t, repo, "r", "claude", "codex")
		if _, err := invoke(repo, "", "end", v.ID, "--as", "claude"); err != nil {
			t.Fatal(err)
		}
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.choose(7)
	list := ansi.Strip(m.roomList())
	if lines := strings.Split(list, "\n"); len(lines) != m.topH-2 || !strings.HasPrefix(lines[len(lines)-2], " "+m.rooms[7].v.ID) {
		t.Fatalf("selected room and its summary not both on screen:\n%s", list)
	}
}

func TestEmptyRoomListReplacesLastRoom(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "last", "claude", "codex")
	m := loaded(t, nil) // outside a checkout only active rooms are listed
	if len(m.rooms) != 1 {
		t.Fatalf("want the active room: %d", len(m.rooms))
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	m.lastRooms = time.Time{}
	m.Update(m.poll()())
	if len(m.rooms) != 0 {
		t.Fatalf("ended room still listed: %+v", m.rooms)
	}
}

func TestNotifyWhenWatchedRoomEnds(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "watched", "claude", "codex")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	old := notify
	notify = func(_, text string) { got = append(got, text) }
	t.Cleanup(func() { notify = old })
	m := loaded(t, s)
	if _, err := invoke(repo, "", "end", v.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(m.poll()())
	if len(got) != 0 {
		t.Fatal("notified inside Update")
	}
	runAll(cmd)
	if len(got) != 1 || !strings.HasPrefix(got[0], "Session ended: 0 messages") {
		t.Fatalf("want one end notification: %q", got)
	}
}

// runAll runs cmd and any batch it returns, except ticks that would wait.
func runAll(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				runAll(c)
			}
		}
	case <-time.After(pollEvery / 2):
	}
}

func TestEndedRoomOfOtherCheckoutStaysShown(t *testing.T) {
	repo := testRepo(t)
	startRoom(t, repo, "older", "claude", "codex")
	v := startRoom(t, repo, "watched", "claude", "codex")
	m := loaded(t, nil) // outside a checkout, an ended room leaves the list
	if m.room.e.v.ID != "watched" {
		t.Fatalf("newest room not selected: %s", m.room.e.v.ID)
	}
	if _, err := invoke(repo, "last words", "send", v.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	m.lastRooms = time.Time{}
	if _, cmd := m.Update(m.poll()()); cmd == nil {
		t.Fatal("no commands after a poll")
	}
	if m.room.e.v.ID != "watched" || m.room.e.v.EndedAt == "" || len(m.room.msgs) != 1 || m.sel != -1 || len(m.rooms) != 1 {
		t.Fatalf("the ended room's last read was dropped: %+v sel %d", m.room, m.sel)
	}
	m.Update(press('j'))
	if m.room.e.v.ID != "older" {
		t.Fatalf("j did not select the remaining room: %s", m.room.e.v.ID)
	}
}

func TestCtrlCQuitsWhileSearching(t *testing.T) {
	m := newModel(nil)
	t.Cleanup(m.zones.Close)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(press('/'))
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil || cmd() != tea.Quit() {
		t.Fatal("Ctrl-C did not quit while typing a search")
	}
}

func TestFileReferencesBecomeLinks(t *testing.T) {
	repo := testRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v := startRoom(t, repo, "links", "claude", "codex")
	if _, err := invoke(repo, "see main.go:1", "send", v.ID, "--as", "claude"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "sessions", v.ID, "reader.log"), []byte("reading main.go\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	link := "\x1b]8;;file://" + filepath.Join(v.Repo, "main.go")
	if !strings.Contains(m.room.logs[0], link) || !strings.Contains(strings.Join(m.chat.lines, "\n"), link) {
		t.Fatalf("file references not linked:\n%q\n%q", m.room.logs, m.chat.lines)
	}
}
