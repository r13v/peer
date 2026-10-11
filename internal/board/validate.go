package board

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	// entityID is a kind prefix and a number from nextId.
	entityID  = regexp.MustCompile(`^([fnetm])([1-9][0-9]{0,15})$`)
	boardID   = regexp.MustCompile(`^b_[A-Za-z0-9_-]{1,64}$`)
	assetPath = regexp.MustCompile(`^assets/[0-9a-f]{64}\.(png|jpg|gif|webp)$`)
)

type checker struct{ problems []Problem }

func (c *checker) add(path, format string, args ...any) {
	c.problems = append(c.problems, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
}

// validate checks every invariant of b; dir is the board directory, where
// image assets must exist.
func validate(b *Board, dir string) []Problem {
	c := &checker{}
	if b.FormatVersion != FormatVersion {
		c.add("formatVersion", "must be %d", FormatVersion)
	}
	if !boardID.MatchString(b.ID) {
		c.add("id", "must be b_ followed by letters, digits, _ or -")
	}
	if b.Revision < 0 {
		c.add("revision", "must not be negative")
	}
	if b.NextID < 1 {
		c.add("nextId", "must be at least 1")
	}
	seen := map[string]bool{}
	checkID := func(path, id, kinds string) {
		m := entityID.FindStringSubmatch(id)
		if m == nil || !strings.Contains(kinds, m[1]) {
			c.add(path, "id %q must be one of the prefixes %q followed by a number", id, kinds)
			return
		}
		if n, _ := strconv.ParseInt(m[2], 10, 64); n >= b.NextID {
			c.add(path, "id %q is not below nextId %d", id, b.NextID)
		}
		if seen[id] {
			c.add(path, "duplicate id %q", id)
		}
		seen[id] = true
	}
	checkRev := func(path string, rev int64) {
		if rev < 0 || rev > b.Revision {
			c.add(path, "rev %d is outside 0..%d", rev, b.Revision)
		}
	}

	nodes := map[string]*Node{}
	for i, n := range b.Nodes {
		p := fmt.Sprintf("nodes[%d]", i)
		prefix := "n"
		if n.Type == TypeFrame {
			prefix = "f"
		}
		checkID(p+".id", n.ID, prefix)
		checkRev(p+".rev", n.Rev)
		validateNode(c, p, n, dir)
		nodes[n.ID] = n
	}
	for i, n := range b.Nodes {
		if n.ParentID == "" {
			continue
		}
		p := fmt.Sprintf("nodes[%d].parentId", i)
		if parent := nodes[n.ParentID]; parent == nil || parent.Type != TypeFrame {
			c.add(p, "%q is not a frame on the board", n.ParentID)
			continue
		}
		// A chain longer than the node count has looped.
		steps := 0
		for id := n.ParentID; id != "" && nodes[id] != nil; id = nodes[id].ParentID {
			if id == n.ID || steps > len(nodes) {
				c.add(p, "frames nest in a cycle through %q", n.ID)
				break
			}
			steps++
		}
	}

	edges := map[string]bool{}
	for i, e := range b.Edges {
		p := fmt.Sprintf("edges[%d]", i)
		checkID(p+".id", e.ID, "e")
		checkRev(p+".rev", e.Rev)
		if nodes[e.From] == nil {
			c.add(p+".from", "%q is not a node on the board", e.From)
		}
		if nodes[e.To] == nil {
			c.add(p+".to", "%q is not a node on the board", e.To)
		}
		edges[e.ID] = true
	}

	for i, t := range b.Threads {
		p := fmt.Sprintf("threads[%d]", i)
		checkID(p+".id", t.ID, "t")
		checkRev(p+".rev", t.Rev)
		validateTarget(c, p+".target", t, nodes, edges)
		if len(t.Messages) == 0 {
			c.add(p+".messages", "a thread has at least one message")
		}
		for j, m := range t.Messages {
			mp := fmt.Sprintf("%s.messages[%d]", p, j)
			checkID(mp+".id", m.ID, "m")
			if m.Author.Role == "" {
				c.add(mp+".author.role", "must not be empty")
			}
			if msg := bodyProblem(m.Body); msg != "" {
				c.add(mp+".body", "%s", msg)
			}
			if _, err := time.Parse(time.RFC3339, m.CreatedAt); err != nil {
				c.add(mp+".createdAt", "must be an RFC 3339 time")
			}
		}
	}

	for i, d := range b.Deleted {
		p := fmt.Sprintf("deleted[%d]", i)
		checkID(p+".id", d.ID, "fnet")
		if d.Rev < 1 || d.Rev > b.Revision {
			c.add(p+".rev", "rev %d is outside 1..%d", d.Rev, b.Revision)
		}
	}
	return c.problems
}

func validateNode(c *checker, p string, n *Node, dir string) {
	has := map[string]bool{
		"text": n.Text != nil, "color": n.Color != "", "asset": n.Asset != nil, "caption": n.Caption != "",
		"title": n.Title != nil, "order": n.Order != nil, "preset": n.Preset != "",
	}
	var required, allowed []string
	switch n.Type {
	case TypeCard:
		required, allowed = []string{"text"}, []string{"text", "color"}
	case TypeImage:
		required, allowed = []string{"asset"}, []string{"asset", "caption"}
	case TypeFrame:
		required, allowed = []string{"title", "order"}, []string{"title", "order", "preset"}
	default:
		c.add(p+".type", "must be card, image or frame")
		return
	}
	for _, f := range required {
		if !has[f] {
			c.add(p+"."+f, "a %s needs %s", n.Type, f)
		}
	}
	for _, f := range sortedKeys(has) {
		if has[f] && !slices.Contains(allowed, f) {
			c.add(p+"."+f, "a %s has no %s", n.Type, f)
		}
	}
	if n.Color != "" && !slices.Contains(cardColors, n.Color) {
		c.add(p+".color", "must be one of %s", strings.Join(cardColors, ", "))
	}
	if n.Preset != "" && n.Preset != "slide" {
		c.add(p+".preset", `must be "slide"`)
	}
	if n.Text != nil && !utf8.ValidString(*n.Text) {
		c.add(p+".text", "must be UTF-8")
	}
	if (n.X == nil) != (n.Y == nil) {
		c.add(p, "x and y are both set or both absent")
	}
	if n.Asset != nil {
		if err := checkAsset(dir, *n.Asset); err != "" {
			c.add(p+".asset", "%s", err)
		}
	}
}

func validateTarget(c *checker, p string, t *Thread, nodes map[string]*Node, edges map[string]bool) {
	switch t.Target.Kind {
	case "board":
		if t.Target.ID != "" {
			c.add(p+".id", "a board target has no id")
		}
		if t.Orphaned {
			c.add(p, "a board thread cannot be orphaned")
		}
		return
	case "frame", "node", "edge":
	default:
		c.add(p+".kind", "must be board, frame, node or edge")
		return
	}
	if t.Target.ID == "" {
		c.add(p+".id", "must not be empty")
		return
	}
	if t.Orphaned {
		return
	}
	n := nodes[t.Target.ID]
	ok := false
	switch t.Target.Kind {
	case "frame":
		ok = n != nil && n.Type == TypeFrame
	case "node":
		ok = n != nil && n.Type != TypeFrame
	case "edge":
		ok = edges[t.Target.ID]
	}
	if !ok {
		c.add(p, "no %s %q on the board; a thread of a deleted object is orphaned", t.Target.Kind, t.Target.ID)
	}
}

// checkAsset returns why asset is not a usable image path of the board in
// dir, or "". It must be a regular file inside the board, not a symlink.
func checkAsset(dir, asset string) string {
	if !assetPath.MatchString(asset) {
		return "must be assets/<sha256>.<png|jpg|gif|webp>"
	}
	if st, err := os.Lstat(filepath.Join(dir, "assets")); err != nil || !st.IsDir() {
		return "the board has no assets directory"
	}
	if st, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(asset))); err != nil || !st.Mode().IsRegular() {
		return fmt.Sprintf("%s is not a file in the board", asset)
	}
	return ""
}

