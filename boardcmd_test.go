package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/r13v/peer/internal/board"
	"github.com/r13v/peer/internal/boardhttp"
)

func boardErr(t *testing.T, err error, kind string) {
	t.Helper()
	var be *board.Error
	if !errors.As(err, &be) || be.Kind != kind {
		t.Fatalf("want %s error, got %v", kind, err)
	}
}

func TestBoardRoomLink(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "design")
	if _, err := invoke(repo, "", "board", "create", "arch", "--as", "reader"); err == nil {
		t.Fatal("an agent created a board without --room")
	}
	created, err := invoke(repo, "", "board", "create", "arch", "--title", "Arch", "--as", "reader", "--room", v.ID)
	var ref board.Ref
	if err != nil || json.Unmarshal([]byte(created), &ref) != nil || ref.Title != "Arch" || !slices.Equal(ref.Rooms, []string{v.ID}) {
		t.Fatalf("create: %s, %v", created, err)
	}
	if !strings.HasSuffix(ref.URL, "/b/"+ref.Key) || !strings.HasSuffix(ref.Path, filepath.Join("boards", "arch.board")) {
		t.Fatalf("ref %+v", ref)
	}

	applied, err := invoke(repo, `{"base":0,"ops":[{"op":"add","ref":"$c","node":{"type":"card","text":"hi"}},{"op":"comment","target":{"kind":"node","id":"$c"},"body":"why"}]}`,
		"board", "apply", "arch", "--as", "reader", "--room", v.ID)
	var res board.ApplyResult
	if err != nil || json.Unmarshal([]byte(applied), &res) != nil || res.Refs["$c"] != "n1" || res.Revision != 1 || len(res.Warnings) != 0 {
		t.Fatalf("apply: %s, %v", applied, err)
	}
	_, err = invoke(repo, `{"base":1,"ops":[{"op":"update","id":"n1","set":{"x":1}}]}`, "board", "apply", "arch", "--as", "reader", "--room", v.ID)
	boardErr(t, err, board.KindForbidden)

	// A second board used through the room is linked once, after the first.
	other := filepath.Join(repo, "docs", "other.board")
	if _, err := invoke(repo, "", "board", "create", "other", "--path", "docs", "--as", "user"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := invoke(repo, "", "board", "inspect", "docs/other.board", "--overview", "--as", "main", "--room", v.ID); err != nil {
			t.Fatal(err)
		}
	}
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	room, err := s.session(v.ID)
	other, _ = board.Canonical(other)
	if err != nil || !slices.Equal(room.Boards, []string{ref.Path, other}) {
		t.Fatalf("room boards %v, %v", room.Boards, err)
	}

	listed, err := invoke(repo, "", "board", "list")
	var list struct{ Boards []board.Ref }
	if err != nil || json.Unmarshal([]byte(listed), &list) != nil || len(list.Boards) != 2 {
		t.Fatalf("list: %s, %v", listed, err)
	}
	for _, r := range list.Boards {
		if !slices.Equal(r.Rooms, []string{v.ID}) || r.Error != nil {
			t.Fatalf("listed %+v", r)
		}
	}

	_, err = invoke(repo, "", "board", "inspect", "arch", "--as", "ghost", "--room", v.ID)
	boardErr(t, err, board.KindRoom)
	if _, err := invoke(repo, "", "kick", v.ID, "reader", "--as", "main"); err != nil {
		t.Fatal(err)
	}
	_, err = invoke(repo, `{"base":1,"ops":[{"op":"reply","thread":"t2","body":"x"}]}`, "board", "apply", "arch", "--as", "reader", "--room", v.ID)
	boardErr(t, err, board.KindRoom)
	if _, err := invoke(repo, "", "end", v.ID, "--as", "main"); err != nil {
		t.Fatal(err)
	}
	_, err = invoke(repo, `{"base":1,"ops":[{"op":"reply","thread":"t2","body":"x"}]}`, "board", "apply", "arch", "--as", "main", "--room", v.ID)
	boardErr(t, err, board.KindRoom)
	// The board outlives the room.
	if _, err := invoke(repo, `{"base":1,"ops":[{"op":"reply","thread":"t2","body":"x"}]}`, "board", "apply", "arch", "--as", "user"); err != nil {
		t.Fatal(err)
	}
}

