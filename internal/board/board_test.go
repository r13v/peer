package board

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

var (
	agent = Author{Role: "reader", RoomID: "r"}
	human = Author{Role: "user"}
)

func newBoard(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "x.board")
	if _, err := Create(dir, "X", ""); err != nil {
		t.Fatal(err)
	}
	dir, err := Canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func pack(t *testing.T, src string) Pack {
	t.Helper()
	p, err := ParsePack([]byte(src))
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}
	return p
}

func apply(t *testing.T, dir string, mode Mode, src string) ApplyResult {
	t.Helper()
	author := agent
	if mode == Browser {
		author = human
	}
	out, err := ApplyPack(dir, pack(t, src), author, mode)
	if err != nil {
		t.Fatalf("apply %s: %v", src, err)
	}
	return out.Result
}

func applyErr(t *testing.T, dir string, mode Mode, src string) *Error {
	t.Helper()
	_, err := ApplyPack(dir, pack(t, src), human, mode)
	var be *Error
	if !errors.As(err, &be) {
		t.Fatalf("apply %s: want a board error, got %v", src, err)
	}
	return be
}

func read(t *testing.T, dir string) (*Board, []byte) {
	t.Helper()
	b, data, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	return b, data
}

func findNode(b *Board, id string) *Node {
	for _, n := range b.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestEditsOfDifferentNodesBothSucceed(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, CLI, `{"base":0,"ops":[{"op":"add","node":{"type":"card","text":"a"}},{"op":"add","node":{"type":"card","text":"b"}}]}`)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, src := range []string{
		`{"base":1,"ops":[{"op":"update","id":"n1","set":{"text":"agent"}}]}`,
		`{"base":1,"ops":[{"op":"update","id":"n2","set":{"x":10,"y":20}}]}`,
	} {
		wg.Go(func() {
			mode := Mode(i) // the agent and the browser
			_, errs[i] = ApplyPack(dir, pack(t, src), human, mode)
		})
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("concurrent edits of different nodes: %v, %v", errs[0], errs[1])
	}
	b, _ := read(t, dir)
	if b.Revision != 3 || *findNode(b, "n1").Text != "agent" || *findNode(b, "n2").X != 10 {
		t.Fatalf("lost an edit: revision %d", b.Revision)
	}
}

func TestSameNodeConflicts(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, CLI, `{"base":0,"ops":[{"op":"add","node":{"type":"card","text":"a"}}]}`)
	apply(t, dir, Browser, `{"base":1,"ops":[{"op":"update","id":"n1","set":{"text":"human"}}]}`)
	be := applyErr(t, dir, CLI, `{"base":1,"ops":[{"op":"update","id":"n1","set":{"text":"agent"}}]}`)
	if be.Kind != KindConflict || len(be.Conflicts) != 1 || be.Conflicts[0].ID != "n1" || *be.Conflicts[0].Rev != 2 || *be.Base != 1 {
		t.Fatalf("want conflict on n1 at rev 2, got %+v", be)
	}
	if b, _ := read(t, dir); *findNode(b, "n1").Text != "human" {
		t.Fatal("the first edit was not kept")
	}
	be = applyErr(t, dir, CLI, `{"base":2,"ops":[{"op":"update","id":"n9","set":{"text":"x"}}]}`)
	if be.Kind != KindConflict || be.Conflicts[0].Rev != nil {
		t.Fatalf("a missing id must conflict with rev null: %+v", be)
	}
}