// bodyProblem says why a message body cannot be posted, or "".
func bodyProblem(body string) string {
	switch {
	case !utf8.ValidString(body) || len(body) > 64*1024:
		return "must be UTF-8 and at most 64 KiB"
	case strings.TrimSpace(body) == "":
		return "must not be empty"
	}
	return ""
}

// shape lists the required fields of an object in board.json and the
// shapes of its nested objects and arrays of objects.
type shape struct {
	required []string
	objects  map[string]*shape // field -> object shape
	arrays   map[string]*shape // field -> shape of each array element
	// nodeKeys checks the keys a node of its type may have; zero values
	// cannot tell an empty caption on a card from no caption.
	nodeKeys bool
}

var nodeTypeKeys = map[string][]string{
	TypeCard:  {"text", "color"},
	TypeImage: {"asset", "caption"},
	TypeFrame: {"title", "order", "preset"},
}

var nodeCommonKeys = []string{"id", "type", "rev", "parentId", "x", "y", "w", "h", "z"}

var boardShape = func() *shape {
	author := &shape{required: []string{"role"}}
	message := &shape{required: []string{"id", "author", "body", "createdAt"}, objects: map[string]*shape{"author": author}}
	target := &shape{required: []string{"kind"}}
	return &shape{
		required: []string{"formatVersion", "id", "title", "revision", "nodes", "edges", "threads", "deleted", "nextId"},
		arrays: map[string]*shape{
			"nodes":   {required: []string{"id", "type", "rev"}, nodeKeys: true},
			"edges":   {required: []string{"id", "rev", "from", "to"}},
			"threads": {required: []string{"id", "rev", "target", "resolved", "orphaned", "messages"}, objects: map[string]*shape{"target": target}, arrays: map[string]*shape{"messages": message}},
			"deleted": {required: []string{"id", "rev"}},
		},
	}
}()

