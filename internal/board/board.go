// Package board keeps a peer board: a <name>.board directory with
// board.json, its image assets and a lock file. It knows nothing of rooms
// or HTTP; the CLI and the server change a board only through Apply, so
// both follow the same rules.
package board

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

const (
	// FormatVersion is the board.json format this peer reads and writes.
	FormatVersion = 1
	// MaxAssetSize limits an imported image.
	MaxAssetSize = 20 << 20
)

// Board is board.json. Field order is the serialization order.
type Board struct {
	FormatVersion int         `json:"formatVersion"`
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	RoomID        string      `json:"roomId,omitempty"`
	Revision      int64       `json:"revision"`
	Nodes         []*Node     `json:"nodes"`
	Edges         []*Edge     `json:"edges"`
	Threads       []*Thread   `json:"threads"`
	Deleted       []Tombstone `json:"deleted"`
	NextID        int64       `json:"nextId"`
}

// Node is a card, an image or a frame. Required type-specific fields are
// pointers so that an empty card text is still written; geometry is
// pointers so that an unplaced node has no x and y at all.
type Node struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Rev      int64    `json:"rev"`
	ParentID string   `json:"parentId,omitempty"`
	Text     *string  `json:"text,omitempty"`
	Color    string   `json:"color,omitempty"`
	Asset    *string  `json:"asset,omitempty"`
	Caption  string   `json:"caption,omitempty"`
	Title    *string  `json:"title,omitempty"`
	Order    *float64 `json:"order,omitempty"`
	Preset   string   `json:"preset,omitempty"`
	X        *float64 `json:"x,omitempty"`
	Y        *float64 `json:"y,omitempty"`
	W        *float64 `json:"w,omitempty"`
	H        *float64 `json:"h,omitempty"`
	Z        *float64 `json:"z,omitempty"`
}

// Edge connects two nodes.
type Edge struct {
	ID    string `json:"id"`
	Rev   int64  `json:"rev"`
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
}

// Target is what a thread discusses: the board, a frame, a node or an edge.
type Target struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// Thread is a discussion attached to a target.
type Thread struct {
	ID          string     `json:"id"`
	Rev         int64      `json:"rev"`
	Target      Target     `json:"target"`
	Resolved    bool       `json:"resolved"`
	Orphaned    bool       `json:"orphaned"`
	TargetLabel string     `json:"targetLabel,omitempty"`
	Messages    []*Message `json:"messages"`
}

// Author is a message's author; a human is {role: "user"}.
type Author struct {
	Role   string `json:"role"`
	RoomID string `json:"roomId,omitempty"`
}

