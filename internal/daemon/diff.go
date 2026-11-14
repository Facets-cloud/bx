package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
)

// takeSnapshot reads the accessibility tree, keeping the refs of elements
// still on the page from the last snapshot of the same document, and
// remembers the result. The caller holds sess.mu.
func (sess *tabSession) takeSnapshot(ctx context.Context) (Snapshot, error) {
	tree, err := cdp.Call(ctx, sess.t, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{})
	if err != nil {
		return Snapshot{}, upstream("snapshot", err)
	}
	loader := sess.loaderID(ctx)
	prev := sess.refs
	if loader != sess.refLoader {
		prev = refState{} // a new document: number from e1 again
	}
	nodes, refs := buildSnapshot(tree.Nodes, prev)
	sess.refs, sess.refLoader, sess.snap = refs, loader, nodes
	named := make(map[string]string, len(nodes))
	for _, n := range nodes {
		if n.Ref != "" {
			named[n.Ref] = roleName(n)
		}
	}
	sess.named.Store(named)
	url, title := sess.location(ctx)
	return Snapshot{URL: url, Title: title, Nodes: nodes}, nil
}

// maxChanges caps the change lines an action reports.
const maxChanges = 40

// changes reports what an action changed on the page since the tab's last
// snapshot: elements added (+), removed (-) and changed (~). When there is
// nothing to compare with (no snapshot yet, or a new document) or the diff
// would be long, it returns the interactive snapshot instead.
func (s *server) changes(id string) []string {
	sess, err := s.lockSession(id)
	if err != nil {
		return nil
	}
	defer sess.mu.Unlock()
	if sess.pendingDialog() != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(sess.ctx, 5*time.Second)
	defer cancel()
	old, oldLoader := sess.snap, sess.refLoader
	snap, err := sess.takeSnapshot(ctx)
	if err != nil {
		return nil
	}
	if old == nil {
		return screen("what you can act on now:", snap)
	}
	if sess.refLoader != oldLoader {
		return screen("new page loaded, refs start again; what you can act on now:", snap)
	}
	if d := diffNodes(old, snap.Nodes); len(d) <= maxChanges {
		return d
	}
	return screen("the page changed a lot; what you can act on now:", snap)
}

// screen is the interactive snapshot as change lines, for when a diff
// would be longer than the page itself: it saves the agent a snapshot call.
func screen(note string, snap Snapshot) []string {
	inter, _ := snap.Filter("", true)
	out := []string{note}
	for _, n := range inter.Nodes {
		out = append(out, line(n))
	}
	return out
}

// diffNodes lists the nodes of b not in a (+), of a not in b (-), and in
// both with a different name, value or state (~), by ref. Apps that redraw
// a whole region replace elements that look the same: a replaced control
// is reported as a new ref for the old one (=), and replaced text that
// reads the same is left out.
func diffNodes(a, b []SnapNode) []string {
	before := map[string]SnapNode{}
	for _, n := range a {
		before[n.Ref] = n
	}
	inB := map[string]bool{}
	for _, n := range b {
		inB[n.Ref] = true
	}
	gone := map[string][]string{} // look of a removed node -> its refs, in order
	for _, n := range a {
		if !inB[n.Ref] {
			gone[look(n)] = append(gone[look(n)], n.Ref)
		}
	}
	var out, moved []string
	for _, n := range b {
		o, ok := before[n.Ref]
		switch {
		case !ok && len(gone[look(n)]) > 0:
			was := gone[look(n)][0]
			gone[look(n)] = gone[look(n)][1:]
			if actionRoles[n.Role] {
				moved = append(moved, n.Ref+"="+was)
			}
		case !ok:
			out = append(out, "+ "+line(n))
		case o.Name != n.Name || o.Value != n.Value || strings.Join(o.Props, ",") != strings.Join(n.Props, ","):
			out = append(out, "~ "+line(n))
		}
	}
	var removed []string
	for _, n := range a {
		if refs := gone[look(n)]; len(refs) > 0 && refs[0] == n.Ref {
			removed = append(removed, n.Ref)
			gone[look(n)] = refs[1:]
		}
	}
	if len(removed) > 0 {
		out = append(out, "- "+strings.Join(removed, " "))
	}
	if len(moved) > 0 {
		out = append(out, "= redrawn, new ref=old ref: "+strings.Join(moved, " "))
	}
	return out
}

// look is what a node shows, for matching a redrawn node to the one it
// replaced.
func look(n SnapNode) string {
	return n.Role + "\x00" + n.Name + "\x00" + n.Value + "\x00" + strings.Join(n.Props, ",")
}

// line renders one node the way Snapshot.Text does, without indent.
// roleName is an element as role and name, the way an agent can name it
// again on another day: button "Next".
func roleName(n SnapNode) string {
	if n.Name == "" {
		return n.Role
	}
	return fmt.Sprintf("%s %q", n.Role, n.Name)
}

func line(n SnapNode) string {
	var b strings.Builder
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
	return b.String()
}