func TestDeleteNodeOrphansThreadsAndRestoreBringsBack(t *testing.T) {
	dir := newBoard(t)
	r := apply(t, dir, CLI, `{"base":0,"ops":[
		{"op":"add","ref":"$a","node":{"type":"card","text":"Warm\nfull"}},
		{"op":"add","ref":"$b","node":{"type":"card","text":"b"}},
		{"op":"add","ref":"$e","edge":{"from":"$a","to":"$b","label":"to"}},
		{"op":"comment","ref":"$t","target":{"kind":"node","id":"$a"},"body":"why?"}]}`)
	if r.Refs["$a"] != "n1" || r.Refs["$e"] != "e3" || r.Refs["$t"] != "t4" || !slices.Equal(r.Created, []string{"n1", "n2", "e3", "t4"}) {
		t.Fatalf("refs %v created %v", r.Refs, r.Created)
	}
	before, _ := read(t, dir)
	node, edge := *findNode(before, "n1"), *before.Edges[0]

	r = apply(t, dir, Browser, `{"expect":{"n1":1,"e3":1,"t4":1},"ops":[{"op":"delete","id":"n1"}]}`)
	if !slices.Equal(r.Deleted, []string{"n1", "e3"}) || !slices.Equal(r.Changed, []string{"t4"}) {
		t.Fatalf("deleted %v changed %v", r.Deleted, r.Changed)
	}
	b, _ := read(t, dir)
	if len(b.Edges) != 0 || !b.Threads[0].Orphaned || b.Threads[0].TargetLabel != "Warm" || b.Threads[0].Rev != 2 {
		t.Fatalf("delete left edges %v or thread %+v", b.Edges, b.Threads[0])
	}

	snap, _ := json.Marshal(map[string]any{"expect": map[string]int64{"n1": 2, "e3": 2, "t4": 2}, "ops": []any{
		map[string]any{"op": "restore", "node": node}, map[string]any{"op": "restore", "edge": edge}}})
	r = apply(t, dir, Browser, string(snap))
	b, _ = read(t, dir)
	if findNode(b, "n1") == nil || len(b.Edges) != 1 || b.Edges[0].ID != "e3" || b.Threads[0].Orphaned || len(b.Deleted) != 0 {
		t.Fatalf("restore did not bring back n1, e3 and the thread: %+v", b)
	}
	if !slices.Equal(r.Changed, []string{"n1", "t4", "e3"}) {
		t.Fatalf("changed %v", r.Changed)
	}
	if be := applyErr(t, dir, Browser, string(snap)); be.Kind != KindConflict {
		t.Fatalf("a second restore must conflict, got %v", be)
	}
}

func TestRestoreConflicts(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, CLI, `{"base":0,"ops":[{"op":"add","node":{"type":"card","text":"a"}},{"op":"comment","target":{"kind":"node","id":"n1"},"body":"q"}]}`)
	b, _ := read(t, dir)
	node := *findNode(b, "n1")
	apply(t, dir, Browser, `{"expect":{"n1":1,"t2":1},"ops":[{"op":"delete","id":"n1"}]}`)
	restore := func(expect map[string]int64) string {
		data, _ := json.Marshal(map[string]any{"expect": expect, "ops": []any{map[string]any{"op": "restore", "node": node}}})
		return string(data)
	}
	// A wrong expect on a tombstone reports the tombstone's rev, not null.
	be := applyErr(t, dir, Browser, restore(map[string]int64{"n1": 1, "t2": 2}))
	if be.Kind != KindConflict || be.Conflicts[0].ID != "n1" || be.Conflicts[0].Rev == nil || *be.Conflicts[0].Rev != 2 {
		t.Fatalf("want n1 at tombstone rev 2, got %+v", be)
	}
	// A reply to the orphaned thread since the delete blocks the undo.
	apply(t, dir, CLI, `{"base":2,"ops":[{"op":"reply","thread":"t2","body":"still here"}]}`)
	be = applyErr(t, dir, Browser, restore(map[string]int64{"n1": 2, "t2": 2}))
	if be.Kind != KindConflict || be.Conflicts[0].ID != "t2" || *be.Conflicts[0].Rev != 3 {
		t.Fatalf("want t2 conflict, got %+v", be)
	}
}

func TestPackRejectsIncompleteOrNull(t *testing.T) {
	for _, src := range []string{
		`{"base":0,"ops":[{"op":"restore","node":{"id":"n1","type":"card","text":"a"}}]}`,
		`{"base":0,"ops":[{"op":"restore","thread":{"id":"t1","rev":1,"target":{"kind":"board"},"resolved":false,"messages":[]}}]}`,
		`{"base":0,"ops":[{"op":"restore","edge":{"id":"e1","rev":1,"from":"n1","to":null}}]}`,
		`{"base":0,"ops":[{"op":"resolve","thread":"t1","resolved":null}]}`,
		`{"base":0,"ops":[{"op":"reply","thread":"t1","body":null}]}`,
		`{"base":0,"ops":[{"op":"add","node":{"text":"a"}}]}`,
		`{"base":0,"ops":[{"op":"add","edge":{"from":"n1"}}]}`,
		`{"base":0,"ops":[{"op":"comment","target":{"id":"n1"},"body":"x"}]}`,
		`{"expect":{"n1":null},"ops":[{"op":"delete","id":"n1"}]}`,
		`{"base":0,"ops":[{"op":"add","node":{"type":"card","text":"a","caption":""}}]}`,
	} {
		if _, err := ParsePack([]byte(src)); err == nil || AsError(err).Kind != KindInvalid {
			t.Fatalf("%s: want invalid, got %v", src, err)
		}
	}
}

