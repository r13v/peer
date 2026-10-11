package boardhttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/r13v/peer/internal/board"
)

const testPort = 4747

func testConfig(t *testing.T, port int, checkouts []Checkout) Config {
	t.Helper()
	return Config{
		Port: port, PeerHome: t.TempDir(), Version: "test",
		Checkouts: func([]string) ([]Checkout, error) { return checkouts, nil },
		SPA:       fstest.MapFS{"index.html": {Data: []byte("<!doctype html><p>spa")}},
	}
}

// newBoard creates a board under parent and returns its canonical path.
func newBoard(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name+".board")
	if _, err := board.Create(dir, name, ""); err != nil {
		t.Fatal(err)
	}
	c, err := board.Canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// call sends a request the way the board's own tab does; edit changes
// it first, to break one rule.
func call(h http.Handler, method, path, contentType string, body []byte, edit func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Host = "127.0.0.1:4747"
	if method == http.MethodPost {
		r.Header.Set("X-Peer", "1")
		r.Header.Set("Origin", "http://127.0.0.1:4747")
		r.Header.Set("Content-Type", contentType)
	}
	if edit != nil {
		edit(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRejectsForeignHostOriginAndMissingXPeer(t *testing.T) {
	h := New(testConfig(t, testPort, nil)).Handler()
	dir := newBoard(t, t.TempDir(), "plan")
	before := readFile(t, filepath.Join(dir, "board.json"))
	apply := "/api/boards/" + board.Key(dir) + "/apply"
	pack := []byte(`{"base":0,"ops":[{"op":"add","node":{"type":"card","text":"x"}}]}`)
	cases := []struct {
		name         string
		method, path string
		edit         func(*http.Request)
	}{
		{"rebound host", http.MethodGet, "/api/health", func(r *http.Request) { r.Host = "evil.example:4747" }},
		{"IPv6 literal host", http.MethodGet, "/", func(r *http.Request) { r.Host = "[::1]:4747" }},
		{"origin of the other loopback name", http.MethodPost, apply, func(r *http.Request) { r.Host = "localhost:4747" }},
		{"write without X-Peer", http.MethodPost, apply, func(r *http.Request) { r.Header.Del("X-Peer") }},
		{"write with null Origin", http.MethodPost, apply, func(r *http.Request) { r.Header.Set("Origin", "null") }},
		{"write without Origin", http.MethodPost, apply, func(r *http.Request) { r.Header.Del("Origin") }},
		{"write from another site", http.MethodPost, apply, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }},
		{"API read from another site", http.MethodGet, "/api/boards/" + board.Key(dir), func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }},
		{"SSE from another site", http.MethodGet, "/api/boards/" + board.Key(dir) + "/events", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }},
	}
	for _, c := range cases {
		w := call(h, c.method, c.path, "application/json", pack, c.edit)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", c.name, w.Code)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s: answered with CORS headers", c.name)
		}
	}
	if !bytes.Equal(readFile(t, filepath.Join(dir, "board.json")), before) {
		t.Fatal("a rejected write changed the board")
	}
	if w := call(h, http.MethodGet, "/api/health", "", nil, func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:4747") }); w.Code != http.StatusOK {
		t.Fatalf("own origin rejected: %d", w.Code)
	}
	localhost := func(r *http.Request) {
		r.Host, r.Header["Origin"] = "localhost:4747", []string{"http://localhost:4747"}
	}
	if w := call(h, http.MethodPost, apply, "application/json", pack, localhost); w.Code != http.StatusOK {
		t.Fatalf("a write from the localhost page answered %d %s", w.Code, w.Body)
	}
}

func TestBadBoardLinkIs404AndWritesNothing(t *testing.T) {
	h := New(testConfig(t, testPort, nil)).Handler()
	empty, err := board.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(empty, "ghost.board")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{board.Key(dir), board.Key(empty), "not_base64*"} {
		w := call(h, http.MethodGet, "/b/"+key, "", nil, nil)
		if w.Code != http.StatusNotFound || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Errorf("GET /b/%s: %d %s, want a 404 HTML page", key, w.Code, w.Header().Get("Content-Type"))
		}
		if w.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("GET /b/%s: the error page can be framed", key)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("opening a bad link wrote %d files", len(entries))
	}
	if w := call(h, http.MethodGet, "/b/"+board.Key(newBoard(t, empty, "real")), "", nil, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "spa") {
		t.Fatalf("a valid link did not open the SPA: %d", w.Code)
	}
}

