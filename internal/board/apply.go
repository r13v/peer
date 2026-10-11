package board

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Mode says who sends a pack. The CLI may not touch geometry or restore;
// the browser may change any field.
type Mode int

const (
	// CLI is a pack from peer board apply or import-image.
	CLI Mode = iota
	// Browser is a pack from the board tab.
	Browser
)

// Pack is a parsed pack: exactly one of Base and Expect is set.
type Pack struct {
	Base   *int64
	Expect map[string]int64
	Ops    []Op
}

// Op is one parsed operation; which fields are set depends on Op.
type Op struct {
	Op       string
	Ref      string
	Node     *Node
	Edge     *Edge
	Thread   *Thread // restore
	ThreadID string  // reply, resolve
	ID       string
	Set      map[string]json.RawMessage
	Cascade  bool
	Target   *Target
	Body     string
	Resolved *bool
	// geometry lists the geometry keys present in add.node, even null
	// ones: the CLI may not name them at all.
	geometry []string
}

// ApplyResult is what a successful pack reports.
type ApplyResult struct {
	Revision int64             `json:"revision"`
	Refs     map[string]string `json:"refs"`
	Created  []string          `json:"created"`
	Changed  []string          `json:"changed"`
	Deleted  []string          `json:"deleted"`
	Warnings []Warning         `json:"warnings"`
}

// Warning reports a problem that did not fail the operation.
type Warning struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

var (
	refName  = regexp.MustCompile(`^\$[A-Za-z0-9_-]{1,64}$`)
	geometry = []string{"x", "y", "w", "h", "z"}
	settable = map[string][]string{
		TypeCard:  {"text", "color", "parentId"},
		TypeImage: {"caption", "parentId"},
		TypeFrame: {"title", "order", "parentId"},
		"edge":    {"from", "to", "label"},
	}
	// requiredField fields cannot be removed with null.
	requiredField = []string{"text", "title", "order", "from", "to"}
	opFields      = map[string][]string{
		"add":     {"op", "ref", "node", "edge"},
		"update":  {"op", "id", "set"},
		"delete":  {"op", "id", "cascade"},
		"restore": {"op", "node", "edge", "thread"},
		"comment": {"op", "ref", "target", "body"},
		"reply":   {"op", "thread", "body"},
		"resolve": {"op", "thread", "resolved"},
	}
)

var (
	newNodeShape = &shape{required: []string{"type"}, nodeKeys: true}
	newEdgeShape = &shape{required: []string{"from", "to"}}
)

// ParsePack parses and checks the shape of a pack.
func ParsePack(data []byte) (Pack, error) {
	var p Pack
	c := &checker{}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		return p, invalid("", "a pack is a JSON object with base or expect, and ops")
	}
	for _, k := range sortedKeys(top) {
		if k != "base" && k != "expect" && k != "ops" {
			c.add(k, "unknown field")
		}
	}
	rawBase, hasBase := top["base"]
	rawExpect, hasExpect := top["expect"]
	switch {
	case hasBase == hasExpect:
		c.add("", "give exactly one of base and expect")
	case hasBase:
		var base int64
		if isNull(rawBase) || json.Unmarshal(rawBase, &base) != nil || base < 0 {
			c.add("base", "must be a board revision")
		} else {
			p.Base = &base
		}
	default:
		var expect map[string]json.RawMessage
		if isNull(rawExpect) || json.Unmarshal(rawExpect, &expect) != nil {
			c.add("expect", "must map ids to revisions")
		}
		p.Expect = map[string]int64{}
		for _, id := range sortedKeys(expect) {
			// null would decode to revision 0.
			var rev int64
			if isNull(expect[id]) || json.Unmarshal(expect[id], &rev) != nil || rev < 0 {
				c.add("expect."+id, "must be a revision")
			}
			p.Expect[id] = rev
		}
	}
	var ops []json.RawMessage
	if err := json.Unmarshal(top["ops"], &ops); err != nil || len(ops) == 0 {
		c.add("ops", "must be a non-empty array")
	}
	for i, raw := range ops {
		if op, ok := parseOp(c, fmt.Sprintf("ops[%d]", i), raw); ok {
			p.Ops = append(p.Ops, op)
		}
	}
	if len(c.problems) > 0 {
		return Pack{}, invalidAll(c.problems)
	}
	return p, nil
}