func TestCLIForbidsNullGeometryKeys(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, CLI, `{"base":0,"ops":[{"op":"add","node":{"type":"card","text":"a"}}]}`)
	for _, src := range []string{
		`{"base":1,"ops":[{"op":"update","id":"n1","set":{"x":null}}]}`,
	} {
		if be := applyErr(t, dir, CLI, src); be.Kind != KindForbidden {
			t.Fatalf("%s: want forbidden, got %v", src, be)
		}
	}
	// A null field is rejected for any author when the pack is parsed.
	if _, err := ParsePack([]byte(`{"base":1,"ops":[{"op":"add","node":{"type":"card","text":"a","x":null,"y":null}}]}`)); err == nil {
		t.Fatal("add with x: null accepted")
	}
}

func TestDeleteThreadAndRestore(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, Browser, `{"base":0,"ops":[{"op":"comment","target":{"kind":"board"},"body":"hi"}]}`)
	b, _ := read(t, dir)
	th := b.Threads[0]
	r := apply(t, dir, Browser, `{"expect":{"t1":1},"ops":[{"op":"delete","id":"t1"}]}`)
	if !slices.Equal(r.Deleted, []string{"t1"}) {
		t.Fatalf("deleted %v", r.Deleted)
	}
	snap, _ := json.Marshal(map[string]any{"expect": map[string]int64{"t1": 2}, "ops": []any{map[string]any{"op": "restore", "thread": th}}})
	apply(t, dir, Browser, string(snap))
	if b, _ := read(t, dir); len(b.Threads) != 1 || b.Threads[0].Messages[0].Body != "hi" || b.Threads[0].Orphaned {
		t.Fatalf("thread not restored: %+v", b.Threads)
	}
}

func TestUndoChain(t *testing.T) {
	for _, interleave := range []bool{false, true} {
		dir := newBoard(t)
		apply(t, dir, CLI, `{"base":0,"ops":[{"op":"add","node":{"type":"card","text":"x"}}]}`)
		apply(t, dir, Browser, `{"expect":{"n1":1},"ops":[{"op":"update","id":"n1","set":{"text":"a"}}]}`)
		apply(t, dir, Browser, `{"expect":{"n1":2},"ops":[{"op":"update","id":"n1","set":{"text":"b"}}]}`)
		// Undo 1; the tab rewrites the next step's expect to the new rev.
		r := apply(t, dir, Browser, `{"expect":{"n1":3},"ops":[{"op":"update","id":"n1","set":{"text":"a"}}]}`)
		if r.Revision != 4 {
			t.Fatalf("undo 1 revision %d", r.Revision)
		}
		if interleave {
			apply(t, dir, CLI, `{"base":4,"ops":[{"op":"update","id":"n1","set":{"text":"agent"}}]}`)
		}
		_, err := ApplyPack(dir, pack(t, `{"expect":{"n1":4},"ops":[{"op":"update","id":"n1","set":{"text":"x"}}]}`), human, Browser)
		var be *Error
		if conflicted := errors.As(err, &be) && be.Kind == KindConflict; conflicted != interleave {
			t.Fatalf("interleaved %v: undo 2 gave %v", interleave, err)
		}
	}
}

