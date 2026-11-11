package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
)

// SnapNode is one line of a snapshot: an element with a ref that click,
// fill and the other actions accept in place of a CSS selector.
type SnapNode struct {
	Ref   string   `json:"ref"`
	Depth int      `json:"depth"`
	Role  string   `json:"role"`
	Name  string   `json:"name,omitempty"`
	Value string   `json:"value,omitempty"`
	Props []string `json:"props,omitempty"`
}

// Snapshot is the result of `bx snapshot`.
type Snapshot struct {
	URL   string     `json:"url"`
	Title string     `json:"title"`
	Nodes []SnapNode `json:"nodes"`
}

// skipRoles carry no meaning for an agent; their children are kept.
var skipRoles = map[string]bool{
	"generic": true, "none": true, "presentation": true, "InlineTextBox": true,
	"LineBreak": true, "RootWebArea": true, "paragraph": true, "group": true,
	"Section": true, "LayoutTable": true, "LayoutTableRow": true, "LayoutTableCell": true,
	"LabelText": true, "MenuListPopup": true, "ListMarker": true, "separator": true,
}

// boolProps are shown when true, such as [checked] or [disabled].
var boolProps = map[string]bool{
	"focused": true, "disabled": true, "checked": true, "expanded": true,
	"selected": true, "required": true, "readonly": true, "pressed": true,
	"modal": true, "multiselectable": true,
}

// buildSnapshot turns an accessibility tree into numbered lines. It returns
// the lines and a map from ref to the DOM node it names.
func buildSnapshot(nodes []*accessibility.Node) ([]SnapNode, map[string]cdp.BackendNodeID) {
	byID := map[accessibility.NodeID]*accessibility.Node{}
	var root *accessibility.Node
	for _, n := range nodes {
		byID[n.NodeID] = n
		if n.ParentID == "" && root == nil {
			root = n
		}
	}
	b := snapBuilder{byID: byID, refs: map[string]cdp.BackendNodeID{}}
	if root != nil {
		b.walk(root, 0, "")
	}
	return b.out, b.refs
}

type snapBuilder struct {
	byID map[accessibility.NodeID]*accessibility.Node
	out  []SnapNode
	refs map[string]cdp.BackendNodeID
}

func (b *snapBuilder) walk(n *accessibility.Node, depth int, parentName string) {
	role, name := axString(n.Role), axString(n.Name)
	if role == "generic" && editable(n) {
		role = "textbox" // a contenteditable element
	}
	keep := !n.Ignored && !skipRoles[role] && role != ""
	// Text that repeats its parent's name (a link's own label) adds nothing.
	if role == "StaticText" && (!hasWord(name) || name == parentName) {
		keep = false
	}
	childDepth, childParent := depth, parentName
	if keep && leafRoles[role] {
		// A text field's inner editor and text repeat its value.
		b.add(n, depth, role, name)
		return
	}
	if keep {
		b.add(n, depth, role, name)
		childDepth, childParent = depth+1, name
	}
	for _, id := range n.ChildIDs {
		if c := b.byID[id]; c != nil {
			b.walk(c, childDepth, childParent)
		}
	}
}

// hasWord reports whether s has a letter or digit; lone punctuation such
// as a quote mark or a bullet says nothing.
func hasWord(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// editable reports a contenteditable element.
func editable(n *accessibility.Node) bool {
	for _, p := range n.Properties {
		if p.Name == "editable" && axString(p.Value) != "" {
			return true
		}
	}
	return false
}

// leafRoles show their value on their own line, so their children are
// left out.
var leafRoles = map[string]bool{"textbox": true, "searchbox": true, "spinbutton": true}

func (b *snapBuilder) add(n *accessibility.Node, depth int, role, name string) {
	ref := fmt.Sprintf("e%d", len(b.out)+1)
	if n.BackendDOMNodeID != 0 {
		b.refs[ref] = n.BackendDOMNodeID
	}
	b.out = append(b.out, SnapNode{Ref: ref, Depth: depth, Role: role, Name: clipText(name, 120),
		Value: clipText(axString(n.Value), 120), Props: axProps(n)})
}

// axString reads an accessibility value as text.
func axString(v *accessibility.Value) string {
	if v == nil || len(v.Value) == 0 {
		return ""
	}
	var x any
	if err := json.Unmarshal([]byte(v.Value), &x); err != nil {
		return ""
	}
	switch t := x.(type) {
	case string:
		return strings.Join(strings.Fields(t), " ")
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

func axProps(n *accessibility.Node) []string {
	var out []string
	for _, p := range n.Properties {
		name := string(p.Name)
		val := axString(p.Value)
		switch {
		case boolProps[name] && (val == "true" || val == "mixed"):
			if val == "mixed" {
				name += "=mixed"
			}
			out = append(out, name)
		case name == "level" && val != "":
			out = append(out, "level="+val)
		}
	}
	return out
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Text renders the snapshot as indented lines, such as
// `  e12 button "Save" [disabled]`.
func (s Snapshot) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n", s.Title, s.URL)
	for _, n := range s.Nodes {
		b.WriteString(strings.Repeat("  ", n.Depth))
		fmt.Fprintf(&b, "%s %s", n.Ref, n.Role)
		if n.Name != "" {
			fmt.Fprintf(&b, " %q", n.Name)
		}
		if n.Value != "" && n.Value != n.Name {
			fmt.Fprintf(&b, " value=%q", n.Value)
		}
		for _, p := range n.Props {
			fmt.Fprintf(&b, " [%s]", p)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
