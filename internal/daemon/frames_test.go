package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
)

// A page with a button, and an iframe (backend node 20) whose page runs in
// its own process (target F1): a button and a nested iframe (backend 20
// too: node IDs repeat across targets) whose page shares F1's process
// (frame S1) and holds a link.
func framedPage() ([]*accessibility.Node, []frameTree) {
	tab := []*accessibility.Node{
		{NodeID: "1", Role: axv("RootWebArea"), ChildIDs: []accessibility.NodeID{"2", "3"}},
		{NodeID: "2", ParentID: "1", Role: axv("button"), Name: axv("Top"), BackendDOMNodeID: 10},
		{NodeID: "3", ParentID: "1", Role: axv("Iframe"), BackendDOMNodeID: 20},
	}
	inner := []*accessibility.Node{
		{NodeID: "1", Role: axv("RootWebArea"), ChildIDs: []accessibility.NodeID{"2", "3"}},
		{NodeID: "2", ParentID: "1", Role: axv("button"), Name: axv("Mark"), BackendDOMNodeID: 10},
		{NodeID: "3", ParentID: "1", Role: axv("Iframe"), BackendDOMNodeID: 20},
	}
	nested := []*accessibility.Node{
		{NodeID: "1", Role: axv("RootWebArea"), ChildIDs: []accessibility.NodeID{"2"}},
		{NodeID: "2", ParentID: "1", Role: axv("link"), Name: axv("Deep"), BackendDOMNodeID: 10},
	}
	return tab, []frameTree{
		{doc: f1, owner: 20, nodes: inner},
		{doc: s1, parent: f1, owner: 20, nodes: nested},
	}
}

var (
	f1 = docKey{target: "F1"}
	s1 = docKey{target: "F1", frame: "S1"}
)

func TestSnapshotNestsFrames(t *testing.T) {
	tab, frames := framedPage()
	got, refs := buildSnapshot(tab, refState{}, frames...)
	want := []struct {
		ref, role, name string
		depth           int
	}{
		{"e1", "button", "Top", 0},
		{"e2", "Iframe", "", 0},
		{"e3", "button", "Mark", 1},
		{"e4", "Iframe", "", 1},
		{"e5", "link", "Deep", 2},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Ref != w.ref || g.Role != w.role || g.Name != w.name || g.Depth != w.depth {
			t.Errorf("line %d: got %+v, want %+v", i, g, w)
		}
	}
	// The same backend node ID in three documents is three elements.
	for ref, d := range map[string]docKey{"e1": {}, "e3": f1, "e5": s1} {
		if refs.refs[ref] != cdp.BackendNodeID(10) || refs.in[ref] != d {
			t.Errorf("%s: backend %d in %v, want 10 in %v", ref, refs.refs[ref], refs.in[ref], d)
		}
	}
	// Each iframe's ref leads to the document it shows, for eval --frame.
	if refs.shows["e2"] != f1 || refs.shows["e4"] != s1 {
		t.Errorf("shows: %v", refs.shows)
	}
}

func TestFrameRefsStayPut(t *testing.T) {
	tab, frames := framedPage()
	_, first := buildSnapshot(tab, refState{}, frames...)
	// The next snapshot sees the frames in the other order: refs follow
	// the element, not the order.
	got, _ := buildSnapshot(tab, first, frames[1], frames[0])
	refOf := map[string]string{}
	for _, n := range got {
		refOf[n.Name] = n.Ref
	}
	if refOf["Mark"] != "e3" || refOf["Deep"] != "e5" || refOf["Top"] != "e1" {
		t.Errorf("refs moved: %v", refOf)
	}
}

func TestSnapshotWithoutFrameTree(t *testing.T) {
	// A frame bx couldn't read leaves its iframe line empty, nothing more.
	tab, _ := framedPage()
	got, refs := buildSnapshot(tab, refState{})
	if len(got) != 2 || len(refs.in) != 0 {
		t.Errorf("got %+v, in %v", got, refs.in)
	}
}

func TestOnEndsWithCaller(t *testing.T) {
	fctx, fcancel := context.WithCancel(context.Background())
	defer fcancel()
	e := elem{fr: &frameRef{f: &frame{ctx: fctx}}}
	sub := elem{fr: &frameRef{}} // a frame sharing the tab's process: input goes to the tab
	ctx, cancel := context.WithCancel(context.Background())
	in := e.on(ctx)
	cancel()
	select {
	case <-in.Done():
	case <-time.After(time.Second):
		t.Fatal("the frame context outlived the caller's")
	}
	if fctx.Err() != nil {
		t.Fatal("ending one action ended the frame's session")
	}
	if (elem{}).on(ctx) != ctx || sub.on(ctx) != ctx {
		t.Fatal("an element in the tab's target should keep the caller's context")
	}
}

func TestDocKeys(t *testing.T) {
	sess := &tabSession{id: "TAB"}
	cases := []struct {
		id   string
		fid  string
		want docKey
	}{
		{"TAB", "TAB", docKey{}},                        // the tab's own page
		{"TAB", "S9", docKey{frame: "S9"}},              // a frame sharing the tab's process
		{"F1", "F1", docKey{target: "F1"}},              // a frame's own target
		{"F1", "S1", docKey{target: "F1", frame: "S1"}}, // a frame inside that one
	}
	for _, c := range cases {
		k := sess.keyOf(target.ID(c.id), cdp.FrameID(c.fid))
		if k != c.want {
			t.Errorf("keyOf(%s, %s) = %v, want %v", c.id, c.fid, k, c.want)
		}
		if got := string(k.frameID("TAB")); got != c.fid {
			t.Errorf("%v.frameID = %s, want %s", k, got, c.fid)
		}
	}
}