func TestUngroupKeepsPositions(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, Browser, `{"base":0,"ops":[
		{"op":"add","ref":"$outer","node":{"type":"frame","title":"O","order":1,"x":100,"y":100}},
		{"op":"add","ref":"$inner","node":{"type":"frame","title":"I","order":2,"parentId":"$outer","x":10,"y":20}},
		{"op":"add","node":{"type":"card","text":"placed","parentId":"$inner","x":5,"y":5,"w":50}},
		{"op":"add","node":{"type":"card","text":"unplaced","parentId":"$inner"}},
		{"op":"add","node":{"type":"card","text":"top"}},
		{"op":"add","edge":{"from":"f2","to":"n5"}}]}`)
	r := apply(t, dir, Browser, `{"expect":{"f2":1,"n3":1,"n4":1,"e6":1},"ops":[{"op":"delete","id":"f2"}]}`)
	if !slices.Equal(r.Deleted, []string{"f2", "e6"}) || !slices.Equal(r.Changed, []string{"n3", "n4"}) {
		t.Fatalf("deleted %v changed %v", r.Deleted, r.Changed)
	}
	b, _ := read(t, dir)
	placed, unplaced := findNode(b, "n3"), findNode(b, "n4")
	if placed.ParentID != "f1" || *placed.X != 15 || *placed.Y != 25 || *placed.W != 50 {
		t.Fatalf("placed child moved: %+v", placed)
	}
	if unplaced.ParentID != "f1" || unplaced.X != nil {
		t.Fatalf("unplaced child: %+v", unplaced)
	}
	// A base pack that ungroups a frame whose child changed after base conflicts.
	apply(t, dir, Browser, `{"base":2,"ops":[{"op":"update","id":"n3","set":{"z":3}}]}`)
	if be := applyErr(t, dir, Browser, `{"base":2,"ops":[{"op":"delete","id":"f1"}]}`); be.Kind != KindConflict || be.Conflicts[0].ID != "n3" {
		t.Fatalf("ungroup must be affected by its children: %+v", be)
	}
	r = apply(t, dir, Browser, `{"base":3,"ops":[{"op":"delete","id":"f1","cascade":true}]}`)
	if b, _ := read(t, dir); len(b.Nodes) != 1 || !slices.Equal(r.Deleted, []string{"f1", "n3", "n4"}) {
		t.Fatalf("cascade left %d nodes, deleted %v", len(b.Nodes), r.Deleted)
	}
}

func TestAddingToFrameDoesNotConflict(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, CLI, `{"base":0,"ops":[{"op":"add","node":{"type":"frame","title":"F","order":1}}]}`)
	apply(t, dir, CLI, `{"base":1,"ops":[{"op":"add","node":{"type":"card","text":"a","parentId":"f1"}}]}`)
	apply(t, dir, Browser, `{"base":1,"ops":[{"op":"add","node":{"type":"card","text":"b","parentId":"f1"}}]}`)
}

func TestFailedPackChangesNothing(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, CLI, `{"base":0,"ops":[{"op":"add","node":{"type":"card","text":"a"}}]}`)
	_, before := read(t, dir)
	for _, src := range []string{
		`{"base":1,"ops":[{"op":"add","node":{"type":"card","text":"b"}},{"op":"update","id":"n1","set":{"asset":"x"}}]}`,
		`{"base":1,"ops":[{"op":"update","id":"n1","set":{"text":"b"}},{"op":"add","node":{"type":"card","text":"c","parentId":"n1"}}]}`,
		`{"base":1,"ops":[{"op":"add","node":{"type":"card","text":"b","color":"pink"}}]}`,
		`{"base":1,"ops":[{"op":"delete","id":"n1"},{"op":"update","id":"n1","set":{"text":"b"}}]}`,
	} {
		if be := applyErr(t, dir, CLI, src); be.Kind != KindInvalid {
			t.Fatalf("%s: want invalid, got %v", src, be)
		}
	}
	if _, after := read(t, dir); !bytes.Equal(before, after) {
		t.Fatal("a failed pack changed board.json")
	}
}

