package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
)

// A page with a button, and an iframe (backend node 20) whose page runs in
// its own process: a button and a nested iframe (backend 20 too: node IDs
// repeat across processes) holding a link.
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
		{id: "F1", owner: 20, nodes: inner},
		{id: "F2", parent: "F1", owner: 20, nodes: nested},
	}
}

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
	// The same backend node ID in three processes is three elements.
	for ref, frame := range map[string]string{"e1": "", "e3": "F1", "e5": "F2"} {
		if refs.refs[ref] != cdp.BackendNodeID(10) || string(refs.frames[ref]) != frame {
			t.Errorf("%s: backend %d in frame %q, want 10 in %q", ref, refs.refs[ref], refs.frames[ref], frame)
		}
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
	if len(got) != 2 || len(refs.frames) != 0 {
		t.Errorf("got %+v, frames %v", got, refs.frames)
	}
}

func TestOnEndsWithCaller(t *testing.T) {
	fctx, fcancel := context.WithCancel(context.Background())
	defer fcancel()
	e := elem{fr: &frameRef{f: &frame{ctx: fctx}}}
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
	if (elem{}).on(ctx) != ctx {
		t.Fatal("an element in the tab itself should keep the caller's context")
	}
}