// checkShape checks raw JSON at path against s and reports like
// checkRequired.
func checkShape(c *checker, s *shape, path string, raw []byte) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		c.add(path, "not JSON: %v", err)
		return
	}
	s.check(c, path, v)
}

// checkRequired reports required fields that are missing or null, and
// objects or arrays of the wrong JSON type.
func checkRequired(data []byte) []Problem {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return []Problem{{Message: fmt.Sprintf("not JSON: %v", err)}}
	}
	c := &checker{}
	boardShape.check(c, "", v)
	return c.problems
}

func (s *shape) check(c *checker, path string, v any) {
	obj, ok := v.(map[string]any)
	if !ok {
		c.add(path, "must be an object")
		return
	}
	join := func(k string) string {
		if path == "" {
			return k
		}
		return path + "." + k
	}
	for _, k := range s.required {
		if _, ok := obj[k]; !ok {
			c.add(join(k), "required")
		}
	}
	if typ, ok := obj["type"].(string); s.nodeKeys && ok && nodeTypeKeys[typ] != nil {
		for _, k := range sortedKeys(obj) {
			if !slices.Contains(nodeCommonKeys, k) && !slices.Contains(nodeTypeKeys[typ], k) {
				c.add(join(k), "a %s has no %s", typ, k)
			}
		}
	}
	// encoding/json would turn null into a zero value; no field takes null.
	for _, k := range sortedKeys(obj) {
		if obj[k] == nil {
			c.add(join(k), "must not be null")
		}
	}
	for _, k := range sortedKeys(s.objects) {
		if f, ok := obj[k]; ok && f != nil {
			s.objects[k].check(c, join(k), f)
		}
	}
	for _, k := range sortedKeys(s.arrays) {
		f, ok := obj[k]
		if !ok || f == nil {
			continue
		}
		items, ok := f.([]any)
		if !ok {
			c.add(join(k), "must be an array")
			continue
		}
		for i, item := range items {
			s.arrays[k].check(c, fmt.Sprintf("%s[%d]", join(k), i), item)
		}
	}
}
