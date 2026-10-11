package board

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMermaidShowsWholeBoard(t *testing.T) {
	dir := newBoard(t)
	apply(t, dir, CLI, `{"base":0,"ops":[
		{"op":"add","ref":"$out","node":{"type":"frame","title":"Out \"1\"","order":1}},
		{"op":"add","ref":"$in","node":{"type":"frame","title":"end","order":2,"parentId":"$out"}},
		{"op":"add","ref":"$a","node":{"type":"card","text":"a|b #1 <br> & <i> SIGNAL_FILTERS слово_слово v1_2 _y_ __init__\n- `+"`x`"+` **y** [l](u)\n2. two\n    code\n%% end\nend","parentId":"$in"}},
		{"op":"add","ref":"$empty","node":{"type":"frame","title":"Empty","order":3}},
		{"op":"add","ref":"$blank","node":{"type":"card","text":""}},
		{"op":"add","edge":{"from":"$a","to":"$a"}},
		{"op":"add","edge":{"from":"$a","to":"$empty","label":"to | \"frame\"\rnext %% x"}},
		{"op":"add","edge":{"from":"$a","to":"$empty"}},
		{"op":"comment","target":{"kind":"node","id":"$a"},"body":"hi"}]}`)
	if _, err := ImportImage(dir, pngBytes(t, 3, 2), "f1", "", agent, CLI); err != nil {
		t.Fatal(err)
	}
	b, _ := read(t, dir)
	got, err := Mermaid(Inspect(b, dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	head, body, _ := strings.Cut(got, "\n")
	var h Header
	if err := json.Unmarshal([]byte(strings.TrimPrefix(head, "%% ")), &h); err != nil || h.Revision != b.Revision || h.Path != dir {
		t.Fatalf("header %q: %v", head, err)
	}
	// Ids are the keys, frames nest as subgraphs, each edge follows its id,
	// and no label text can end a label, a line or the diagram.
	want := `flowchart TB
	subgraph f1["Out #quot;1#quot;"]
		subgraph f2["end"]
			n3["a#124;b #35;1 #lt;br#gt; #amp; #lt;i#gt; SIGNAL_FILTERS слово_слово v1_2 #95;y#95; #95;#95;init#95;#95;
#45; #96;x#96; #42;#42;y#42;#42; #91;l#93;(u)
2#46; two
#32;   code
#37;% end
end"]
		end
		n11[["image"]]
	end
	subgraph f4["Empty"]
	end
	n5[" "]
	%% e6
	n3 --> n3
	%% e7
	n3 -->|"to #124; #quot;frame#quot;
next %% x"| f4
	%% e8
	n3 --> f4
	%% thread t9 on n3: open, 1 messages
`
	if body != want {
		t.Fatalf("got\n%s\nwant\n%s", body, want)
	}
}
