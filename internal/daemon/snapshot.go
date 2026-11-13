package daemon

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"

	"github.com/Facets-cloud/bx/internal/exit"
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

// refState carries refs from one snapshot of a page to the next, so an
// element keeps its ref while it stays on the page.
type refState struct {
	refs map[string]cdp.BackendNodeID
	next int // the number the next new ref gets
}

// buildSnapshot turns an accessibility tree into numbered lines. An element
// that prev already numbered keeps its ref; new ones get new numbers. It
// returns the lines and the refs for the next snapshot.
func buildSnapshot(nodes []*accessibility.Node, prev refState) ([]SnapNode, refState) {
	byID := map[accessibility.NodeID]*accessibility.Node{}
	var root *accessibility.Node
	for _, n := range nodes {
		byID[n.NodeID] = n
		if n.ParentID == "" && root == nil {
			root = n
		}
	}
	b := snapBuilder{byID: byID, refs: map[string]cdp.BackendNodeID{}, old: map[cdp.BackendNodeID]string{}, next: max(prev.next, 1)}
	for ref, id := range prev.refs {
		b.old[id] = ref
	}
	if root != nil {
		b.walk(root, 0, "", "")
	}
	return b.out, refState{refs: b.refs, next: b.next}
}

type snapBuilder struct {
	byID map[accessibility.NodeID]*accessibility.Node
	out  []SnapNode
	refs map[string]cdp.BackendNodeID
	old  map[cdp.BackendNodeID]string // refs from the previous snapshot
	next int
}

func (b *snapBuilder) walk(n *accessibility.Node, depth int, parentName, parentRole string) {
	role, name := axString(n.Role), axString(n.Name)
	if role == "generic" && editable(n) {
		role = "textbox" // a contenteditable element
	}
	keep := !n.Ignored && !skipRoles[role] && role != ""
	// Text that repeats its parent's name (a link's own label) adds nothing.
	if role == "StaticText" && (!hasWord(name) || name == parentName) {
		keep = false
	}
	// A text combobox's inner editor repeats the combobox.
	if role == "textbox" && name == "" && parentRole == "combobox" {
		return
	}
	childDepth, childParent, childRole := depth, parentName, parentRole
	if keep && leafRoles[role] {
		// A text field's inner editor and text repeat its value.
		b.add(n, depth, role, name)
		return
	}
	if keep {
		b.add(n, depth, role, name)
		childDepth, childParent, childRole = depth+1, name, role
	}
	for _, id := range n.ChildIDs {
		if c := b.byID[id]; c != nil {
			b.walk(c, childDepth, childParent, childRole)
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
var leafRoles = map[string]bool{"textbox": true, "searchbox": true, "spinbutton": true,
	"Date": true, "DateTime": true, "InputTime": true} // a date field's day, month and year parts are fill's job

func (b *snapBuilder) add(n *accessibility.Node, depth int, role, name string) {
	ref, ok := b.old[n.BackendDOMNodeID]
	if _, taken := b.refs[ref]; !ok || taken || n.BackendDOMNodeID == 0 {
		ref = fmt.Sprintf("e%d", b.next)
		b.next++
	}
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
		b.WriteString(line(n))
		b.WriteByte('\n')
	}
	return b.String()
}

// actionRoles are the roles an agent can act on; an interactive snapshot
// keeps these and headings, which say where on the page they sit.
var actionRoles = map[string]bool{
	"link": true, "button": true, "textbox": true, "searchbox": true, "combobox": true,
	"checkbox": true, "radio": true, "switch": true, "slider": true, "spinbutton": true,
	"tab": true, "menuitem": true, "menuitemcheckbox": true, "menuitemradio": true,
	"option": true, "listbox": true, "treeitem": true, "Date": true, "DateTime": true,
	"InputTime": true, "gridcell": true, "columnheader": true, "rowheader": true, "heading": true, "dialog": true, "alertdialog": true, "alert": true,
}

// Filter narrows a snapshot. within keeps only the subtree under that ref;
// interactive keeps only actionRoles, unindented. Refs stay the same, so a
// filtered snapshot's refs work like the full one's.
func (s Snapshot) Filter(within string, interactive bool) (Snapshot, error) {
	nodes := s.Nodes
	if within != "" {
		sub, ok := subtree(nodes, within)
		if !ok {
			return s, exit.New(exit.User, "%s is not in this snapshot", within)
		}
		nodes = sub
	}
	if interactive {
		var kept []SnapNode
		for _, n := range nodes {
			if actionRoles[n.Role] {
				n.Depth = 0
				kept = append(kept, n)
			}
		}
		nodes = kept
	}
	s.Nodes = nodes
	return s, nil
}

// subtree returns the node with ref and everything under it, re-indented
// so the node sits at depth 0.
func subtree(nodes []SnapNode, ref string) ([]SnapNode, bool) {
	start := slices.IndexFunc(nodes, func(n SnapNode) bool { return n.Ref == ref })
	if start < 0 {
		return nil, false
	}
	end, base := start+1, nodes[start].Depth
	for end < len(nodes) && nodes[end].Depth > base {
		end++
	}
	out := slices.Clone(nodes[start:end])
	for i := range out {
		out[i].Depth -= base
	}
	return out, true
}
