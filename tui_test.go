package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	v := startRoom(t, repo, "view")
	if _, err := invoke(repo, "proposal with `peer log`", "send", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "writer"); err != nil {
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
		return bytes.Contains(b, []byte("proposal")) && bytes.Contains(b, []byte("codex thinking")) && bytes.Contains(b, []byte("2 messages (writer 1, reader 0, peer 1)"))
	}, teatest.WithDuration(5*time.Second))
	tm.Type("l/proposal")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Type("q")
	m := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(*model)
	if m.showLog || strings.Contains(m.render(), "reader log") {
		t.Fatal("l did not hide the reader log")
	}
	if chat := ansi.Strip(strings.Join(m.chat.lines, "\n")); !strings.Contains(chat, "writer (claude) → all") || !strings.Contains(chat, "peer → writer\n") {
		t.Fatalf("headers do not name the sender's agent only:\n%s", chat)
	}
	if m.chat.query != "proposal" || len(m.chat.matches) != 1 || m.chat.cur != 0 {
		t.Fatalf("search did not find the message: %q %+v %d", m.chat.query, m.chat.matches, m.chat.cur)
	}
}

func TestPollDropsReadsOfAnotherRoom(t *testing.T) {
	repo := testRepo(t)
	a := startRoom(t, repo, "a")
	startRoom(t, repo, "b")
	if _, err := invoke(repo, "only in a", "send", a.ID, "--as", "writer"); err != nil {
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
	startRoom(t, repo, "same")
	startRoom(t, other, "same")
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
	v := startRoom(t, repo, "stuck")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	if _, cmd := m.Update(press('x')); cmd != nil {
		t.Fatal("the first x closed the room")
	}
	if !strings.Contains(m.statusLine(), "press x again to close stuck") {
		t.Fatalf("status line lacks the close prompt: %q", m.statusLine())
	}
	_, cmd := m.Update(press('x'))
	if cmd == nil {
		t.Fatal("the second x did nothing on an active room")
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

func TestCloseNeedsSecondPressOnSameRoom(t *testing.T) {
	repo := testRepo(t)
	startRoom(t, repo, "one")
	startRoom(t, repo, "two")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		mid  func(m *model)
	}{
		{"another key between", func(m *model) { m.Update(press('m')) }},
		{"window passed", func(m *model) { m.closeAt = m.closeAt.Add(-closeAfter) }},
		{"another room selected", func(m *model) { m.choose(1 - m.sel) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := loaded(t, s)
			m.Update(press('x'))
			c.mid(m)
			if _, cmd := m.Update(press('x')); cmd != nil {
				t.Fatal("x closed a room without confirmation")
			}
		})
	}
	t.Run("list reordered", func(t *testing.T) {
		m := loaded(t, s)
		m.Update(press('x'))
		armed := m.closing.v.ID
		m.setRooms([]entry{m.rooms[1], m.rooms[0]})
		_, cmd := m.Update(press('x'))
		if cmd == nil {
			t.Fatal("the second x did nothing")
		}
		m.Update(cmd())
		for _, id := range []string{"one", "two"} {
			v, err := s.session(id)
			if err != nil {
				t.Fatal(err)
			}
			if closed := v.EndedReason == "closed in peer"; closed != (id == armed) {
				t.Fatalf("room %s: ended %q, armed room was %s", id, v.EndedReason, armed)
			}
		}
	})
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
		v := startRoom(t, repo, "r")
		if _, err := invoke(repo, "", "end", v.ID, "--as", "writer"); err != nil {
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
	if lines := strings.Split(list, "\n"); len(lines) != m.topH || !strings.HasPrefix(lines[len(lines)-3], "│ ❯ ○ "+m.rooms[7].v.ID) {
		t.Fatalf("selected room and its summary not both on screen:\n%s", list)
	}
}

func TestEmptyRoomListReplacesLastRoom(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "last")
	m := loaded(t, nil) // outside a checkout, every checkout's rooms are listed
	if len(m.rooms) != 1 {
		t.Fatalf("want the active room: %d", len(m.rooms))
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(s.dir, "sessions", v.ID)); err != nil {
		t.Fatal(err)
	}
	m.lastRooms = time.Time{}
	m.Update(m.poll()())
	if len(m.rooms) != 0 {
		t.Fatalf("removed room still listed: %+v", m.rooms)
	}
}

func TestNotifyWhenWatchedRoomEnds(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "watched")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	old := notify
	notify = func(_, text string) { got = append(got, text) }
	t.Cleanup(func() { notify = old })
	m := loaded(t, s)
	if _, err := invoke(repo, "", "end", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(m.poll()())
	if len(got) != 0 {
		t.Fatal("notified inside Update")
	}
	runAll(cmd)
	if len(got) != 1 || !strings.HasPrefix(got[0], "Session ended: 1 message") {
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

func TestEndedRoomStaysSelected(t *testing.T) {
	repo := testRepo(t)
	startRoom(t, repo, "older")
	v := startRoom(t, repo, "watched")
	m := loaded(t, nil)
	if m.room.e.v.ID != "watched" {
		t.Fatalf("newest room not selected: %s", m.room.e.v.ID)
	}
	if _, err := invoke(repo, "last words", "send", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "end", v.ID, "--as", "writer"); err != nil {
		t.Fatal(err)
	}
	m.lastRooms = time.Time{}
	if _, cmd := m.Update(m.poll()()); cmd == nil {
		t.Fatal("no commands after a poll")
	}
	if m.room.e.v.ID != "watched" || m.room.e.v.EndedAt == "" || len(m.room.msgs) != 2 || m.sel != 1 || m.rooms[0].v.ID != "older" {
		t.Fatalf("the ended room did not move below the active one, selected: %+v sel %d", m.room, m.sel)
	}
	list := ansi.Strip(m.roomList())
	if !strings.Contains(list, "Active ─") || strings.Contains(list, "writer") || strings.Contains(list, "reader") || !strings.Contains(list, "Today") || !strings.Contains(list, "2 msgs") {
		t.Fatalf("list lacks its sections or count:\n%s", list)
	}
}

func TestNarrowRoomListKeepsCountAndDuration(t *testing.T) {
	m := newModel(nil)
	t.Cleanup(m.zones.Close)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24}) // the narrowest list
	now := time.Now()
	old := session{ID: "long-running", Repo: "/src/peer-chat", Members: []member{{Role: writer, Agent: "claude"}, {Role: "reader", Agent: "codex"}}, StartedAt: now.Add(-(24*time.Hour + 3*time.Minute)).UTC().Format(time.RFC3339Nano)}
	done := session{ID: "done", Repo: "/src/peer-chat", Members: []member{{Role: writer, Agent: "claude"}, {Role: "reader", Agent: "codex"}}, StartedAt: now.Add(-time.Minute).UTC().Format(time.RFC3339Nano), EndedAt: now.UTC().Format(time.RFC3339Nano)}
	m.rooms = []entry{{s: &store{}, v: old, count: 123}, {s: &store{}, v: done, count: 8}}
	m.sel = -1
	list := ansi.Strip(m.roomList())
	if !strings.Contains(list, "123 msgs · 24h 3m") || !strings.Contains(list, "8 msgs · 1m") {
		t.Fatalf("a narrow list clipped counts or durations:\n%s", list)
	}
}

func TestDayLabel(t *testing.T) {
	now := time.Date(2026, 3, 1, 0, 30, 0, 0, time.Local)
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{now.Add(-40 * time.Minute), "Yesterday"},
		{time.Date(2026, 2, 28, 0, 1, 0, 0, time.Local), "Yesterday"},
		{time.Date(2026, 2, 27, 23, 59, 0, 0, time.Local), "Fri 27 Feb"},
		{time.Date(2025, 12, 31, 12, 0, 0, 0, time.Local), "Wed 31 Dec 2025"},
		{now.Add(time.Minute), "Today"},
	} {
		if got := dayLabel(c.t, now); got != c.want {
			t.Errorf("dayLabel(%v) = %q, want %q", c.t, got, c.want)
		}
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
	v := startRoom(t, repo, "links")
	if _, err := invoke(repo, "see main.go:1", "send", v.ID, "--as", "writer"); err != nil {
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
	if !strings.Contains(m.room.logs[0].Text, link) || !strings.Contains(strings.Join(m.chat.lines, "\n"), link) {
		t.Fatalf("file references not linked:\n%+v\n%q", m.room.logs, m.chat.lines)
	}
}

func TestComposeSendsToChosenMember(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "compose")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	m.Update(press('i'))
	for _, r := range "q jx" { // room keys must type, not act
		if _, cmd := m.Update(press(r)); cmd != nil && cmd() == tea.Quit() {
			t.Fatal("q quit while composing")
		}
	}
	m.Update(tea.PasteMsg{Content: " hi"})
	if !strings.Contains(ansi.Strip(m.statusLine()), "to all › q jx hi") {
		t.Fatalf("composer shows %q", ansi.Strip(m.statusLine()))
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !strings.Contains(ansi.Strip(m.statusLine()), "to writer ›") {
		t.Fatalf("Tab did not pick the first member: %q", ansi.Strip(m.statusLine()))
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	msg, err := s.nextMessage(v.ID, writer)
	if err != nil || msg == nil || msg.Text != "q jx hi" || msg.From != human || msg.To != writer {
		t.Fatalf("writer got %+v, %v", msg, err)
	}
	if msg, _ := s.nextMessage(v.ID, "reader"); msg != nil {
		t.Fatalf("reader got %+v", msg)
	}
	if m.compose.Value() != "" || m.sendErr != nil {
		t.Fatalf("draft %q, error %v after a send", m.compose.Value(), m.sendErr)
	}
}

func TestComposeKeepsDraftWhenSendFails(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "compose")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	m.Update(press('i'))
	m.Update(tea.PasteMsg{Content: "draft"})
	if err := s.end(v.ID, writer, io.Discard); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	if m.sendErr == nil || m.compose.Value() != "draft" {
		t.Fatalf("draft %q, error %v after a failed send", m.compose.Value(), m.sendErr)
	}
}

func TestComposeWaitsForSendAndKeepsItsRoom(t *testing.T) {
	repo := testRepo(t)
	startRoom(t, repo, "first")
	startRoom(t, repo, "second")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	target := m.room.e.v.ID
	m.Update(press('i'))
	m.Update(tea.PasteMsg{Content: "hello"})
	m.choose(1 - m.sel)  // the selection moves while composing
	m.Update(m.poll()()) // and the new room is read
	_, send := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.Update(press('i')); m.composing {
		t.Fatal("the composer reopened while a send was in flight")
	}
	m.Update(send())
	if m.compose.Value() != "" || m.sending {
		t.Fatalf("draft %q, sending %v after the send", m.compose.Value(), m.sending)
	}
	for _, id := range []string{"first", "second"} {
		msg, err := s.nextMessage(id, writer)
		if err != nil {
			t.Fatal(err)
		}
		if got := msg != nil; got != (id == target) {
			t.Fatalf("room %s got %+v; the draft was opened in %s", id, msg, target)
		}
	}
}

func TestRoomStatusShowsExitedMember(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cursor-reader"), []byte("0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v := session{StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Members: []member{{Role: writer}, {Role: "reader", Exited: true}}}
	if got := roomStatus(v, dir); !strings.HasSuffix(got, " · reader exited") {
		t.Fatalf("status %q hides the exited member", got)
	}
}

func TestLogSwitchesMembersAndShowsBlocks(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "logs")
	if _, err := invoke(repo, "", "join", v.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.dir, "sessions", v.ID)
	at := "2026-10-01T09:08:07Z\t"
	reader := at + `{"type":"item.completed","item":{"type":"agent_message","text":"reader **says**"}}` + "\n" +
		at + `{"type":"item.completed","item":{"type":"command_execution","command":"go test","aggregated_output":"FAIL\n","exit_code":1,"status":"failed"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "reader.log"), []byte(reader), 0600); err != nil {
		t.Fatal(err)
	}
	tester := "tester plain\n" +
		"2026-10-01T09:08:07Z\t" + `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"make"}}]}}` + "\n" +
		"2026-10-01T09:09:37Z\t" + `{"type":"user","message":{"content":[{"type":"tool_result","content":"built"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "tester.log"), []byte(tester), 0600); err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	out := ansi.Strip(strings.Join(m.log.lines, "\n"))
	stamp := time.Date(2026, 10, 1, 9, 8, 7, 0, time.UTC).Local().Format("15:04:05")
	for _, want := range []string{stamp + "  ● reader", "reader says", stamp + "  ● go test ✗ failed", "  │ FAIL"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log misses %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "**") {
		t.Fatalf("log text not rendered as markdown:\n%s", out)
	}
	m.press(tea.KeyPressMsg{Code: 'm', Text: "m"})
	if !strings.Contains(ansi.Strip(strings.Join(m.log.lines, "\n")), "reader **says**") {
		t.Fatal("m did not show the log as plain text")
	}
	late := m.poll() // reads the reader's log
	m.press(tea.KeyPressMsg{Code: 'L', Text: "L"})
	m.Update(late())
	if m.room.logRole != "tester" || len(m.room.logs) != 0 {
		t.Fatalf("a late read of the previous log was shown: %s %+v", m.room.logRole, m.room.logs)
	}
	m.Update(m.poll()())
	if len(m.room.logs) != 3 || m.room.logs[0].Text != "tester plain" || !strings.Contains(strings.Join(strings.Fields(ansi.Strip(m.render())), " "), "2/2 · L next") {
		t.Fatalf("L did not show the tester's log: %+v", m.room.logs)
	}
	result := time.Date(2026, 10, 1, 9, 9, 37, 0, time.UTC).Local().Format("15:04:05")
	if out := ansi.Strip(strings.Join(m.log.lines, "\n")); !strings.Contains(out, stamp+"  ● Bash make\n\n"+result+"  ↳ output\n  │ built") {
		t.Fatalf("a later result lost its time:\n%s", out)
	}
	m.press(tea.KeyPressMsg{Code: 'L', Text: "L"})
	m.Update(m.poll()())
	if m.room.logRole != "reader" || len(m.room.logs) != 3 {
		t.Fatalf("L did not cycle back to the reader: %s %+v", m.room.logRole, m.room.logs)
	}
}

func TestAddAsksWriterToInvite(t *testing.T) {
	repo := testRepo(t)
	startRoom(t, repo, "first")
	startRoom(t, repo, "second")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	target := m.room.e.v.ID
	m.Update(press('i'))
	m.Update(tea.PasteMsg{Content: "message draft"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(press('a'))
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !strings.Contains(ansi.Strip(m.statusLine()), "[codex]") {
		t.Fatalf("Tab did not pick codex: %q", ansi.Strip(m.statusLine()))
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Fatal("Enter did not focus the description")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil || m.adding != addDescribe {
		t.Fatal("an empty description was sent")
	}
	for _, r := range "q a" { // keys must type, not act
		if _, cmd := m.Update(press(r)); cmd != nil && cmd() == tea.Quit() {
			t.Fatal("q quit while adding")
		}
	}
	m.Update(tea.PasteMsg{Content: " security reviewer"})
	m.choose(1 - m.sel) // the selection moves while typing
	m.Update(m.poll()())
	_, send := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(send())
	if m.addInput.Value() != "" || m.compose.Value() != "message draft" || !strings.Contains(m.statusLine(), "asked the writer to add codex") {
		t.Fatalf("description %q, message draft %q, status %q", m.addInput.Value(), m.compose.Value(), ansi.Strip(m.statusLine()))
	}
	msg, err := s.nextMessage(target, writer)
	if err != nil || msg == nil || msg.From != human || msg.To != writer ||
		!strings.Contains(msg.Text, "\n\nq a security reviewer\n\n") || !strings.Contains(msg.Text, "peer invite "+target+" ROLE --as writer --agent codex") {
		t.Fatalf("writer got %+v, %v", msg, err)
	}
	other := "first"
	if target == other {
		other = "second"
	}
	if msg, _ := s.nextMessage(other, writer); msg != nil {
		t.Fatalf("the other room got %+v", msg)
	}
}

func TestAddKeepsDescriptionWhenRoomEnds(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "add")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	m.Update(press('a'))
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.PasteMsg{Content: "tech riter"})
	for range len("riter") {
		m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	}
	m.Update(press('w'))
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.addInput.Value() != "tech writer" || m.addInput.Position() != len("tech wr") {
		t.Fatalf("arrows did not move the cursor: %q at %d", m.addInput.Value(), m.addInput.Position())
	}
	if err := s.end(v.ID, writer, io.Discard); err != nil {
		t.Fatal(err)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(cmd())
	if m.sendErr == nil || m.addInput.Value() != "tech writer" || m.added != "" {
		t.Fatalf("description %q, error %v, added %q after a failed send", m.addInput.Value(), m.sendErr, m.added)
	}
}

func TestScrollbar(t *testing.T) {
	// thumbAt returns the thumb's first half cell and its size in halves.
	thumbAt := func(bar []string) (first, n int) {
		first = -1
		for i, c := range bar {
			for j, half := range map[string][]bool{"┃": {true, true}, "╹": {true, false}, "╻": {false, true}}[ansi.Strip(c)] {
				if half {
					if first < 0 {
						first = 2*i + j
					}
					n++
				}
			}
		}
		return first, n
	}
	for _, c := range []struct{ h, total, visible int }{{0, 5, 2}, {1, 0, 1}, {3, 3, 3}, {2, 1, 5}} {
		if bar := scrollbar(c.h, c.total, c.visible, 0, " ", thumbOn); len(bar) != max(c.h, 0) || slices.ContainsFunc(bar, func(s string) bool { return s != " " }) {
			t.Fatalf("%+v: %q", c, bar)
		}
	}
	// Ten rows show 18 halves of travel over 90 offsets, so offset 5 is
	// half a cell down: the thumb straddles two cells.
	if bar := scrollbar(10, 100, 10, 5, " ", thumbOn); ansi.Strip(bar[0]) != "╻" || ansi.Strip(bar[1]) != "╹" {
		t.Fatalf("thumb does not move by half cells: %q", bar[:3])
	}
	for _, h := range []int{1, 2, 10} {
		prev := -1
		for off := 0; off <= 90; off++ {
			first, n := thumbAt(scrollbar(h, 100, 10, off, " ", thumbOn))
			if n < 1 || first < prev || first+n > 2*h {
				t.Fatalf("h %d offset %d: thumb at %d size %d after %d", h, off, first, n, prev)
			}
			prev = first
		}
		if first, n := thumbAt(scrollbar(h, 100, 10, 90, " ", thumbOn)); first+n != 2*h {
			t.Fatalf("h %d: thumb does not reach the bottom at the last offset", h)
		}
	}
}

func TestRenderFitsWindow(t *testing.T) {
	repo := testRepo(t)
	for range 8 {
		startRoom(t, repo, "r")
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	for _, size := range [][2]int{{100, 30}, {80, 12}, {60, 8}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(m.zones.Scan(m.render()), "\n")
		if len(lines) != size[1] {
			t.Fatalf("%v: %d lines", size, len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Fatalf("%v: line %d is %d wide: %q", size, i, w, ansi.Strip(l))
			}
		}
	}
}

func TestNoticeOutranksLongRoomID(t *testing.T) {
	repo := testRepo(t)
	startRoom(t, repo, strings.Repeat("x", 40))
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 8})
	m.sendErr = errors.New("room ended")
	if line := ansi.Strip(m.statusLine()); !strings.Contains(line, "not sent: room ended") {
		t.Fatalf("notice hidden: %q", line)
	}
}

func TestMouseOnPanesRoomsAndScrollbar(t *testing.T) {
	repo := testRepo(t)
	for range 3 {
		startRoom(t, repo, "r")
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.chat.setLines(slices.Repeat([]string{"line"}, 100))
	// Zones are recorded off the update loop in order; the log is marked last.
	m.zones.Scan(m.render())
	for deadline := time.Now().Add(2 * time.Second); m.zones.Get("log").IsZero(); {
		if time.Now().After(deadline) {
			t.Fatal("zones not recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	at := func(id string) (int, int) { z := m.zones.Get(id); return z.StartX, z.StartY }
	x, y := at("log")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if m.focus != focusLog {
		t.Fatalf("log click left focus %d", m.focus)
	}
	off, z := m.chat.vp.YOffset(), m.zones.Get("chat")
	m.Update(tea.MouseWheelMsg{X: z.EndX, Y: z.StartY + 5, Button: tea.MouseWheelUp})
	if off-m.chat.vp.YOffset() != 1 { // a line a step scrolls smoothly
		t.Fatalf("wheel over the chat scrollbar left offset %d", m.chat.vp.YOffset())
	}
	// Choosing a room replaces the chat pane, so this goes last.
	x, y = at("room1")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if m.sel != 1 || m.focus != focusRooms {
		t.Fatalf("room click: sel %d focus %d", m.sel, m.focus)
	}
}

func TestUserBarAndPaletteSwitch(t *testing.T) {
	t.Cleanup(func() { applyPalette(true) })
	repo := testRepo(t)
	v := startRoom(t, repo, "r")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.post(v.ID, everyone, "привет `peer wait` готово"); err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	userLines := func() string {
		var out []string
		for _, l := range m.chat.lines {
			if w := ansi.StringWidth(l); w > m.chat.vp.Width() {
				t.Fatalf("line %d wide in a %d pane: %q", w, m.chat.vp.Width(), ansi.Strip(l))
			}
			if strings.HasPrefix(ansi.Strip(l), "▎ ") {
				out = append(out, l)
			}
		}
		return strings.Join(out, "\n")
	}
	for _, md := range []bool{true, false} {
		m.markdown = md
		m.renderChat()
		if got := ansi.Strip(userLines()); !strings.Contains(got, "привет") || !strings.Contains(got, "peer wait") || !strings.Contains(got, "готово") {
			t.Fatalf("markdown %v: user body lost text: %q", md, got)
		}
	}
	m.markdown = true // repaint the cached markdown, not just plain text
	m.renderChat()
	dark := userLines()
	id := m.room.msgs[slices.IndexFunc(m.room.msgs, func(msg message) bool { return msg.From == human })].ID
	darkBody := m.rendered[id]
	m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	if lightBody := m.rendered[id]; lightBody == darkBody || ansi.Strip(lightBody) != ansi.Strip(darkBody) {
		t.Fatal("the cached markdown was not rendered again in the light palette")
	}
	if pal != lightPalette || m.help.Styles.FullKey.GetForeground() != lipgloss.Color(lightPalette.fg) {
		t.Fatal("light background did not switch the palette and the help")
	}
	if light := userLines(); light == dark || ansi.Strip(light) != ansi.Strip(dark) {
		t.Fatal("the transcript was not repainted in the light palette")
	}
}

func TestFramesAndKeyModal(t *testing.T) {
	repo := testRepo(t)
	for range 2 {
		startRoom(t, repo, "r")
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.focus = focusChat
	rows := strings.Split(m.zones.Scan(m.render()), "\n")
	corner := func(st lipgloss.Style) int {
		return strings.Index(rows[0], strings.TrimSuffix(st.Render("┌"), "\x1b[m"))
	}
	// Rooms take the left of the top row, the transcript its right.
	if r, c := corner(rule), corner(blue); r != 0 || c <= r {
		t.Fatalf("only the focused frame should be blue: %q", rows[0])
	}
	base := m.zones.Scan(m.render())
	m.showHelp = true
	out := strings.Split(m.View().Content, "\n")
	if len(out) != 30 || !strings.Contains(m.View().Content, "Keys") || !strings.Contains(ansi.Strip(m.View().Content), "any key closes") {
		t.Fatalf("key list not laid over the %d-row frame", len(out))
	}
	// The key list sits in the middle: the frame's edges show around it.
	if ansi.Strip(out[0]) != ansi.Strip(strings.Split(base, "\n")[0]) || strings.Contains(ansi.Strip(out[15]), "No messages") {
		t.Fatal("the key list is not centered over the panes")
	}
	for deadline := time.Now().Add(2 * time.Second); m.zones.Get("room1").IsZero(); {
		if time.Now().After(deadline) {
			t.Fatal("zones not recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	z := m.zones.Get("room1")
	m.Update(tea.MouseClickMsg{X: z.StartX, Y: z.StartY, Button: tea.MouseLeft})
	m.Update(tea.MouseWheelMsg{X: z.StartX, Y: z.StartY, Button: tea.MouseWheelDown})
	if m.sel != 0 {
		t.Fatalf("the mouse chose room %d under the key list", m.sel)
	}
	// However short the screen, the key list's frame shows whole.
	for _, size := range [][2]int{{80, 12}, {60, 8}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		modal := strings.Split(ansi.Strip(m.helpModal()), "\n")
		if len(modal) > size[1] || !strings.HasPrefix(modal[len(modal)-1], "└") {
			t.Fatalf("%v: the key list's frame is cut: %d rows", size, len(modal))
		}
	}
}

func TestShortcutsWorkInRussianLayout(t *testing.T) {
	for in, want := range map[tea.KeyPressMsg]string{
		press('о'): "j", press('Т'): "N", press('Д'): "L", press('.'): "/", press(','): "?", press('х'): "[",
		{Code: 'т', Mod: tea.ModShift}: "N",
		{Code: 'й', Mod: tea.ModCtrl}:  "ctrl+q", {Code: 'й', Mod: tea.ModAlt}: "alt+q",
		{Code: 'г', Mod: tea.ModCtrl}:                 "ctrl+u",
		{Code: 'с', Mod: tea.ModCtrl, BaseCode: 'c'}:  "ctrl+c",
		{Code: 'ю', Mod: tea.ModShift, BaseCode: '.'}: ">",
		{Code: 'о', Text: "о", BaseCode: 'k'}:         "k", {Code: '.', Text: ".", BaseCode: '.'}: ".", {Code: 'т', Text: "Т", BaseCode: 'n'}: "N", {Code: 'т', Text: "т", Mod: tea.ModShift, BaseCode: 'n'}: "n",
		{Code: 'ق', Text: "ق", BaseCode: 'f'}: "f", // Arabic, from the kitty protocol
		press('q'):                            "q", {Code: tea.KeyEnter}: "enter",
	} {
		if got := latin(in).String(); got != want {
			t.Errorf("%q gave %q, want %q", in.String(), got, want)
		}
	}
	repo := testRepo(t)
	for range 3 {
		startRoom(t, repo, "r")
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	m.Update(press('о'))
	if m.sel != 1 {
		t.Fatalf("о did not move down the rooms: sel %d", m.sel)
	}
	m.Update(m.poll()()) // read the room it moved to
	m.Update(press('ш'))
	for _, r := range "привет" {
		m.Update(press(r))
	}
	if !m.composing || m.compose.Value() != "привет" {
		t.Fatalf("composer took %q, composing %v", m.compose.Value(), m.composing)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.Update(press('.'))
	if !m.searching {
		t.Fatal(". did not open the search")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if _, cmd := m.Update(press('й')); cmd == nil || cmd() != tea.Quit() {
		t.Fatal("й did not quit")
	}
}

func TestLogGrowsAsIfRenderedWhole(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "grow")
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "sessions", v.ID, "reader.log")
	cmd := "2026-10-01T09:08:07Z\t" + `{"type":"item.completed","item":{"type":"command_execution","command":"go test","aggregated_output":"ok\n","exit_code":0,"status":"completed"}}` + "\n"
	if err := os.WriteFile(path, []byte(cmd+"raw one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := loaded(t, s)
	// The added entries continue the last raw block, then start a new day.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("raw two\n2026-10-03T10:00:00Z\t" + `{"type":"item.completed","item":{"type":"agent_message","text":"next day"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	m.Update(m.poll()())
	grown := slices.Clone(m.log.lines)
	if !strings.Contains(ansi.Strip(strings.Join(grown, "\n")), "next day") {
		t.Fatalf("the poll did not extend the log: %q", grown)
	}
	m.logDone = logDone{}
	m.renderLog()
	if !slices.Equal(grown, m.log.lines) {
		t.Fatalf("extended log differs from a whole render:\n%q\n%q", grown, m.log.lines)
	}
	// The role invited again as claude names claude in every heading.
	m.room.e.v.member("reader").Agent = "claude"
	m.renderLog()
	if out := ansi.Strip(strings.Join(m.log.lines, "\n")); !strings.Contains(out, "reader (claude, readonly)") {
		t.Fatalf("the log kept the old agent: %s", out)
	}
}