func TestBoardImportAndInspect(t *testing.T) {
	repo := testRepo(t)
	if _, err := invoke(repo, "", "board", "create", "b", "--as", "user"); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(t.TempDir(), "x.gif")
	// A 1x1 GIF.
	gif := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")
	if err := os.WriteFile(img, gif, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(repo, "", "board", "import-image", "b", img, "--caption", "dot", "--as", "user"); err != nil {
		t.Fatal(err)
	}
	got, err := invoke(repo, "", "board", "inspect", "b")
	var in board.Inspection
	if err != nil || json.Unmarshal([]byte(got), &in) != nil || len(in.Nodes) != 1 || in.Nodes[0].Caption != "dot" {
		t.Fatalf("inspect: %s, %v", got, err)
	}
	if _, err := os.Stat(*in.Nodes[0].Asset); err != nil {
		t.Fatalf("asset path: %v", err)
	}
	_, err = invoke(repo, "", "board", "inspect", "missing")
	boardErr(t, err, board.KindNotFound)
	if out, err := invoke(repo, "", "board", "inspect", "b", "--format", "mermaid"); err != nil || !strings.Contains(out, "\nflowchart TB\n") {
		t.Fatalf("mermaid: %s, %v", out, err)
	}
	// A part of a board would draw its outside edge ends as cards.
	_, err = invoke(repo, "", "board", "inspect", "b", "--frame", "f1", "--format", "mermaid")
	boardErr(t, err, board.KindInvalid)
	_, err = invoke(repo, "", "board", "inspect", "b", "--format", "svg")
	boardErr(t, err, board.KindInvalid)
	if out, err := invoke(repo, "", "board", "schema"); err != nil || !strings.Contains(out, `"cliPack"`) {
		t.Fatalf("schema: %v", err)
	}
}

func TestStoreBoardsWithoutRoomsGetTheirCheckout(t *testing.T) {
	repo := testRepo(t)
	if _, err := invoke(repo, "", "board", "create", "notes", "--as", "user"); err != nil {
		t.Fatal(err)
	}
	checkouts, err := boardCheckouts(nil)
	if err != nil {
		t.Fatal(err)
	}
	root, err := repoRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkouts) != 1 || checkouts[0].Path != root {
		t.Fatalf("a store with a board and no room has no checkout: %+v", checkouts)
	}
}

// tree lists every path under root with its size and mtime.
func tree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %d %d\n", p, st.Size(), st.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestCatalogLeavesPeerHomeUntouched(t *testing.T) {
	repo := testRepo(t)
	v := startRoom(t, repo, "plan")
	if _, err := invoke(repo, "", "board", "create", "notes", "--as", "main", "--room", v.ID); err != nil {
		t.Fatal(err)
	}
	other := testRepo(t) // a workspace whose store does not exist yet
	t.Setenv("PEER_HOME", filepath.Dir(filepath.Dir(mustStore(t, repo).dir)))
	before := tree(t, os.Getenv("PEER_HOME"))

	srv := boardhttp.New(boardhttp.Config{Port: 4747, Checkouts: boardCheckouts, SPA: fstest.MapFS{}})
	srv.Register(repo)
	srv.Register(other)
	r := httptest.NewRequest(http.MethodGet, "/api/catalog", nil)
	r.Host = "127.0.0.1:4747"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	var c boardhttp.Catalog
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil || w.Code != http.StatusOK || len(c.Groups) != 2 {
		t.Fatalf("catalog: %d %s", w.Code, w.Body)
	}
	if after := tree(t, os.Getenv("PEER_HOME")); after != before {
		t.Fatalf("GET /api/catalog changed PEER_HOME:\n%s\nwant\n%s", after, before)
	}
}

func mustStore(t *testing.T, repo string) *store {
	t.Helper()
	s, err := openStore(repo)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