func parseOp(c *checker, path string, raw json.RawMessage) (Op, bool) {
	var op Op
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		c.add(path, "an op is a JSON object")
		return op, false
	}
	start := len(c.problems)
	if json.Unmarshal(fields["op"], &op.Op) != nil || opFields[op.Op] == nil {
		c.add(path+".op", "must be add, update, delete, restore, comment, reply or resolve")
		return op, false
	}
	for _, k := range sortedKeys(fields) {
		if !slices.Contains(opFields[op.Op], k) {
			c.add(path+"."+k, "not a field of %s", op.Op)
		}
	}
	field := func(name string, v any, required bool) bool {
		raw, ok := fields[name]
		if !ok {
			if required {
				c.add(path+"."+name, "required")
			}
			return false
		}
		// null would decode to a zero value, such as resolved false.
		if isNull(raw) {
			c.add(path+"."+name, "must not be null")
			return false
		}
		if err := decodeStrict(raw, v); err != nil {
			c.add(path+"."+name, "%v", err)
			return false
		}
		return true
	}
	if field("ref", &op.Ref, false) && !refName.MatchString(op.Ref) {
		c.add(path+".ref", "must be $ followed by letters, digits, _ or -")
	}
	one := func(names ...string) string {
		var given []string
		for _, n := range names {
			if _, ok := fields[n]; ok {
				given = append(given, n)
			}
		}
		if len(given) != 1 {
			c.add(path, "%s takes exactly one of %s", op.Op, strings.Join(names, ", "))
			return ""
		}
		return given[0]
	}
	switch op.Op {
	case "add":
		switch one("node", "edge") {
		case "node":
			var keys map[string]json.RawMessage
			_ = json.Unmarshal(fields["node"], &keys)
			for _, k := range geometry {
				if _, ok := keys[k]; ok {
					op.geometry = append(op.geometry, k)
				}
			}
			for _, k := range []string{"id", "rev"} {
				if _, ok := keys[k]; ok {
					c.add(path+".node."+k, "a new node gets its %s from the board", k)
				}
			}
			checkShape(c, newNodeShape, path+".node", fields["node"])
			op.Node = &Node{}
			field("node", op.Node, true)
		case "edge":
			checkShape(c, newEdgeShape, path+".edge", fields["edge"])
			var e struct {
				From  string `json:"from"`
				To    string `json:"to"`
				Label string `json:"label,omitempty"`
			}
			if field("edge", &e, true) {
				op.Edge = &Edge{From: e.From, To: e.To, Label: e.Label}
			}
		}
	case "update":
		field("id", &op.ID, true)
		if field("set", &op.Set, true) && len(op.Set) == 0 {
			c.add(path+".set", "must set at least one field")
		}
	case "delete":
		field("id", &op.ID, true)
		field("cascade", &op.Cascade, false)
	case "restore":
		which := one("node", "edge", "thread")
		if which != "" {
			// A snapshot is stored as is, so it must be complete.
			checkShape(c, boardShape.arrays[which+"s"], path+"."+which, fields[which])
		}
		switch which {
		case "node":
			op.Node = &Node{}
			field("node", op.Node, true)
		case "edge":
			op.Edge = &Edge{}
			field("edge", op.Edge, true)
		case "thread":
			op.Thread = &Thread{}
			field("thread", op.Thread, true)
		}
	case "comment":
		if _, ok := fields["target"]; ok {
			checkShape(c, &shape{required: []string{"kind"}}, path+".target", fields["target"])
		}
		op.Target = &Target{}
		field("target", op.Target, true)
		field("body", &op.Body, true)
	case "reply":
		field("thread", &op.ThreadID, true)
		field("body", &op.Body, true)
	case "resolve":
		field("thread", &op.ThreadID, true)
		op.Resolved = new(bool)
		field("resolved", op.Resolved, true)
	}
	return op, len(c.problems) == start
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// checkMode rejects what a CLI pack may not do: geometry and restore.
// The apply logic itself still moves nodes, on ungroup and on reparenting.
func checkMode(pack Pack, mode Mode) error {
	if mode == Browser {
		return nil
	}
	for i, op := range pack.Ops {
		switch {
		case op.Op == "restore":
			return forbidden("ops[%d]: restore is only for the browser", i)
		case op.Op == "add" && op.Node != nil:
			n := op.Node
			if len(op.geometry) > 0 || n.X != nil || n.Y != nil || n.W != nil || n.H != nil || n.Z != nil {
				return forbidden("ops[%d]: the CLI does not set geometry (x, y, w, h, z); the browser places nodes", i)
			}
		case op.Op == "update":
			for _, k := range sortedKeys(op.Set) {
				if slices.Contains(geometry, k) {
					return forbidden("ops[%d]: the CLI does not set %s; the browser places nodes", i, k)
				}
			}
		}
	}
	return nil
}