// Message is one post in a thread; messages never change.
type Message struct {
	ID        string `json:"id"`
	Author    Author `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

// Tombstone records a deleted node, edge or thread and the revision that
// deleted it, so restore can tell whether anyone touched it since.
type Tombstone struct {
	ID  string `json:"id"`
	Rev int64  `json:"rev"`
}

// Node types.
const (
	TypeCard  = "card"
	TypeImage = "image"
	TypeFrame = "frame"
)

var cardColors = []string{"gray", "yellow", "green", "blue", "purple", "red"}

// Error kinds of the contract's BoardError.
const (
	KindConflict           = "conflict"
	KindInvalid            = "invalid"
	KindForbidden          = "forbidden"
	KindNotFound           = "notFound"
	KindUnsupportedVersion = "unsupportedVersion"
	KindTooLarge           = "tooLarge"
	KindUnsupportedMedia   = "unsupportedMedia"
	KindRoom               = "room"
)

// Error is the contract's BoardError; its JSON is what the CLI prints and
// the server answers.
type Error struct {
	Kind          string     `json:"error"`
	Base          *int64     `json:"base,omitempty"`
	Conflicts     []Conflict `json:"conflicts,omitempty"`
	Problems      []Problem  `json:"problems,omitempty"`
	Message       string     `json:"message,omitempty"`
	FormatVersion int        `json:"formatVersion,omitempty"`
	Limit         int64      `json:"limit,omitempty"`
}

// Conflict names an entity whose revision does not match; Rev is nil when
// the entity does not exist.
type Conflict struct {
	ID  string `json:"id"`
	Rev *int64 `json:"rev"`
}

// Problem is one schema or invariant violation.
type Problem struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindConflict:
		parts := make([]string, len(e.Conflicts))
		for i, c := range e.Conflicts {
			rev := "gone"
			if c.Rev != nil {
				rev = fmt.Sprintf("rev %d", *c.Rev)
			}
			parts[i] = c.ID + " (" + rev + ")"
		}
		return "conflict: " + strings.Join(parts, ", ")
	case KindInvalid:
		parts := make([]string, len(e.Problems))
		for i, p := range e.Problems {
			parts[i] = p.Message
			if p.Path != "" {
				parts[i] = p.Path + ": " + p.Message
			}
		}
		return "invalid: " + strings.Join(parts, "; ")
	case KindUnsupportedVersion:
		return fmt.Sprintf("board formatVersion %d is newer than this peer; update peer", e.FormatVersion)
	case KindTooLarge:
		return fmt.Sprintf("too large: the limit is %d bytes", e.Limit)
	default:
		return e.Kind + ": " + e.Message
	}
}

func invalid(path, format string, args ...any) *Error {
	return &Error{Kind: KindInvalid, Problems: []Problem{{Path: path, Message: fmt.Sprintf(format, args...)}}}
}

func invalidAll(problems []Problem) *Error {
	return &Error{Kind: KindInvalid, Problems: problems}
}

func notFound(format string, args ...any) *Error {
	return &Error{Kind: KindNotFound, Message: fmt.Sprintf(format, args...)}
}

func forbidden(format string, args ...any) *Error {
	return &Error{Kind: KindForbidden, Message: fmt.Sprintf(format, args...)}
}

func unsupportedMedia(format string, args ...any) *Error {
	return &Error{Kind: KindUnsupportedMedia, Message: fmt.Sprintf(format, args...)}
}

// AsError turns any error into a BoardError, so callers always have
// contract JSON to print.
func AsError(err error) *Error {
	var be *Error
	if errors.As(err, &be) {
		return be
	}
	return invalid("", "%v", err)
}

// Marshal serializes b deterministically: tabs, contract key order,
// stored array order and a final newline.
func Marshal(b *Board) ([]byte, error) {
	normalize(b)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Board text is not HTML; escaping < and & would only make diffs noisy.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "\t")
	if err := enc.Encode(b); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Parse reads board.json bytes and checks the schema and every invariant;
// dir is the board directory, for checking assets.
func Parse(data []byte, dir string) (*Board, error) {
	var head struct {
		FormatVersion json.RawMessage `json:"formatVersion"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, invalid("", "board.json is not a JSON object: %v", err)
	}
	var version int
	if err := json.Unmarshal(head.FormatVersion, &version); err != nil || version < 1 {
		return nil, invalid("formatVersion", "must be a positive integer")
	}
	if version > FormatVersion {
		return nil, &Error{Kind: KindUnsupportedVersion, FormatVersion: version}
	}
	// encoding/json fills missing and null fields with zero values, so
	// required fields are checked on the raw JSON first.
	if problems := checkRequired(data); len(problems) > 0 {
		return nil, invalidAll(problems)
	}
	var b Board
	if err := decodeStrict(data, &b); err != nil {
		return nil, invalid("", "%v", err)
	}
	normalize(&b)
	if problems := validate(&b, dir); len(problems) > 0 {
		return nil, invalidAll(problems)
	}
	return &b, nil
}

// decodeStrict decodes one JSON value and rejects unknown fields and
// trailing data, so a typo in board.json or a pack is reported, not lost.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}

// normalize makes nil arrays empty, so they serialize as [] and not null.
func normalize(b *Board) {
	if b.Nodes == nil {
		b.Nodes = []*Node{}
	}
	if b.Edges == nil {
		b.Edges = []*Edge{}
	}
	if b.Threads == nil {
		b.Threads = []*Thread{}
	}
	if b.Deleted == nil {
		b.Deleted = []Tombstone{}
	}
	for _, t := range b.Threads {
		if t.Messages == nil {
			t.Messages = []*Message{}
		}
	}
}

// clone deep-copies b, so a pack works on a copy and a failed pack leaves
// the original untouched.
func clone(b *Board) *Board {
	data, err := json.Marshal(b)
	if err != nil {
		panic(err) // a Board always marshals
	}
	var c Board
	if err := json.Unmarshal(data, &c); err != nil {
		panic(err)
	}
	normalize(&c)
	return &c
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// label is the text a thread keeps of its target once the target is gone.
func (n *Node) label() string {
	switch {
	case n.Text != nil:
		first, _, _ := strings.Cut(*n.Text, "\n")
		return first
	case n.Title != nil:
		return *n.Title
	default:
		return n.Caption
	}
}

func (n *Node) placed() bool { return n.X != nil && n.Y != nil }