func TestCLIGeometryAndRestoreForbidden(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, CLI, `{"base":0,"ops":[{"op":"add","node":{"type":"frame","title":"F","order":1}}]}`)
	apply(t, dir, CLI, `{"base":1,"ops":[{"op":"delete","id":"f1"}]}`)
	for _, src := range []string{
		`{"base":2,"ops":[{"op":"add","node":{"type":"card","text":"a","x":1,"y":2}}]}`,
		`{"base":2,"ops":[{"op":"restore","node":{"id":"f1","type":"frame","rev":1,"title":"F","order":1}}]}`,
	} {
		if be := applyErr(t, dir, CLI, src); be.Kind != KindForbidden {
			t.Fatalf("CLI %s: want forbidden, got %v", src, be)
		}
		if _, err := ApplyPack(dir, pack(t, src), human, Browser); err != nil {
			t.Fatalf("browser %s: %v", src, err)
		}
	}
	if be := applyErr(t, dir, CLI, `{"base":4,"ops":[{"op":"update","id":"n2","set":{"w":5}}]}`); be.Kind != KindForbidden {
		t.Fatalf("CLI update w: %v", be)
	}
	apply(t, dir, Browser, `{"base":4,"ops":[{"op":"update","id":"n2","set":{"w":5}}]}`)
}

func TestCLIReparentResetsPosition(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, Browser, `{"base":0,"ops":[{"op":"add","node":{"type":"frame","title":"F","order":1}},{"op":"add","node":{"type":"card","text":"a","x":1,"y":2,"w":30}}]}`)
	apply(t, dir, CLI, `{"base":1,"ops":[{"op":"update","id":"n2","set":{"parentId":"f1"}}]}`)
	b, _ := read(t, dir)
	if n := findNode(b, "n2"); n.ParentID != "f1" || n.X != nil || n.Y != nil || *n.W != 30 {
		t.Fatalf("reparent: %+v", n)
	}
}

func TestInspectHasNoGeometry(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, Browser, `{"base":0,"ops":[
		{"op":"add","node":{"type":"frame","title":"F","order":1,"x":0,"y":0,"w":1600,"h":900,"z":0}},
		{"op":"add","node":{"type":"card","text":"a","parentId":"f1","x":1,"y":2,"w":3,"h":4,"z":5}},
		{"op":"comment","target":{"kind":"frame","id":"f1"},"body":"hi"}]}`)
	if _, err := ImportImage(dir, pngBytes(t, 3, 2), "f1", "chart", agent, CLI); err != nil {
		t.Fatal(err)
	}
	b, _ := read(t, dir)
	for _, v := range []any{Inspect(b, dir), mustFrame(t, b, dir, "f1"), InspectIDs(b, dir, []string{"n2"})} {
		var got struct{ Nodes []map[string]any }
		data, _ := json.Marshal(v)
		_ = json.Unmarshal(data, &got)
		if len(got.Nodes) == 0 {
			t.Fatalf("no nodes in %s", data)
		}
		for _, n := range got.Nodes {
			for _, k := range geometry {
				if _, ok := n[k]; ok {
					t.Fatalf("inspect has %s: %s", k, data)
				}
			}
		}
	}
	o := NewOverview(b, dir)
	if len(o.Frames) != 1 || o.Frames[0].Nodes != 2 || o.Frames[0].OpenThreads != 1 || len(o.Loose) != 0 {
		t.Fatalf("overview %+v", o)
	}
}