// Apply applies pack to a copy of b, all or nothing, and returns the new
// board. dir is the board directory, for checking image assets.
func Apply(b *Board, dir string, pack Pack, author Author, mode Mode) (*Board, ApplyResult, error) {
	if err := checkMode(pack, mode); err != nil {
		return nil, ApplyResult{}, err
	}
	if author.Role == "" {
		return nil, ApplyResult{}, invalid("author", "a pack needs an author")
	}
	a := &applier{
		b: clone(b), pack: pack, author: author, mode: mode, rev: b.Revision + 1,
		now:  time.Now().UTC().Format(time.RFC3339),
		refs: map[string]string{}, origLive: map[string]int64{}, origTomb: map[string]int64{},
		seen: map[string]bool{},
	}
	for _, n := range b.Nodes {
		a.origLive[n.ID] = n.Rev
	}
	for _, e := range b.Edges {
		a.origLive[e.ID] = e.Rev
	}
	for _, t := range b.Threads {
		a.origLive[t.ID] = t.Rev
	}
	for _, d := range b.Deleted {
		a.origTomb[d.ID] = d.Rev
	}
	for i, op := range pack.Ops {
		if err := a.apply(fmt.Sprintf("ops[%d]", i), op); err != nil {
			return nil, ApplyResult{}, err
		}
	}
	if conflicts := a.conflicts(); len(conflicts) > 0 {
		return nil, ApplyResult{}, a.conflictError(conflicts)
	}
	result := a.finish()
	if problems := validate(a.b, dir); len(problems) > 0 {
		return nil, ApplyResult{}, invalidAll(problems)
	}
	return a.b, result, nil
}

type applier struct {
	b      *Board
	pack   Pack
	author Author
	mode   Mode
	rev    int64
	now    string
	refs   map[string]string
	// origLive and origTomb hold the revisions before the pack, which
	// base and expect are checked against.
	origLive, origTomb map[string]int64
	// affected are pre-existing entities the pack changes, explicitly or
	// implicitly; created, touched and deleted feed the result.
	affected, created, touched, deleted []string
	seen                                map[string]bool
}

// record appends id to list once.
func (a *applier) record(list *[]string, kind, id string) {
	if key := kind + ":" + id; !a.seen[key] {
		a.seen[key] = true
		*list = append(*list, id)
	}
}

func (a *applier) affect(id string) {
	_, live := a.origLive[id]
	_, tomb := a.origTomb[id]
	if live || tomb {
		a.record(&a.affected, "affected", id)
	}
}

func (a *applier) touch(id string) {
	a.affect(id)
	a.record(&a.touched, "touched", id)
}

func (a *applier) newID(prefix string) string {
	id := prefix + strconv.FormatInt(a.b.NextID, 10)
	a.b.NextID++
	return id
}

