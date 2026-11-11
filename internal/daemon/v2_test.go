package daemon

import (
	"strings"
	"testing"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/Facets-cloud/bx/internal/exit"
)

func TestParseCombo(t *testing.T) {
	cases := []struct {
		in   string
		key  string
		mods []chromedp.Modifier
	}{
		{"Enter", kb.Enter, nil},
		{"a", "a", nil},
		{"Escape", kb.Escape, nil},
		{"ArrowDown", kb.ArrowDown, nil},
		{"Control+A", "A", []chromedp.Modifier{chromedp.ModifierCtrl}},
		{"meta+shift+r", "r", []chromedp.Modifier{chromedp.ModifierMeta, chromedp.ModifierShift}},
		{"Control++", "+", []chromedp.Modifier{chromedp.ModifierCtrl}},
		{"Space", " ", nil},
	}
	for _, c := range cases {
		got, err := parseCombo(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if string(got.key) != c.key || len(got.mods) != len(c.mods) {
			t.Errorf("%q: got key %q mods %v", c.in, string(got.key), got.mods)
			continue
		}
		for i := range c.mods {
			if got.mods[i] != c.mods[i] {
				t.Errorf("%q: mod %d = %v", c.in, i, got.mods[i])
			}
		}
	}
	for _, bad := range []string{"Hyper+A", "Enterr", "Control+"} {
		if _, err := parseCombo(bad); exit.CodeOf(err) != exit.Validation {
			t.Errorf("%q: want validation error, got %v", bad, err)
		}
	}
}

func TestEvalSource(t *testing.T) {
	cases := map[string]string{
		"document.title":                  "document.title",
		"() => document.title":            "(() => document.title)()",
		"async () => { return 1 }":        "(async () => { return 1 })()",
		"function () { return 2 }":        "(function () { return 2 })()",
		"x => x":                          "(x => x)()",
		"[1, 2].map(x => x * 2)":          "[1, 2].map(x => x * 2)",
		"  document.querySelector('a')  ": "document.querySelector('a')",
	}
	for in, want := range cases {
		if got := evalSource(in); got != want {
			t.Errorf("evalSource(%q) = %q, want %q", in, got, want)
		}
	}
}

func axv(s string) *accessibility.Value {
	return &accessibility.Value{Type: "string", Value: []byte(`"` + s + `"`)}
}

func TestBuildSnapshot(t *testing.T) {
	nodes := []*accessibility.Node{
		{NodeID: "1", Role: axv("RootWebArea"), Name: axv("Page"), ChildIDs: []accessibility.NodeID{"2", "5"}},
		{NodeID: "2", ParentID: "1", Role: axv("generic"), ChildIDs: []accessibility.NodeID{"3"}},
		{NodeID: "3", ParentID: "2", Role: axv("link"), Name: axv("Docs"), BackendDOMNodeID: 30, ChildIDs: []accessibility.NodeID{"4"}},
		{NodeID: "4", ParentID: "3", Role: axv("StaticText"), Name: axv("Docs")},
		{NodeID: "5", ParentID: "1", Role: axv("checkbox"), Name: axv("Remember me"), BackendDOMNodeID: 50,
			Properties: []*accessibility.Property{{Name: "checked", Value: &accessibility.Value{Type: "tristate", Value: []byte(`"true"`)}}}},
		{NodeID: "6", ParentID: "1", Role: axv("button"), Name: axv("Hidden"), Ignored: true},
	}
	got, refs := buildSnapshot(nodes)
	if len(got) != 2 {
		t.Fatalf("want the link and the checkbox only, got %+v", got)
	}
	if got[0].Ref != "e1" || got[0].Role != "link" || got[0].Name != "Docs" || got[0].Depth != 0 {
		t.Errorf("link: %+v", got[0])
	}
	if got[1].Ref != "e2" || got[1].Role != "checkbox" || len(got[1].Props) != 1 || got[1].Props[0] != "checked" {
		t.Errorf("checkbox: %+v", got[1])
	}
	if refs["e1"] != cdp.BackendNodeID(30) || refs["e2"] != cdp.BackendNodeID(50) {
		t.Errorf("refs: %v", refs)
	}
	text := Snapshot{URL: "https://x", Title: "X", Nodes: got}.Text()
	if !strings.Contains(text, `e1 link "Docs"`) || !strings.Contains(text, `e2 checkbox "Remember me" [checked]`) {
		t.Errorf("text:\n%s", text)
	}
}

func TestRefPattern(t *testing.T) {
	for _, ok := range []string{"e1", "e123"} {
		if !refPattern.MatchString(ok) {
			t.Errorf("%q should be a ref", ok)
		}
	}
	for _, css := range []string{"e", "#e1", "e1 a", "em", "div.e1"} {
		if refPattern.MatchString(css) {
			t.Errorf("%q should be CSS", css)
		}
	}
}

func TestDispatchTabValidation(t *testing.T) {
	s := &server{}
	for _, req := range []Request{
		{Op: "fill", Args: map[string]string{"pairs": "not json"}},
		{Op: "upload", Args: map[string]string{"files": "nope"}},
		{Op: "key", Args: map[string]string{"keys": "Hyper+Q"}},
		{Op: "type", Args: map[string]string{"text": "x", "submit": "Nope"}},
		{Op: "goto", Args: map[string]string{"url": "javascript:alert(1)"}},
		{Op: "wait", Args: map[string]string{}},
		{Op: "eval", Args: map[string]string{"script": " "}},
		{Op: "fill", Args: map[string]string{"pairs": "[]"}},
	} {
		if _, err := s.dispatch(req); exit.CodeOf(err) != exit.Validation {
			t.Errorf("%+v: want validation error, got %v", req, err)
		}
	}
}

func TestMacCommands(t *testing.T) {
	cases := map[string]string{"Meta+a": "selectAll", "Meta+A": "selectAll", "Meta+v": "paste", "Meta+z": "undo", "Meta+Shift+z": "redo"}
	for in, want := range cases {
		c, _ := parseCombo(in)
		if got := c.commands(); len(got) != 1 || got[0] != want {
			t.Errorf("%s: got %v, want %s", in, got, want)
		}
	}
	for _, in := range []string{"Control+a", "a", "Meta+Shift+a", "Meta+q", "Enter"} {
		c, _ := parseCombo(in)
		if got := c.commands(); got != nil {
			t.Errorf("%s: want no command, got %v", in, got)
		}
	}
}

func TestSnapshotEditable(t *testing.T) {
	nodes := []*accessibility.Node{
		{NodeID: "1", Role: axv("RootWebArea"), ChildIDs: []accessibility.NodeID{"2"}},
		{NodeID: "2", ParentID: "1", Role: axv("generic"), Name: axv("Editor"), BackendDOMNodeID: 7,
			Properties: []*accessibility.Property{{Name: "editable", Value: &accessibility.Value{Type: "token", Value: []byte(`"richtext"`)}}}},
	}
	got, _ := buildSnapshot(nodes)
	if len(got) != 1 || got[0].Role != "textbox" || got[0].Name != "Editor" {
		t.Fatalf("got %+v", got)
	}
}

func TestSnapshotTextboxIsLeaf(t *testing.T) {
	nodes := []*accessibility.Node{
		{NodeID: "1", Role: axv("RootWebArea"), ChildIDs: []accessibility.NodeID{"2"}},
		{NodeID: "2", ParentID: "1", Role: axv("textbox"), Name: axv("Name"), Value: axv("abc"), ChildIDs: []accessibility.NodeID{"3"}},
		{NodeID: "3", ParentID: "2", Role: axv("generic"), ChildIDs: []accessibility.NodeID{"4"},
			Properties: []*accessibility.Property{{Name: "editable", Value: &accessibility.Value{Type: "token", Value: []byte(`"plaintext"`)}}}},
		{NodeID: "4", ParentID: "3", Role: axv("StaticText"), Name: axv("abc")},
	}
	got, _ := buildSnapshot(nodes)
	if len(got) != 1 || got[0].Role != "textbox" || got[0].Value != "abc" {
		t.Fatalf("want one textbox line, got %+v", got)
	}
}

func TestSnapshotDropsNoise(t *testing.T) {
	nodes := []*accessibility.Node{
		{NodeID: "1", Role: axv("RootWebArea"), ChildIDs: []accessibility.NodeID{"2", "3", "4", "5"}},
		{NodeID: "2", ParentID: "1", Role: axv("separator")},
		{NodeID: "3", ParentID: "1", Role: axv("ListMarker"), Name: axv("•")},
		{NodeID: "4", ParentID: "1", Role: axv("StaticText"), Name: axv("“")},
		{NodeID: "5", ParentID: "1", Role: axv("StaticText"), Name: axv("3 items left!")},
	}
	got, _ := buildSnapshot(nodes)
	if len(got) != 1 || got[0].Name != "3 items left!" {
		t.Fatalf("want only the real text, got %+v", got)
	}
}
