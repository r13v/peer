package board

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Mermaid renders a whole-board inspection as a Mermaid flowchart. It is
// a view: node and frame ids are the Mermaid keys, a "%% eN" line names
// each edge, and "%%" lines summarize threads, but colors, frame order,
// asset paths and thread messages are only in the JSON inspection. The
// first line is the JSON header, so the revision is the next pack's base.
func Mermaid(in Inspection, warnings []Warning) (string, error) {
	head, err := json.Marshal(struct {
		Header
		Warnings []Warning `json:"warnings,omitempty"`
	}{in.Board, warnings})
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%%%% %s\nflowchart TB\n", head)
	children := map[string][]*Node{}
	for _, n := range in.Nodes {
		children[n.ParentID] = append(children[n.ParentID], n)
	}
	var nodes func(parent, indent string)
	nodes = func(parent, indent string) {
		for _, n := range children[parent] {
			switch n.Type {
			case TypeFrame:
				fmt.Fprintf(&sb, "%ssubgraph %s[\"%s\"]\n", indent, n.ID, mermaidText(*n.Title))
				nodes(n.ID, indent+"\t")
				fmt.Fprintf(&sb, "%send\n", indent)
			case TypeImage:
				caption := n.Caption
				if caption == "" {
					caption = "image"
				}
				fmt.Fprintf(&sb, "%s%s[[\"%s\"]]\n", indent, n.ID, mermaidText(caption))
			default:
				fmt.Fprintf(&sb, "%s%s[\"%s\"]\n", indent, n.ID, mermaidText(*n.Text))
			}
		}
	}
	nodes("", "\t")
	for _, e := range in.Edges {
		fmt.Fprintf(&sb, "\t%%%% %s\n", e.ID)
		if e.Label == "" {
			fmt.Fprintf(&sb, "\t%s --> %s\n", e.From, e.To)
		} else {
			fmt.Fprintf(&sb, "\t%s -->|\"%s\"| %s\n", e.From, mermaidText(e.Label), e.To)
		}
	}
	for _, t := range in.Threads {
		on := t.Target.Kind
		if t.Target.ID != "" {
			on = t.Target.ID
		}
		state := "open"
		if t.Resolved {
			state = "resolved"
		}
		if t.Orphaned {
			state += ", orphaned"
		}
		fmt.Fprintf(&sb, "\t%%%% thread %s on %s: %s, %d messages\n", t.ID, on, state, len(t.Messages))
	}
	return sb.String(), nil
}

// mermaidEntities replaces the characters that end a quoted label, start
// an entity or HTML, or mark inline markdown: Mermaid 11 renders a label
// as markdown, and an entity code stays literal there. mermaidUnderscores
// handles "_".
var mermaidEntities = strings.NewReplacer(
	"#", "#35;", `"`, "#quot;", "&", "#amp;", "<", "#lt;", ">", "#gt;", "|", "#124;",
	"`", "#96;", "*", "#42;", "[", "#91;", "]", "#93;", "\\", "#92;", "\r\n", "\n", "\r", "\n",
)

// mermaidLineStart matches what makes a line a markdown block: a list
// marker, a setext rule, indentation, which would become a code block,
// or "%%", which Mermaid strips as a comment before it parses.
var mermaidLineStart = regexp.MustCompile(`^(?:[-+=%\s]|\d+[.)])`)

// mermaidText makes plain text safe inside a quoted Mermaid label. A line
// break stays a real one: a "<br>" would make Mermaid decode the entities
// before its markdown pass.
func mermaidText(s string) string {
	lines := strings.Split(mermaidUnderscores(mermaidEntities.Replace(s)), "\n")
	for i, l := range lines {
		if m := mermaidLineStart.FindString(l); m != "" {
			last := m[len(m)-1:]
			lines[i] = m[:len(m)-1] + fmt.Sprintf("#%d;", last[0]) + l[len(m):]
		}
	}
	s = strings.Join(lines, "\n")
	if strings.TrimSpace(s) == "" {
		return " "
	}
	return s
}

// mermaidUnderscores keeps a "_" between two letters or digits, as in
// SIGNAL_FILTERS: markdown never starts or ends emphasis inside a word.
// Any other "_" becomes an entity code.
func mermaidUnderscores(s string) string {
	if !strings.Contains(s, "_") {
		return s
	}
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	rs := []rune(s)
	var sb strings.Builder
	for i, r := range rs {
		if r == '_' && (i == 0 || i == len(rs)-1 || !word(rs[i-1]) || !word(rs[i+1])) {
			sb.WriteString("#95;")
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