func (a *applier) conflicts() []Conflict {
	var out []Conflict
	for _, id := range a.affected {
		orig, live := a.origLive[id]
		if !live {
			orig = a.origTomb[id]
		}
		var bad bool
		if a.pack.Base != nil {
			bad = orig > *a.pack.Base
		} else {
			want, ok := a.pack.Expect[id]
			bad = !ok || want != orig
		}
		if bad {
			out = append(out, a.conflict(id))
		}
	}
	return out
}

// conflict reports id with its current revision, that of its tombstone
// when it is deleted, or nil when it does not exist at all.
func (a *applier) conflict(id string) Conflict {
	if rev, ok := a.origLive[id]; ok {
		return Conflict{ID: id, Rev: &rev}
	}
	if rev, ok := a.origTomb[id]; ok {
		return Conflict{ID: id, Rev: &rev}
	}
	return Conflict{ID: id}
}

func (a *applier) conflictError(conflicts []Conflict) error {
	return &Error{Kind: KindConflict, Base: a.pack.Base, Conflicts: conflicts}
}

// missing reports an op on an entity that is not on the board: a conflict
// if it was not there before the pack, a mistake in the pack otherwise.
func (a *applier) missing(path, id string) error {
	_, live := a.origLive[id]
	if live || a.seen["created:"+id] {
		return invalid(path, "%s was deleted earlier in this pack", id)
	}
	return a.conflictError(append(a.conflicts(), Conflict{ID: id}))
}

func (a *applier) resolve(path, id string) (string, error) {
	if strings.HasPrefix(id, "$") {
		target, ok := a.refs[id]
		if !ok {
			return "", invalid(path, "%s is not defined by an earlier add or comment of this pack", id)
		}
		return target, nil
	}
	if id == "" {
		return "", invalid(path, "must not be empty")
	}
	return id, nil
}

func (a *applier) setRef(path, ref, id string) error {
	if ref == "" {
		return nil
	}
	if _, ok := a.refs[ref]; ok {
		return invalid(path+".ref", "%s is defined twice", ref)
	}
	a.refs[ref] = id
	return nil
}

