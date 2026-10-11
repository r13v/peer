package boardhttp

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/r13v/peer/internal/board"
)

// Catalog is the contract's list of boards for the home page.
type Catalog struct {
	Workspaces []string    `json:"workspaces"`
	Groups     []Group     `json:"groups"`
	Outside    []board.Ref `json:"outside"`
}

// Group is one checkout's boards: by room, in its store and found under
// workspaces. A board shows in one section only, rooms first.
type Group struct {
	Checkout CheckoutName `json:"checkout"`
	Rooms    []RoomBoards `json:"rooms"`
	Store    []board.Ref  `json:"store"`
	Found    []board.Ref  `json:"found"`
}

// CheckoutName names a group.
type CheckoutName struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// RoomBoards is a room and the boards it worked with.
type RoomBoards struct {
	ID        string      `json:"id"`
	Active    bool        `json:"active"`
	StartedAt string      `json:"startedAt"`
	EndedAt   string      `json:"endedAt,omitempty"`
	Boards    []board.Ref `json:"boards"`
}

// Register adds a workspace and scans it once; a known one is left as
// is, so repeated registration is cheap.
func (s *Server) Register(path string) {
	s.mu.Lock()
	known := slices.Contains(s.workspaces, path)
	if !known {
		s.workspaces = append(s.workspaces, path)
	}
	s.mu.Unlock()

	if !known {
		s.scan(path)
	}
}

// scan replaces a workspace's cached scan; a failed scan finds nothing.
func (s *Server) scan(workspace string) {
	found, _ := board.Scan(workspace)
	s.mu.Lock()
	s.found[workspace] = found
	s.mu.Unlock()
}

// Catalog groups the boards of rooms, stores and scans by checkout.
func (s *Server) Catalog() (Catalog, error) {
	s.mu.Lock()
	workspaces := slices.Clone(s.workspaces)
	var found []string
	for _, ws := range workspaces {
		for _, p := range s.found[ws] {
			if !slices.Contains(found, p) {
				found = append(found, p)
			}
		}
	}
	s.mu.Unlock()

	checkouts, err := s.cfg.Checkouts(workspaces)
	if err != nil {
		return Catalog{}, err
	}
	c := Catalog{Workspaces: workspaces, Groups: []Group{}, Outside: []board.Ref{}}
	if c.Workspaces == nil {
		c.Workspaces = []string{}
	}
	for _, co := range checkouts {
		var mine []string
		for _, p := range found {
			if owner(p, checkouts) == co.Path {
				mine = append(mine, p)
			}
		}
		c.Groups = append(c.Groups, group(co, mine))
	}
	for _, p := range found {
		if owner(p, checkouts) == "" {
			c.Outside = append(c.Outside, board.NewRef(p))
		}
	}

	return c, nil
}

func group(co Checkout, found []string) Group {
	links := map[string][]string{} // board path -> ids of rooms that link it
	for _, r := range co.Rooms {
		for _, p := range r.Boards {
			if !slices.Contains(links[p], r.ID) {
				links[p] = append(links[p], r.ID)
			}
		}
	}
	ref := func(p string) board.Ref {
		r := board.NewRef(p)
		r.Rooms = links[p]
		return r
	}

	g := Group{Checkout: CheckoutName{Path: co.Path, Name: filepath.Base(co.Path)}, Rooms: []RoomBoards{}, Store: []board.Ref{}, Found: []board.Ref{}}
	rooms := slices.Clone(co.Rooms)
	sort.SliceStable(rooms, func(i, j int) bool { return rooms[i].StartedAt > rooms[j].StartedAt })
	for _, r := range rooms {
		rb := RoomBoards{ID: r.ID, Active: r.Active, StartedAt: r.StartedAt, EndedAt: r.EndedAt, Boards: []board.Ref{}}
		for _, p := range r.Boards {
			rb.Boards = append(rb.Boards, ref(p))
		}
		g.Rooms = append(g.Rooms, rb)
	}
	shown := map[string]bool{}
	for p := range links {
		shown[p] = true
	}
	for _, p := range storeBoards(co.Store) {
		if !shown[p] {
			shown[p] = true
			g.Store = append(g.Store, ref(p))
		}
	}
	for _, p := range found {
		if !shown[p] {
			shown[p] = true
			g.Found = append(g.Found, ref(p))
		}
	}

	return g
}

// storeBoards lists the store's boards, newest board.json first.
func storeBoards(store string) []string {
	dirs, _ := filepath.Glob(filepath.Join(store, "boards", "*.board"))
	var paths []string
	for _, d := range dirs {
		if c, err := board.Canonical(d); err == nil {
			paths = append(paths, c)
		}
	}
	sort.SliceStable(paths, func(i, j int) bool { return modTime(paths[i]) > modTime(paths[j]) })

	return paths
}

func modTime(dir string) int64 {
	st, err := os.Stat(filepath.Join(dir, "board.json"))
	if err != nil {
		return 0
	}

	return st.ModTime().UnixNano()
}

// owner is the innermost checkout that contains path, or "".
func owner(path string, checkouts []Checkout) string {
	best := ""
	for _, co := range checkouts {
		inside := path == co.Path || strings.HasPrefix(path, co.Path+string(filepath.Separator))
		if inside && len(co.Path) > len(best) {
			best = co.Path
		}
	}

	return best
}