func mustFrame(t *testing.T, b *Board, dir, id string) Inspection {
	t.Helper()
	in, err := InspectFrame(b, dir, id)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestImportImageTwiceKeepsOneFile(t *testing.T) {
	dir := newBoard(t)
	img := pngBytes(t, 7, 5)
	a, err := ImportAsset(dir, img)
	if err != nil || a.Width != 7 || a.Height != 5 || !strings.HasSuffix(a.Asset, ".png") {
		t.Fatalf("asset %+v, %v", a, err)
	}
	for range 2 {
		if _, err := ImportImage(dir, img, "", "", agent, CLI); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := os.ReadDir(filepath.Join(dir, "assets"))
	if b, _ := read(t, dir); len(files) != 1 || len(b.Nodes) != 2 {
		t.Fatalf("%d asset files, %d nodes", len(files), len(b.Nodes))
	}
	if _, err := ImportAsset(dir, []byte("not an image")); AsError(err).Kind != KindUnsupportedMedia {
		t.Fatalf("text accepted as image: %v", err)
	}
	if _, err := ImportAsset(dir, make([]byte, MaxAssetSize+1)); AsError(err).Kind != KindTooLarge {
		t.Fatalf("oversized image: %v", err)
	}
}

func TestImportIntoBrokenBoardWritesNothing(t *testing.T) {
	dir := newBoard(t)
	if err := os.WriteFile(filepath.Join(dir, "board.json"), []byte(`{"formatVersion":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportImage(dir, pngBytes(t, 1, 1), "", "", agent, CLI); AsError(err).Kind != KindInvalid {
		t.Fatalf("import into a broken board: %v", err)
	}
	if _, err := ImportAsset(dir, pngBytes(t, 1, 1)); AsError(err).Kind != KindInvalid {
		t.Fatalf("asset into a broken board: %v", err)
	}
	if files, _ := os.ReadDir(filepath.Join(dir, "assets")); len(files) != 0 {
		t.Fatalf("assets written: %v", files)
	}
}

func TestImportRefusesSymlinkedAssets(t *testing.T) {
	dir := newBoard(t)
	outside := t.TempDir()
	if err := os.Remove(filepath.Join(dir, "assets")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "assets")); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportAsset(dir, pngBytes(t, 1, 1)); err == nil {
		t.Fatal("imported through a symlinked assets directory")
	}
	if files, _ := os.ReadDir(outside); len(files) != 0 {
		t.Fatal("wrote outside the board")
	}
}

func TestWebpSize(t *testing.T) {
	riff := func(chunk string, body []byte) []byte {
		data := append([]byte("RIFF\x00\x00\x00\x00WEBP"+chunk+"\x00\x00\x00\x00"), body...)
		return append(data, make([]byte, 16)...)
	}
	// VP8X: flags and reserved, then width-1 = 639 and height-1 = 479 as 24-bit.
	vp8x := riff("VP8X", []byte{0, 0, 0, 0, 0x7f, 0x02, 0, 0xdf, 0x01, 0})
	// VP8L: signature, then 14-bit width-1 = 99 and height-1 = 49.
	bits := uint32(99) | uint32(49)<<14
	vp8l := riff("VP8L", []byte{0x2f, byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24)})
	// VP8: frame tag, start code, 16-bit width and height.
	vp8 := riff("VP8 ", []byte{0, 0, 0, 0x9d, 0x01, 0x2a, 32, 0, 16, 0})
	for _, c := range []struct {
		data []byte
		w, h int
	}{{vp8x, 640, 480}, {vp8l, 100, 50}, {vp8, 32, 16}} {
		ext, w, h, err := sniff(c.data)
		if err != nil || ext != "webp" || w != c.w || h != c.h {
			t.Fatalf("webp: %s %dx%d %v, want %dx%d", ext, w, h, err, c.w, c.h)
		}
	}
}

func TestMovedBoardGivesNewAssetPaths(t *testing.T) {
	dir := newBoard(t)
	if _, err := ImportImage(dir, pngBytes(t, 2, 2), "", "c", agent, CLI); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "moved.board")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	moved, _ = Canonical(moved)
	b, _ := read(t, moved)
	asset := *Inspect(b, moved).Nodes[0].Asset
	if !strings.HasPrefix(asset, moved+string(filepath.Separator)) {
		t.Fatalf("asset %s is not under %s", asset, moved)
	}
	if _, err := os.Stat(asset); err != nil {
		t.Fatal(err)
	}
	if NewRef(moved).Key == Key(dir) {
		t.Fatal("moved board kept its old key")
	}
}

func TestReserializeIsByteIdentical(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, Browser, `{"base":0,"ops":[
		{"op":"add","node":{"type":"frame","title":"<F & G>","order":1.5,"x":0.25,"y":-3}},
		{"op":"add","node":{"type":"card","text":"a\n\"b\"","color":"green","parentId":"f1"}},
		{"op":"add","edge":{"from":"f1","to":"n2","label":"l"}},
		{"op":"comment","target":{"kind":"edge","id":"e3"},"body":"ü"}]}`)
	b, before := read(t, dir)
	after, err := Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !bytes.HasSuffix(after, []byte("}\n")) || !bytes.Contains(after, []byte("\n\t\"nodes\"")) {
		t.Fatalf("re-serialized bytes differ:\n%s\n%s", before, after)
	}
	if !bytes.Contains(after, []byte("<F & G>")) {
		t.Fatal("HTML characters were escaped")
	}
}

func TestReadRejectsBrokenBoards(t *testing.T) {
	for name, c := range map[string]struct {
		json string
		kind string
		path string
	}{
		"incomplete": {`{"formatVersion":1,"id":"b_a","nextId":1}`, KindInvalid, "title"},
		"null nodes": {`{"formatVersion":1,"id":"b_a","title":"","revision":0,"nodes":null,"edges":[],"threads":[],"deleted":[],"nextId":1}`, KindInvalid, "nodes"},
		"node rev":   {`{"formatVersion":1,"id":"b_a","title":"","revision":1,"nodes":[{"id":"n1","type":"card","text":""}],"edges":[],"threads":[],"deleted":[],"nextId":2}`, KindInvalid, "nodes[0].rev"},
		"newer":      {`{"formatVersion":2}`, KindUnsupportedVersion, ""},
		"x only":     {`{"formatVersion":1,"id":"b_a","title":"","revision":1,"nodes":[{"id":"n1","type":"card","rev":1,"text":"","x":1}],"edges":[],"threads":[],"deleted":[],"nextId":2}`, KindInvalid, "nodes[0]"},
		"id range":   {`{"formatVersion":1,"id":"b_a","title":"","revision":1,"nodes":[{"id":"n1","type":"card","rev":1,"text":""}],"edges":[],"threads":[],"deleted":[],"nextId":1}`, KindInvalid, "nodes[0].id"},
		"cycle":      {`{"formatVersion":1,"id":"b_a","title":"","revision":1,"nodes":[{"id":"f1","type":"frame","rev":1,"parentId":"f2","title":"","order":1},{"id":"f2","type":"frame","rev":1,"parentId":"f1","title":"","order":2}],"edges":[],"threads":[],"deleted":[],"nextId":3}`, KindInvalid, "nodes[0].parentId"},
		"edge":       {`{"formatVersion":1,"id":"b_a","title":"","revision":1,"nodes":[],"edges":[{"id":"e1","rev":1,"from":"n5","to":"n6"}],"threads":[],"deleted":[],"nextId":2}`, KindInvalid, "edges[0].from"},
		"asset":      {`{"formatVersion":1,"id":"b_a","title":"","revision":1,"nodes":[{"id":"n1","type":"image","rev":1,"asset":"assets/../../etc/passwd"}],"edges":[],"threads":[],"deleted":[],"nextId":2}`, KindInvalid, "nodes[0].asset"},
		"empty key":  {`{"formatVersion":1,"id":"b_a","title":"","revision":1,"nodes":[{"id":"n1","type":"card","rev":1,"text":"","caption":""}],"edges":[],"threads":[],"deleted":[],"nextId":2}`, KindInvalid, "nodes[0].caption"},
		"unknown":    {`{"formatVersion":1,"id":"b_a","title":"","revision":0,"nodes":[],"edges":[],"threads":[],"deleted":[],"nextId":1,"extra":1}`, KindInvalid, ""},
	} {
		dir := newBoard(t)
		if err := os.WriteFile(filepath.Join(dir, "board.json"), []byte(c.json), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := Read(dir)
		be := AsError(err)
		if err == nil || be.Kind != c.kind {
			t.Fatalf("%s: want %s, got %v", name, c.kind, err)
		}
		if c.kind == KindInvalid && !slices.ContainsFunc(be.Problems, func(p Problem) bool { return p.Path == c.path }) {
			t.Fatalf("%s: no problem at %q: %+v", name, c.path, be.Problems)
		}
		if _, err := ApplyPack(dir, pack(t, `{"base":0,"ops":[{"op":"comment","target":{"kind":"board"},"body":"x"}]}`), human, Browser); err == nil {
			t.Fatalf("%s: wrote to a broken board", name)
		}
	}
}

func TestKeyRoundTrip(t *testing.T) {
	dir := newBoard(t)
	got, err := DecodeKey(Key(dir))
	if err != nil || got != dir {
		t.Fatalf("round trip: %s, %v", got, err)
	}
	plain := t.TempDir()
	notBoard := filepath.Join(plain, "x.board")
	if err := os.Mkdir(notBoard, 0o755); err != nil {
		t.Fatal(err)
	}
	notBoard, _ = Canonical(notBoard)
	plain, _ = Canonical(plain)
	for _, key := range []string{"!!", Key("relative.board"), Key(plain), Key(notBoard), Key(dir + "/../x.board"), Key(filepath.Join(plain, "gone.board"))} {
		if _, err := DecodeKey(key); err == nil {
			t.Fatalf("key %s accepted", key)
		}
	}
	if !strings.Contains(NewRef(dir).URL, "/b/"+Key(dir)) || strings.ContainsAny(Key(dir), "+/=") {
		t.Fatal("key is not base64url without padding")
	}
}

func makeBoard(t *testing.T, dir string) {
	t.Helper()
	if _, err := Create(dir, "b", ""); err != nil {
		t.Fatal(err)
	}
}

func TestScanGit(t *testing.T) {
	ws := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", ws).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	makeBoard(t, filepath.Join(ws, "docs", "arch.board"))
	makeBoard(t, filepath.Join(ws, "a", "b", "c", "d", "e", "deep.board")) // git has no depth limit
	makeBoard(t, filepath.Join(ws, "build", "ignored.board"))
	makeBoard(t, filepath.Join(ws, "docs", "arch.board", "inner.board"))
	if err := os.WriteFile(filepath.Join(ws, ".gitignore"), []byte("build/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Scan(ws)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ = Canonical(ws)
	want := []string{filepath.Join(ws, "a", "b", "c", "d", "e", "deep.board"), filepath.Join(ws, "docs", "arch.board")}
	if !slices.Equal(got, want) {
		t.Fatalf("scan %v, want %v", got, want)
	}
	if got, _ := Scan(filepath.Join(ws, "a")); len(got) != 1 {
		t.Fatalf("scan of a subdirectory: %v", got)
	}
}

func TestScanWalk(t *testing.T) {
	ws := t.TempDir()
	makeBoard(t, filepath.Join(ws, "x.board"))
	makeBoard(t, filepath.Join(ws, "a", "b", "c", "four.board"))
	makeBoard(t, filepath.Join(ws, "a", "b", "c", "d", "five.board"))
	makeBoard(t, filepath.Join(ws, "node_modules", "n.board"))
	makeBoard(t, filepath.Join(ws, "vendor", "v.board"))
	makeBoard(t, filepath.Join(ws, ".hidden", "h.board"))
	makeBoard(t, filepath.Join(ws, "x.board", "nested.board"))
	other := t.TempDir()
	makeBoard(t, filepath.Join(other, "linked.board"))
	if err := os.Symlink(other, filepath.Join(ws, "link")); err != nil {
		t.Fatal(err)
	}
	got, err := Scan(ws)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ = Canonical(ws)
	want := []string{filepath.Join(ws, "a", "b", "c", "four.board"), filepath.Join(ws, "x.board")}
	if !slices.Equal(got, want) {
		t.Fatalf("scan %v, want %v", got, want)
	}
}

func TestSchemasAreJSON(t *testing.T) {
	s, err := LoadSchemas()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{s.Board, s.CLIPack, s.BrowserPack} {
		var v map[string]any
		if err := json.Unmarshal(raw, &v); err != nil || v["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
			t.Fatalf("schema: %v", err)
		}
	}
	if bytes.Contains(s.CLIPack, []byte(`"restore"`)) || !bytes.Contains(s.BrowserPack, []byte(`"restore"`)) {
		t.Fatal("restore must be browser only")
	}
}

func TestRemovedFramePresetStillOpens(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, Browser, `{"base":0,"ops":[{"op":"add","node":{"type":"frame","title":"F","order":1,"x":0,"y":0,"w":960,"h":540}},{"op":"add","node":{"type":"card","text":"a"}}]}`)
	_, data := read(t, dir)
	old := strings.Replace(string(data), `"order": 1,`, `"order": 1,`+"\n\t\t\t"+`"preset": "slide",`, 1)
	if old == string(data) {
		t.Fatal("no frame to add a preset to")
	}
	b, err := Parse([]byte(old), dir)
	if err != nil {
		t.Fatalf("a board with a frame preset does not open: %v", err)
	}
	out, err := Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("the next write keeps the preset:\n%s", out)
	}
	// Only frames had a preset; on a card it stays an error.
	card := strings.Replace(string(data), `"text": "a"`, `"text": "a", "preset": "slide"`, 1)
	if _, err := Parse([]byte(card), dir); err == nil {
		t.Fatal("a card with a preset opens")
	}
}