func TestApplyReturnsSnapshotAndReportsConflict(t *testing.T) {
	h := New(testConfig(t, testPort, nil)).Handler()
	dir := newBoard(t, t.TempDir(), "plan")
	apply := "/api/boards/" + board.Key(dir) + "/apply"

	w := call(h, http.MethodPost, apply, "application/json", []byte(`{"base":0,"ops":[{"op":"add","ref":"$a","node":{"type":"card","text":"a","x":10,"y":20}}]}`), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", w.Code, w.Body)
	}
	var got struct {
		Revision int64             `json:"revision"`
		Refs     map[string]string `json:"refs"`
		Board    board.Board       `json:"board"`
		Hash     string            `json:"hash"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Revision != 1 || got.Board.Revision != 1 || len(got.Board.Nodes) != 1 || got.Refs["$a"] == "" {
		t.Fatalf("apply did not return the new board: %s", w.Body)
	}
	if got.Hash != board.Hash(readFile(t, filepath.Join(dir, "board.json"))) {
		t.Fatal("the hash is not that of board.json on disk")
	}

	w = call(h, http.MethodPost, apply, "application/json", []byte(`{"base":0,"ops":[{"op":"update","id":"`+got.Refs["$a"]+`","set":{"text":"b"}}]}`), nil)
	var conflict board.Error
	if err := json.Unmarshal(w.Body.Bytes(), &conflict); err != nil || w.Code != http.StatusConflict || conflict.Kind != board.KindConflict {
		t.Fatalf("a stale base answered %d %s, want 409 conflict", w.Code, w.Body)
	}
	if w := call(h, http.MethodPost, apply, "text/plain", []byte(`{}`), nil); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a non-JSON pack answered %d, want 415", w.Code)
	}
}

func TestAssetUploadChecksTypeAndSize(t *testing.T) {
	h := New(testConfig(t, testPort, nil)).Handler()
	dir := newBoard(t, t.TempDir(), "plan")
	upload := "/api/boards/" + board.Key(dir) + "/assets"
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}

	if w := call(h, http.MethodPost, upload, "text/html", img.Bytes(), nil); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("an upload as text/html answered %d, want 415", w.Code)
	}
	if w := call(h, http.MethodPost, upload, "image/png", make([]byte, board.MaxAssetSize+1), nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized upload answered %d, want 413", w.Code)
	}
	w := call(h, http.MethodPost, upload, "image/png", img.Bytes(), nil)
	var asset board.Asset
	if err := json.Unmarshal(w.Body.Bytes(), &asset); err != nil || w.Code != http.StatusOK || asset.Width != 3 || asset.Height != 2 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}

	w = call(h, http.MethodGet, "/b/"+board.Key(dir)+"/"+asset.Asset, "", nil, nil)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), img.Bytes()) || w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatalf("the stored asset is not served back: %d %v", w.Code, w.Header())
	}
	if w := call(h, http.MethodGet, "/b/"+board.Key(dir)+"/assets/..%2Fboard.json", "", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("an asset path outside the pattern answered %d", w.Code)
	}
}

func TestAssetSymlinkIsNotServed(t *testing.T) {
	h := New(testConfig(t, testPort, nil)).Handler()
	dir := newBoard(t, t.TempDir(), "plan")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("a", 64) + ".png"
	if err := os.Symlink(secret, filepath.Join(dir, "assets", name)); err != nil {
		t.Fatal(err)
	}
	if w := call(h, http.MethodGet, "/b/"+board.Key(dir)+"/assets/"+name, "", nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("a symlinked asset answered %d", w.Code)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// nextEvent returns the data of the next "board" event.
func nextEvent(t *testing.T, events chan boardEvent) boardEvent {
	t.Helper()
	select {
	case ev := <-events:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no SSE event in 5s")
	}
	return boardEvent{}
}

func TestEventsMarkOutOfBandWritesExternal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	srv := httptest.NewUnstartedServer(New(testConfig(t, port, nil)).Handler())
	srv.Listener = ln
	srv.Start()
	defer srv.Close()
	dir := newBoard(t, t.TempDir(), "plan")
	file := filepath.Join(dir, "board.json")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/boards/"+board.Key(dir)+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := make(chan boardEvent, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var ev boardEvent
				_ = json.Unmarshal([]byte(data), &ev)
				events <- ev
			}
		}
	}()

	first := nextEvent(t, events)
	if first.Hash != board.Hash(readFile(t, file)) || first.External {
		t.Fatalf("the first event is not the current state: %+v", first)
	}

	if _, err := board.ApplyPack(dir, board.Pack{Base: new(int64), Ops: []board.Op{{Op: "add", Node: &board.Node{Type: board.TypeCard, Text: new("a")}}}}, board.Author{Role: "user"}, board.Browser); err != nil {
		t.Fatal(err)
	}
	if ev := nextEvent(t, events); ev.Revision != 1 || ev.External {
		t.Fatalf("a peer write is reported as %+v, want revision 1, not external", ev)
	}

	// An editor or git rewrites the file at the same revision.
	b, _, err := board.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	b.Title = "renamed outside peer"
	data, err := board.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if ev := nextEvent(t, events); ev.Revision != 1 || !ev.External || ev.Hash != board.Hash(data) {
		t.Fatalf("an out-of-band write is reported as %+v, want external at revision 1", ev)
	}

	// A broken file still reaches the tab, so it shows the error.
	if err := os.WriteFile(file, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ev := nextEvent(t, events); !ev.External || ev.Hash != board.Hash([]byte("{")) {
		t.Fatalf("a corrupted board.json is reported as %+v, want external with its hash", ev)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if ev := nextEvent(t, events); !ev.External || ev.Hash != "" {
		t.Fatalf("a removed board.json is reported as %+v, want external with no hash", ev)
	}
}

func TestCatalogShowsEachBoardOnce(t *testing.T) {
	root, err := board.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkout, store, elsewhere := filepath.Join(root, "repo"), filepath.Join(root, "store"), filepath.Join(root, "elsewhere")
	for _, d := range []string{checkout, store, elsewhere} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	inRepo := newBoard(t, checkout, "design") // linked by the room and found by the scan
	linkedStore := newBoard(t, filepath.Join(store, "boards"), "linked")
	storeOnly := newBoard(t, filepath.Join(store, "boards"), "notes")
	foundOnly := newBoard(t, filepath.Join(checkout, "docs"), "found")
	outside := newBoard(t, elsewhere, "loose")
	gone := filepath.Join(checkout, "gone.board")
	rooms := []Room{{ID: "r1", Active: true, StartedAt: "2026-10-11T10:00:00Z", Boards: []string{inRepo, linkedStore, gone}}}
	s := New(testConfig(t, testPort, []Checkout{{Path: checkout, Store: store, Rooms: rooms}}))
	s.Register(checkout)
	s.Register(elsewhere)

	c, err := s.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	paths := func(refs []board.Ref) []string {
		out := []string{}
		for _, r := range refs {
			out = append(out, r.Path)
		}
		return out
	}
	if len(c.Groups) != 1 || len(c.Groups[0].Rooms) != 1 {
		t.Fatalf("want one group with one room: %+v", c)
	}
	g := c.Groups[0]
	if got := paths(g.Rooms[0].Boards); strings.Join(got, ",") != strings.Join([]string{inRepo, linkedStore, gone}, ",") {
		t.Errorf("room boards: %v", got)
	}
	if r := g.Rooms[0].Boards[2]; r.Error == nil || r.Error.Kind != board.KindNotFound {
		t.Errorf("a removed room board has no notFound error: %+v", r)
	}
	if got := paths(g.Store); len(got) != 1 || got[0] != storeOnly {
		t.Errorf("store repeats a room board or misses one: %v", got)
	}
	if got := paths(g.Found); len(got) != 1 || got[0] != foundOnly {
		t.Errorf("found repeats a room board or misses one: %v", got)
	}
	if got := paths(c.Outside); len(got) != 1 || got[0] != outside {
		t.Errorf("outside: %v", got)
	}
}

func TestSecondPeerTakesServerOverWhenOwnerExits(t *testing.T) {
	cfg := testConfig(t, freePort(t), nil)
	type run struct {
		statuses chan Status
		cancel   context.CancelFunc
		done     chan struct{}
	}
	start := func() run {
		ctx, cancel := context.WithCancel(context.Background())
		r := run{statuses: make(chan Status, 16), cancel: cancel, done: make(chan struct{})}
		go func() {
			defer close(r.done)
			Run(ctx, cfg, t.TempDir(), func(st Status) { r.statuses <- st })
		}()
		t.Cleanup(func() { cancel(); <-r.done })
		return r
	}
	await := func(r run, want func(Status) bool, what string) {
		t.Helper()
		deadline := time.After(15 * time.Second)
		for {
			select {
			case st := <-r.statuses:
				if want(st) {
					return
				}
			case <-deadline:
				t.Fatalf("timed out waiting until %s", what)
			}
		}
	}

	first := start()
	await(first, func(st Status) bool { return st.Owner }, "the first peer runs the server")
	second := start()
	await(second, func(st Status) bool { return st.URL != "" && !st.Owner }, "the second peer uses it")

	first.cancel()
	<-first.done
	await(second, func(st Status) bool { return st.Owner }, "the second peer takes the server over")
	h, err := NewClient(cfg.Port).Health(context.Background())
	if err != nil || h.PeerHome != cfg.PeerHome {
		t.Fatalf("the new server does not answer for the same PEER_HOME: %+v %v", h, err)
	}
}

func TestServeRefusesPortTakenOnIPv6Loopback(t *testing.T) {
	port := freePort(t)
	other, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", port))
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	defer other.Close()
	err = serve(context.Background(), testConfig(t, port, nil), t.TempDir(), func() { t.Error("served beside another program on [::1]") })
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("serve answered %v, want a busy port", err)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("serve kept 127.0.0.1:%d: %v", port, err)
	}
	ln.Close()
}