func (a *applier) node(id string) *Node {
	for _, n := range a.b.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func (a *applier) edge(id string) *Edge {
	for _, e := range a.b.Edges {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (a *applier) thread(id string) *Thread {
	for _, t := range a.b.Threads {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// needNode resolves a node reference that must exist; kind "frame" also
// requires a frame. The referenced node is not affected.
func (a *applier) needNode(path, ref, kind string) (string, error) {
	id, err := a.resolve(path, ref)
	if err != nil {
		return "", err
	}
	n := a.node(id)
	if n == nil {
		return "", a.missing(path, id)
	}
	if kind == TypeFrame && n.Type != TypeFrame {
		return "", invalid(path, "%s is not a frame", id)
	}
	return id, nil
}

func (a *applier) apply(path string, op Op) error {
	switch op.Op {
	case "add":
		if op.Node != nil {
			return a.addNode(path, op)
		}
		return a.addEdge(path, op)
	case "update":
		return a.update(path, op)
	case "delete":
		return a.delete(path, op)
	case "restore":
		return a.restore(path, op)
	case "comment":
		return a.comment(path, op)
	case "reply", "resolve":
		id, err := a.resolve(path+".thread", op.ThreadID)
		if err != nil {
			return err
		}
		t := a.thread(id)
		if t == nil {
			return a.missing(path+".thread", id)
		}
		a.touch(id)
		if op.Op == "resolve" {
			t.Resolved = *op.Resolved
			return nil
		}
		m, err := a.message(path+".body", op.Body)
		if err != nil {
			return err
		}
		t.Messages = append(t.Messages, m)
		return nil
	}
	return invalid(path+".op", "unknown op %q", op.Op)
}

func (a *applier) addNode(path string, op Op) error {
	n := *op.Node
	prefix := "n"
	switch n.Type {
	case TypeFrame:
		prefix = "f"
	case TypeCard, TypeImage:
	default:
		return invalid(path+".node.type", "must be card, image or frame")
	}
	if n.ParentID != "" {
		id, err := a.needNode(path+".node.parentId", n.ParentID, TypeFrame)
		if err != nil {
			return err
		}
		n.ParentID = id
	}
	n.ID, n.Rev = a.newID(prefix), a.rev
	a.b.Nodes = append(a.b.Nodes, &n)
	a.record(&a.created, "created", n.ID)
	return a.setRef(path, op.Ref, n.ID)
}

func (a *applier) addEdge(path string, op Op) error {
	e := *op.Edge
	var err error
	if e.From, err = a.needNode(path+".edge.from", e.From, ""); err != nil {
		return err
	}
	if e.To, err = a.needNode(path+".edge.to", e.To, ""); err != nil {
		return err
	}
	e.ID, e.Rev = a.newID("e"), a.rev
	a.b.Edges = append(a.b.Edges, &e)
	a.record(&a.created, "created", e.ID)
	return a.setRef(path, op.Ref, e.ID)
}

func (a *applier) update(path string, op Op) error {
	id, err := a.resolve(path+".id", op.ID)
	if err != nil {
		return err
	}
	if n := a.node(id); n != nil {
		a.touch(id)
		return a.updateNode(path+".set", n, op.Set)
	}
	if e := a.edge(id); e != nil {
		a.touch(id)
		return a.updateEdge(path+".set", e, op.Set)
	}
	if a.thread(id) != nil {
		return invalid(path+".id", "threads change only through comment, reply, resolve, delete and restore")
	}
	return a.missing(path+".id", id)
}

func (a *applier) fields(kind string) []string {
	fields := settable[kind]
	if a.mode == Browser && kind != "edge" {
		fields = append(slices.Clone(fields), "x", "y", "w", "h", "z")
	}
	return fields
}

// setField checks that key may be set on kind and decodes raw into v;
// it returns false when raw is null and the field is to be removed.
func (a *applier) setField(path, kind, key string, raw json.RawMessage, v any) (bool, error) {
	if !slices.Contains(a.fields(kind), key) {
		return false, invalid(path, "%s cannot be set on a %s", key, kind)
	}
	if isNull(raw) {
		if slices.Contains(requiredField, key) {
			return false, invalid(path, "%s cannot be removed", key)
		}
		return false, nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return false, invalid(path, "%v", err)
	}
	return true, nil
}

func (a *applier) updateNode(path string, n *Node, set map[string]json.RawMessage) error {
	for _, k := range sortedKeys(set) {
		p := path + "." + k
		var err error
		switch k {
		case "text", "title":
			var s string
			var ok bool
			if ok, err = a.setField(p, n.Type, k, set[k], &s); ok {
				if k == "text" {
					n.Text = &s
				} else {
					n.Title = &s
				}
			}
		case "color", "caption":
			var s string
			if _, err = a.setField(p, n.Type, k, set[k], &s); err == nil {
				*map[string]*string{"color": &n.Color, "caption": &n.Caption}[k] = s
			}
		case "order", "x", "y", "w", "h", "z":
			var f float64
			var ok bool
			if ok, err = a.setField(p, n.Type, k, set[k], &f); err == nil {
				ptr := map[string]**float64{"order": &n.Order, "x": &n.X, "y": &n.Y, "w": &n.W, "h": &n.H, "z": &n.Z}[k]
				*ptr = nil
				if ok {
					*ptr = &f
				}
			}
		case "parentId":
			var ref string
			var ok bool
			if ok, err = a.setField(p, n.Type, k, set[k], &ref); err != nil {
				return err
			}
			parent := ""
			if ok && ref != "" {
				if parent, err = a.needNode(p, ref, TypeFrame); err != nil {
					return err
				}
			}
			// An agent cannot see geometry, so coordinates relative to the
			// old frame would be meaningless; the browser places it anew.
			if parent != n.ParentID && a.mode == CLI {
				n.X, n.Y = nil, nil
			}
			n.ParentID = parent
		default:
			err = invalid(p, "%s cannot be set on a %s", k, n.Type)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *applier) updateEdge(path string, e *Edge, set map[string]json.RawMessage) error {
	for _, k := range sortedKeys(set) {
		p := path + "." + k
		var s string
		ok, err := a.setField(p, "edge", k, set[k], &s)
		if err != nil {
			return err
		}
		switch k {
		case "label":
			e.Label = s
		case "from", "to":
			if !ok {
				continue
			}
			id, err := a.needNode(p, s, "")
			if err != nil {
				return err
			}
			if k == "from" {
				e.From = id
			} else {
				e.To = id
			}
		}
	}
	return nil
}

func (a *applier) tombstone(id string) {
	a.affect(id)
	a.b.Deleted = append(a.b.Deleted, Tombstone{ID: id, Rev: a.rev})
	a.record(&a.deleted, "deleted", id)
}

func (a *applier) delete(path string, op Op) error {
	id, err := a.resolve(path+".id", op.ID)
	if err != nil {
		return err
	}
	if n := a.node(id); n != nil {
		if n.Type != TypeFrame || op.Cascade {
			a.dropSubtree(id)
			return nil
		}
		// Ungroup: the children move to the frame's parent and keep their
		// place on the board; unplaced children stay unplaced.
		for _, c := range a.b.Nodes {
			if c.ParentID != id {
				continue
			}
			a.touch(c.ID)
			c.ParentID = n.ParentID
			if c.placed() {
				x, y := *c.X+value(n.X), *c.Y+value(n.Y)
				c.X, c.Y = &x, &y
			}
		}
		a.dropNode(n)
		return nil
	}
	if a.edge(id) != nil {
		a.dropEdge(id)
		return nil
	}
	if a.thread(id) != nil {
		// A deleted thread is gone until restored, not orphaned.
		a.b.Threads = slices.DeleteFunc(a.b.Threads, func(t *Thread) bool { return t.ID == id })
		a.tombstone(id)
		return nil
	}
	return a.missing(path+".id", id)
}

func value(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

// dropSubtree deletes node id and, for a frame, everything nested in it.
func (a *applier) dropSubtree(id string) {
	doomed := map[string]bool{id: true}
	for grown := true; grown; {
		grown = false
		for _, n := range a.b.Nodes {
			if !doomed[n.ID] && doomed[n.ParentID] {
				doomed[n.ID], grown = true, true
			}
		}
	}
	for _, n := range slices.Clone(a.b.Nodes) {
		if doomed[n.ID] {
			a.dropNode(n)
		}
	}
}

// dropNode deletes n with its edges and orphans its threads.
func (a *applier) dropNode(n *Node) {
	a.b.Nodes = slices.DeleteFunc(a.b.Nodes, func(m *Node) bool { return m.ID == n.ID })
	a.tombstone(n.ID)
	for _, e := range slices.Clone(a.b.Edges) {
		if e.From == n.ID || e.To == n.ID {
			a.dropEdge(e.ID)
		}
	}
	a.orphan(n.ID, n.label())
}

func (a *applier) dropEdge(id string) {
	e := a.edge(id)
	a.b.Edges = slices.DeleteFunc(a.b.Edges, func(x *Edge) bool { return x.ID == id })
	a.tombstone(id)
	a.orphan(id, e.Label)
}

func (a *applier) orphan(target, label string) {
	for _, t := range a.b.Threads {
		if t.Target.Kind == "board" || t.Target.ID != target || t.Orphaned {
			continue
		}
		a.touch(t.ID)
		t.Orphaned = true
		if label != "" {
			t.TargetLabel = label
		}
	}
}

func (a *applier) restore(path string, op Op) error {
	var id string
	switch {
	case op.Node != nil:
		id = op.Node.ID
	case op.Edge != nil:
		id = op.Edge.ID
	default:
		id = op.Thread.ID
	}
	if id == "" {
		return invalid(path, "the snapshot has no id")
	}
	i := slices.IndexFunc(a.b.Deleted, func(d Tombstone) bool { return d.ID == id })
	if i < 0 {
		if a.node(id) != nil || a.edge(id) != nil || a.thread(id) != nil {
			return a.conflictError(append(a.conflicts(), a.conflict(id)))
		}
		return a.missing(path, id)
	}
	a.affect(id)
	a.b.Deleted = slices.Delete(a.b.Deleted, i, i+1)
	a.record(&a.touched, "touched", id)
	switch {
	case op.Node != nil:
		n := *op.Node
		n.Rev = a.rev
		a.b.Nodes = append(a.b.Nodes, &n)
		a.unorphan(id)
	case op.Edge != nil:
		e := *op.Edge
		e.Rev = a.rev
		a.b.Edges = append(a.b.Edges, &e)
		a.unorphan(id)
	default:
		t := *op.Thread
		t.Rev = a.rev
		// The thread's target may have gone while the thread was deleted.
		t.Orphaned = t.Target.Kind != "board" && a.node(t.Target.ID) == nil && a.edge(t.Target.ID) == nil
		a.b.Threads = append(a.b.Threads, &t)
	}
	return nil
}

// unorphan reattaches the threads of a restored node or edge. They are
// affected like the threads a delete orphans, so a reply posted since
// makes the undo conflict instead of being changed unseen.
func (a *applier) unorphan(target string) {
	for _, t := range a.b.Threads {
		if t.Orphaned && t.Target.Kind != "board" && t.Target.ID == target {
			t.Orphaned, t.TargetLabel = false, ""
			a.touch(t.ID)
		}
	}
}

func (a *applier) comment(path string, op Op) error {
	t := *op.Target
	p := path + ".target"
	switch t.Kind {
	case "board":
		if t.ID != "" {
			return invalid(p+".id", "a board target has no id")
		}
	case "frame", "node":
		id, err := a.needNode(p+".id", t.ID, "")
		if err != nil {
			return err
		}
		if isFrame := a.node(id).Type == TypeFrame; isFrame != (t.Kind == "frame") {
			return invalid(p+".kind", "%s is not a %s", id, t.Kind)
		}
		t.ID = id
	case "edge":
		id, err := a.resolve(p+".id", t.ID)
		if err != nil {
			return err
		}
		if a.edge(id) == nil {
			return a.missing(p+".id", id)
		}
		t.ID = id
	default:
		return invalid(p+".kind", "must be board, frame, node or edge")
	}
	id := a.newID("t")
	m, err := a.message(path+".body", op.Body)
	if err != nil {
		return err
	}
	a.b.Threads = append(a.b.Threads, &Thread{ID: id, Rev: a.rev, Target: t, Messages: []*Message{m}})
	a.record(&a.created, "created", id)
	return a.setRef(path, op.Ref, id)
}

func (a *applier) message(path, body string) (*Message, error) {
	if msg := bodyProblem(body); msg != "" {
		return nil, invalid(path, "%s", msg)
	}
	return &Message{ID: a.newID("m"), Author: a.author, Body: body, CreatedAt: a.now}, nil
}

// finish stamps the new revision on every created or changed entity and
// builds the result.
func (a *applier) finish() ApplyResult {
	live := map[string]bool{}
	stamp := func(id string, rev *int64) {
		live[id] = true
		if a.seen["created:"+id] || a.seen["touched:"+id] {
			*rev = a.rev
		}
	}
	for _, n := range a.b.Nodes {
		stamp(n.ID, &n.Rev)
	}
	for _, e := range a.b.Edges {
		stamp(e.ID, &e.Rev)
	}
	for _, t := range a.b.Threads {
		stamp(t.ID, &t.Rev)
	}
	a.b.Revision = a.rev
	r := ApplyResult{Revision: a.rev, Refs: a.refs, Created: []string{}, Changed: []string{}, Deleted: []string{}, Warnings: []Warning{}}
	for _, id := range a.created {
		if live[id] {
			r.Created = append(r.Created, id)
		}
	}
	// A restored entity existed before, so it is reported as changed.
	for _, id := range a.touched {
		if live[id] && !a.seen["created:"+id] {
			r.Changed = append(r.Changed, id)
		}
	}
	for _, id := range a.deleted {
		if _, before := a.origLive[id]; before && !live[id] {
			r.Deleted = append(r.Deleted, id)
		}
	}
	return r
}
