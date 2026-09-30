package main

import (
	"bytes"
	"io"
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
	if chat := ansi.Strip(strings.Join(m.chat.lines, "\n")); !strings.Contains(chat, "writer claude → all") || !strings.Contains(chat, "peer → writer\n") {
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
	if lines := strings.Split(list, "\n"); len(lines) != m.topH-2 || !strings.HasPrefix(lines[len(lines)-2], " ○ "+m.rooms[7].v.ID) {
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
	if !strings.Contains(list, "Active · 1") || !strings.Contains(list, "Today") || !strings.Contains(list, "2 msgs") {
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
	if !strings.Contains(list, "123 msgs · 24h 3m ·") || !strings.Contains(list, "8 msgs · 1m ·") {
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
	for _, want := range []string{stamp + "  reader", "reader says", stamp + "  $ go test ✗ failed", "  │ FAIL"} {
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
	if len(m.room.logs) != 3 || m.room.logs[0].Text != "tester plain" || !strings.Contains(ansi.Strip(m.render()), "tester log 2/2") {
		t.Fatalf("L did not show the tester's log: %+v", m.room.logs)
	}
	result := time.Date(2026, 10, 1, 9, 9, 37, 0, time.UTC).Local().Format("15:04:05")
	if out := ansi.Strip(strings.Join(m.log.lines, "\n")); !strings.Contains(out, stamp+"  $ Bash make\n\n"+result+"  ↳ output\n  │ built") {
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
