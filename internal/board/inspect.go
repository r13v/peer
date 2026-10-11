package board

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"time"
)

// Ref is the contract's BoardRef.
type Ref struct {
	Key      string   `json:"key"`
	Path     string   `json:"path"`
	URL      string   `json:"url"`
	ID       string   `json:"id,omitempty"`
	Title    string   `json:"title,omitempty"`
	Revision *int64   `json:"revision,omitempty"`
	Rooms    []string `json:"rooms,omitempty"`
	Mtime    string   `json:"mtime,omitempty"`
	Error    *Error   `json:"error,omitempty"`
}

// Header names the board in every inspect answer; its revision is the
// base for the next pack.
type Header struct {
	Key      string `json:"key"`
	Path     string `json:"path"`
	URL      string `json:"url"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	RoomID   string `json:"roomId,omitempty"`
	Revision int64  `json:"revision"`
}

// Inspection is the semantic projection: the board without geometry.
type Inspection struct {
	Board   Header    `json:"board"`
	Nodes   []*Node   `json:"nodes"`
	Edges   []*Edge   `json:"edges"`
	Threads []*Thread `json:"threads"`
}

// Overview is the short projection: frames by order, loose nodes and
// board threads.
type Overview struct {
	Board           Header           `json:"board"`
	Frames          []OverviewFrame  `json:"frames"`
	Loose           []LooseNode      `json:"loose"`
	OverviewThreads []OverviewThread `json:"boardThreads"`
}

// OverviewFrame counts a frame's direct children and the open threads on
// the frame and on them.
type OverviewFrame struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Order       float64 `json:"order"`
	ParentID    string  `json:"parentId,omitempty"`
	Nodes       int     `json:"nodes"`
	OpenThreads int     `json:"openThreads"`
}

// LooseNode is a card or image outside any frame.
type LooseNode struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

// OverviewThread is a thread on the board itself.
type OverviewThread struct {
	ID       string `json:"id"`
	Resolved bool   `json:"resolved"`
	Messages int    `json:"messages"`
}

// Canonical is the absolute path with symlinks resolved; keys and room
// links use it, so one board has one key.
func Canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// Key encodes a canonical board path for URLs.
func Key(path string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(path))
}

// DecodeKey returns the board directory of key after checking that it is
// a canonical *.board directory with a valid board.json.
func DecodeKey(key string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil {
		return "", notFound("the board key is not base64url")
	}
	path := string(raw)
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Ext(path) != ".board" {
		return "", notFound("the board key is not an absolute *.board path")
	}
	if resolved, err := filepath.EvalSymlinks(path); err != nil || resolved != path {
		return "", notFound("no board at %s", path)
	}
	if _, _, err := Read(path); err != nil {
		return "", err
	}
	return path, nil
}

// Port is the board server's port: PEER_PORT or 4747.
func Port() int {
	if n, err := strconv.Atoi(os.Getenv("PEER_PORT")); err == nil && n > 0 && n < 65536 {
		return n
	}
	return 4747
}

// URL is the browser link of a board key.
func URL(key string) string {
	return fmt.Sprintf("http://localhost:%d/b/%s", Port(), key)
}

// NewRef describes the board at path; Error is set when it cannot be read.
func NewRef(path string) Ref {
	if c, err := Canonical(path); err == nil {
		path = c
	}
	r := Ref{Key: Key(path), Path: path, URL: URL(Key(path))}
	b, _, err := Read(path)
	if err != nil {
		r.Error = AsError(err)
		return r
	}
	r.ID, r.Title, r.Revision = b.ID, b.Title, &b.Revision
	if st, err := os.Stat(filepath.Join(path, "board.json")); err == nil {
		r.Mtime = st.ModTime().UTC().Format(time.RFC3339)
	}
	return r
}

// NewHeader names board b at dir, which should be canonical.
func NewHeader(b *Board, dir string) Header {
	return Header{Key: Key(dir), Path: dir, URL: URL(Key(dir)), ID: b.ID, Title: b.Title, RoomID: b.RoomID, Revision: b.Revision}
}

// Inspect projects the whole board.
func Inspect(b *Board, dir string) Inspection {
	return project(b, dir, func(*Node) bool { return true }, func(*Edge) bool { return true }, func(*Thread) bool { return true })
}

// InspectFrame keeps frame id, its descendants, the edges that touch them
// and the threads on any of them.
func InspectFrame(b *Board, dir, id string) (Inspection, error) {
	keep := map[string]bool{}
	for _, n := range b.Nodes {
		if n.ID == id && n.Type == TypeFrame {
			keep[id] = true
		}
	}
	if !keep[id] {
		return Inspection{}, invalid("frame", "no frame %s on the board", id)
	}
	for grown := true; grown; {
		grown = false
		for _, n := range b.Nodes {
			if !keep[n.ID] && keep[n.ParentID] {
				keep[n.ID], grown = true, true
			}
		}
	}
	for _, e := range b.Edges {
		if keep[e.From] || keep[e.To] {
			keep[e.ID] = true
		}
	}
	return project(b, dir,
		func(n *Node) bool { return keep[n.ID] },
		func(e *Edge) bool { return keep[e.ID] },
		func(t *Thread) bool { return t.Target.Kind != "board" && keep[t.Target.ID] }), nil
}

// InspectIDs keeps the listed entities and the threads on them.
func InspectIDs(b *Board, dir string, ids []string) Inspection {
	return project(b, dir,
		func(n *Node) bool { return slices.Contains(ids, n.ID) },
		func(e *Edge) bool { return slices.Contains(ids, e.ID) },
		func(t *Thread) bool {
			return slices.Contains(ids, t.ID) || t.Target.Kind != "board" && slices.Contains(ids, t.Target.ID)
		})
}

func project(b *Board, dir string, node func(*Node) bool, edge func(*Edge) bool, thread func(*Thread) bool) Inspection {
	out := Inspection{Board: NewHeader(b, dir), Nodes: []*Node{}, Edges: []*Edge{}, Threads: []*Thread{}}
	for _, n := range b.Nodes {
		if node(n) {
			out.Nodes = append(out.Nodes, semantic(n, dir))
		}
	}
	for _, e := range b.Edges {
		if edge(e) {
			out.Edges = append(out.Edges, e)
		}
	}
	for _, t := range b.Threads {
		if thread(t) {
			out.Threads = append(out.Threads, t)
		}
	}
	return out
}

// semantic drops geometry and makes the asset an absolute path from where
// the board lies now, so a moved board still gives readable paths.
func semantic(n *Node, dir string) *Node {
	s := *n
	s.X, s.Y, s.W, s.H, s.Z, s.Preset = nil, nil, nil, nil, nil, ""
	if n.Asset != nil {
		abs := filepath.Join(dir, filepath.FromSlash(*n.Asset))
		s.Asset = &abs
	}
	return &s
}

// NewOverview builds the overview of b.
func NewOverview(b *Board, dir string) Overview {
	out := Overview{Board: NewHeader(b, dir), Frames: []OverviewFrame{}, Loose: []LooseNode{}, OverviewThreads: []OverviewThread{}}
	open := map[string]int{}
	for _, t := range b.Threads {
		if t.Target.Kind == "board" {
			out.OverviewThreads = append(out.OverviewThreads, OverviewThread{ID: t.ID, Resolved: t.Resolved, Messages: len(t.Messages)})
		} else if !t.Resolved && !t.Orphaned {
			open[t.Target.ID]++
		}
	}
	for _, n := range b.Nodes {
		if n.Type != TypeFrame {
			if n.ParentID == "" {
				out.Loose = append(out.Loose, LooseNode{ID: n.ID, Type: n.Type, Label: n.label()})
			}
			continue
		}
		f := OverviewFrame{ID: n.ID, Title: *n.Title, Order: *n.Order, ParentID: n.ParentID, OpenThreads: open[n.ID]}
		for _, c := range b.Nodes {
			if c.ParentID == n.ID {
				f.Nodes++
				f.OpenThreads += open[c.ID]
			}
		}
		out.Frames = append(out.Frames, f)
	}
	sort.SliceStable(out.Frames, func(i, j int) bool { return out.Frames[i].Order < out.Frames[j].Order })
	return out
}
