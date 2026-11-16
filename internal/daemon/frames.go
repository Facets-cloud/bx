package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// A tab can show other documents in iframes. The browser runs a cross-site
// or sandboxed iframe in its own process, with its own DevTools target
// (frame); JavaScript in the tab can't reach into it, and mouse events
// sent to the tab don't reliably arrive in it. Other iframes share their
// parent's process and target, but often not its JavaScript world (a
// different origin). So bx reaches every document through its target and
// frame, runs scripts on an element in the element's own window, and
// sends input to the element's target in that target's coordinates.

// docKey names one document of the tab: the target that holds it ("" for
// the tab itself) and its frame there ("" for the target's main frame).
type docKey struct {
	target target.ID
	frame  cdp.FrameID
}

// doc is a document of the tab, with the target that holds it.
type doc struct {
	key    docKey
	parent docKey // the document whose iframe shows this one
	t      *chromedp.Target
	f      *frame // the target's session for a frame in its own process; nil for the tab
}

// frame is a part of the tab that runs in its own process, attached
// through its own DevTools target.
type frame struct {
	id     target.ID
	parent target.ID // the tab, or the frame this one sits in
	ctx    context.Context
	cancel context.CancelFunc
	t      *chromedp.Target
}

// frameRef is where an element outside the tab's own page is: the target
// holding it (f, nil for the tab's), the element itself as a JavaScript
// object in its own window, and the offset of the target's viewport on the
// tab's page, in CSS pixels, as of the last time bx placed the element.
type frameRef struct {
	f      *frame
	t      *chromedp.Target
	obj    runtime.RemoteObjectID
	ox, oy float64
}

// on returns ctx moved to the element's target when that is a frame in its
// own process, so chromedp input actions go there; it ends when ctx ends.
func (e elem) on(ctx context.Context) context.Context {
	if e.fr == nil || e.fr.f == nil {
		return ctx
	}
	c, cancel := context.WithCancel(e.fr.f.ctx)
	context.AfterFunc(ctx, cancel)
	return c
}

// evalEl evaluates js, which may name the element as e.js does, in the
// element's own window, and decodes the result into T.
func evalEl[T any](ctx context.Context, e elem, js string) (T, error) {
	if e.fr == nil {
		return chromedp.Run(ctx, chromedp.Evaluate[T](js))
	}
	var out T
	yes := true
	res, err := cdp.Call(ctx, e.fr.t, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: "function() { return (" + js + "); }",
		ObjectID:            e.fr.obj, ReturnByValue: &yes, AwaitPromise: &yes,
	})
	if err != nil {
		return out, err
	}
	if res.ExceptionDetails != nil {
		return out, fmt.Errorf("%s", res.ExceptionDetails.Text)
	}
	if res.Result != nil && len(res.Result.Value) > 0 {
		err = json.Unmarshal(res.Result.Value, &out)
	}
	return out, err
}

// childFrames lists the tab's frames that run in their own process, nested
// ones too, each after its parent.
func (sess *tabSession) childFrames() ([]*target.Info, error) {
	all, err := chromedp.Targets(sess.bctx)
	if err != nil {
		return nil, upstream("list frames", err)
	}
	in := map[target.ID]bool{sess.id: true}
	var out []*target.Info
	for grew := true; grew; {
		grew = false
		for _, t := range all {
			if t.Type == "iframe" && in[t.ParentID] && !in[t.TargetID] {
				in[t.TargetID] = true
				out = append(out, t)
				grew = true
			}
		}
	}
	return out, nil
}

// frameSession returns the frame's attached session, attaching on first
// use. A frame that went away is attached again.
func (sess *tabSession) frameSession(info *target.Info) (*frame, error) {
	if f := sess.frames[info.TargetID]; f != nil && f.ctx.Err() == nil {
		return f, nil
	}
	fctx, cancel := chromedp.NewContext(sess.bctx, chromedp.WithTargetID(info.TargetID), chromedp.WithDetachOnCancel())
	f := &frame{id: info.TargetID, parent: info.ParentID, ctx: fctx, cancel: cancel}
	done := make(chan error, 1)
	go func() {
		_, err := chromedp.Run(fctx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			f.t = t
			// So a field in the frame takes focus while the tab is in the
			// background, as in the tab itself; best effort.
			_, _ = cdp.Call(ctx, t, emulation.SetFocusEmulationEnabled, emulation.SetFocusEmulationEnabledParams{Enabled: true})
			return nil
		}))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			cancel()
			return nil, upstream("attach to frame", err)
		}
	case <-time.After(10 * time.Second):
		cancel()
		return nil, upstream("attach to frame", context.DeadlineExceeded)
	}
	if sess.frames == nil {
		sess.frames = map[target.ID]*frame{}
	}
	sess.frames[info.TargetID] = f
	return f, nil
}

// docs lists the tab's documents: its own page first, then every frame's,
// each after the document that shows it. A frame bx can't attach to or
// read is left out.
func (sess *tabSession) docs(ctx context.Context) []doc {
	out := []doc{{t: sess.t}}
	infos, _ := sess.childFrames()
	own := map[cdp.FrameID]bool{} // frames with their own target
	for _, info := range infos {
		own[cdp.FrameID(info.TargetID)] = true
	}
	out = append(out, subframes(ctx, docKey{}, sess.t, nil, own)...)
	for _, info := range infos {
		f, err := sess.frameSession(info)
		if err != nil {
			continue
		}
		key := docKey{target: f.id}
		out = append(out, doc{key: key, parent: sess.keyOf(info.ParentID, info.ParentFrameID), t: f.t, f: f})
		out = append(out, subframes(ctx, key, f.t, f, own)...)
	}
	return out
}

// keyOf is the docKey of frame fid in target id.
func (sess *tabSession) keyOf(id target.ID, fid cdp.FrameID) docKey {
	k := docKey{target: id}
	if id == sess.id {
		k.target = ""
	}
	if fid != cdp.FrameID(id) { // a target's main frame has the target's ID
		k.frame = fid
	}
	return k
}

// subframes lists the frames in target t that share its process, below its
// main frame (main), each after its parent.
func subframes(ctx context.Context, main docKey, t *chromedp.Target, f *frame, own map[cdp.FrameID]bool) []doc {
	tree, err := cdp.Call(ctx, t, page.GetFrameTree, cdp.Empty{})
	if err != nil || tree.FrameTree == nil {
		return nil
	}
	var out []doc
	var walk func(n *page.FrameTree, parent docKey)
	walk = func(n *page.FrameTree, parent docKey) {
		for _, c := range n.ChildFrames {
			if c.Frame == nil || own[c.Frame.ID] {
				continue // a frame with its own target is listed by docs
			}
			k := docKey{target: main.target, frame: c.Frame.ID}
			out = append(out, doc{key: k, parent: parent, t: t, f: f})
			walk(c, k)
		}
	}
	walk(tree.FrameTree, main)
	return out
}

// frameID is the DevTools frame ID of document k in tab tab.
func (k docKey) frameID(tab target.ID) cdp.FrameID {
	switch {
	case k.frame != "":
		return k.frame
	case k.target != "":
		return cdp.FrameID(k.target)
	}
	return cdp.FrameID(tab)
}

// owner returns the iframe element, in the target parent, that shows
// document k.
func (sess *tabSession) owner(ctx context.Context, k docKey, parent *chromedp.Target) (cdp.BackendNodeID, error) {
	res, err := cdp.Call(ctx, parent, dom.GetFrameOwner, dom.GetFrameOwnerParams{FrameID: k.frameID(sess.id)})
	if err != nil {
		return 0, err
	}
	return res.BackendNodeID, nil
}

// targetOf is the DevTools target holding a document.
func (sess *tabSession) targetOf(k docKey) *chromedp.Target {
	if k.target == "" || k.target == sess.id {
		return sess.t
	}
	if f := sess.frames[k.target]; f != nil {
		return f.t
	}
	return nil
}

// frameTrees reads the accessibility tree of each of the tab's frames, for
// the snapshot, all at once. A page without frames costs two quick calls.
// A frame that can't be read is left out.
func (sess *tabSession) frameTrees(ctx context.Context) []frameTree {
	ds := sess.docs(ctx)[1:]
	got := make([]*frameTree, len(ds))
	var wg sync.WaitGroup
	for i, d := range ds {
		parent := sess.targetOf(d.parent)
		if parent == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			own, err := sess.owner(ctx, d.key, parent)
			if err != nil {
				return
			}
			tree, err := cdp.Call(ctx, d.t, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{FrameID: d.key.frame})
			if err != nil {
				return
			}
			got[i] = &frameTree{doc: d.key, parent: d.parent, owner: own, nodes: tree.Nodes}
		}()
	}
	wg.Wait()
	var trees []frameTree
	for _, t := range got {
		if t != nil {
			trees = append(trees, *t)
		}
	}
	return trees
}

// offset is where frame f's viewport starts on the tab's page, in CSS
// pixels: the sum of each iframe element's content box in its parent.
func (sess *tabSession) offset(ctx context.Context, f *frame) (x, y float64, err error) {
	for f != nil {
		parent := sess.targetOf(docKey{target: f.parent})
		own, err := sess.owner(ctx, docKey{target: f.id}, parent)
		if err != nil {
			return 0, 0, err
		}
		box, err := cdp.Call(ctx, parent, dom.GetBoxModel, dom.GetBoxModelParams{BackendNodeID: own})
		if err != nil || box.Model == nil || len(box.Model.Content) < 2 {
			return 0, 0, fmt.Errorf("frame %s: no box for its iframe", f.id)
		}
		x, y = x+box.Model.Content[0], y+box.Model.Content[1]
		f = sess.frames[f.parent]
	}
	return x, y, nil
}

// frameAt finds the innermost frame running in its own process that shows
// the tab's page point x, y (CSS pixels), and that point in the frame's
// own coordinates. It returns nil when the point is in the tab's target:
// the tab passes input on to frames that share its process.
func (sess *tabSession) frameAt(ctx context.Context, x, y float64) (f *frame, fx, fy float64) {
	infos, err := sess.childFrames()
	if err != nil {
		return nil, 0, 0
	}
	for _, info := range infos {
		c, err := sess.frameSession(info)
		if err != nil {
			continue
		}
		parent := sess.targetOf(docKey{target: c.parent})
		own, err := sess.owner(ctx, docKey{target: c.id}, parent)
		if err != nil {
			continue
		}
		ox, oy, err := sess.offset(ctx, c)
		if err != nil {
			continue
		}
		box, err := cdp.Call(ctx, parent, dom.GetBoxModel, dom.GetBoxModelParams{BackendNodeID: own})
		if err != nil || box.Model == nil {
			continue
		}
		if x >= ox && y >= oy && x < ox+float64(box.Model.Width) && y < oy+float64(box.Model.Height) {
			f, fx, fy = c, x-ox, y-oy // later frames are nested deeper
		}
	}
	return f, fx, fy
}

// dropFrames detaches from the tab's frames.
func (sess *tabSession) dropFrames() {
	for id, f := range sess.frames {
		f.cancel()
		delete(sess.frames, id)
	}
	sess.worlds = nil
}

// world returns bx's own JavaScript world in document d, where scripts see
// the document but not the page's globals: lookups there go unnoticed by
// the page and work whatever its origin. Worlds are kept until they fail.
func (sess *tabSession) world(ctx context.Context, d doc) (runtime.ExecutionContextID, error) {
	if id, ok := sess.worlds[d.key]; ok {
		return id, nil
	}
	res, err := cdp.Call(ctx, d.t, page.CreateIsolatedWorld, page.CreateIsolatedWorldParams{FrameID: d.key.frameID(sess.id), WorldName: "bx"})
	if err != nil {
		return 0, err
	}
	if sess.worlds == nil {
		sess.worlds = map[docKey]runtime.ExecutionContextID{}
	}
	sess.worlds[d.key] = res.ExecutionContextID
	return res.ExecutionContextID, nil
}

// evalIn evaluates js in bx's world in document d. A world that went with
// its document is made again, once.
func (sess *tabSession) evalIn(ctx context.Context, d doc, js string, byValue bool) (*runtime.RemoteObject, error) {
	for try := 0; ; try++ {
		id, err := sess.world(ctx, d)
		if err != nil {
			return nil, err
		}
		res, err := cdp.Call(ctx, d.t, runtime.Evaluate, runtime.EvaluateParams{Expression: js, ContextID: id, ReturnByValue: &byValue})
		if err == nil && res.ExceptionDetails != nil {
			err = fmt.Errorf("%s", res.ExceptionDetails.Text)
		}
		if err == nil {
			return res.Result, nil
		}
		if try == 1 {
			return nil, err
		}
		delete(sess.worlds, d.key)
	}
}

// inFrames reports whether the boolean expression js holds in any of the
// tab's frames (not the tab's own page).
func (sess *tabSession) inFrames(ctx context.Context, js string) bool {
	for _, d := range sess.docs(ctx)[1:] {
		res, err := sess.evalIn(ctx, d, js, true)
		if err == nil && res != nil && string(res.Value) == "true" {
			return true
		}
	}
	return false
}

// findCSS finds the first element matching a CSS selector in the tab's
// frames: the document holding it and its node.
func (sess *tabSession) findCSS(ctx context.Context, sel string) (nodeKey, bool) {
	q, _ := json.Marshal(sel)
	for _, d := range sess.docs(ctx)[1:] {
		res, err := sess.evalIn(ctx, d, fmt.Sprintf(deepQueryJS, q), false)
		if err != nil || res == nil || res.ObjectID == "" {
			continue
		}
		node, err := cdp.Call(ctx, d.t, dom.DescribeNode, dom.DescribeNodeParams{ObjectID: res.ObjectID})
		if err == nil && node.Node != nil {
			return nodeKey{d.key, node.Node.BackendNodeID}, true
		}
	}
	return nodeKey{}, false
}

// docObject is document d's document node, for accessibility queries.
func (sess *tabSession) docObject(ctx context.Context, d doc) (runtime.RemoteObjectID, error) {
	res, err := sess.evalIn(ctx, d, "document", false)
	if err != nil || res == nil || res.ObjectID == "" {
		return "", fmt.Errorf("no document: %v", err)
	}
	return res.ObjectID, nil
}

// frameWindow is the window of a frame that shares its parent's process,
// in the page's own JavaScript world (not bx's), for eval.
func (sess *tabSession) frameWindow(ctx context.Context, d doc) (runtime.RemoteObjectID, error) {
	own, err := sess.owner(ctx, d.key, d.t)
	if err != nil {
		return "", err
	}
	depth, yes := int64(1), true
	n, err := cdp.Call(ctx, d.t, dom.DescribeNode, dom.DescribeNodeParams{BackendNodeID: own, Depth: &depth, Pierce: &yes})
	if err != nil || n.Node == nil || n.Node.ContentDocument == nil {
		return "", fmt.Errorf("no document in the frame: %v", err)
	}
	obj, err := cdp.Call(ctx, d.t, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: n.Node.ContentDocument.BackendNodeID})
	if err != nil || obj.Object == nil {
		return "", fmt.Errorf("no document in the frame: %v", err)
	}
	res, err := cdp.Call(ctx, d.t, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		FunctionDeclaration: "function() { return this.defaultView; }", ObjectID: obj.Object.ObjectID})
	if err != nil || res.Result == nil || res.Result.ObjectID == "" {
		return "", fmt.Errorf("no window in the frame: %v", err)
	}
	return res.Result.ObjectID, nil
}

// placeJS scrolls an element into view when it is out of it (the scroll
// carries on through the frames around it) and returns the name the
// pointer's tag shows for it.
const placeJS = `(el => {
	const r = el.getBoundingClientRect();
	if (r.top < 0 || r.bottom > innerHeight || r.left < 0 || r.right > innerWidth) el.scrollIntoView({block: "center", inline: "nearest"});
	const label = el.labels && el.labels[0] ? el.labels[0].innerText : "";
	const name = (el.getAttribute && (el.getAttribute("aria-label") || el.placeholder || el.title)) || label || el.innerText || "";
	return String(name).trim().slice(0, 80);
})(%s)`

// boxOf is an element's box in its target's viewport: x, y, width, height.
func boxOf(ctx context.Context, e elem) ([4]float64, error) {
	res, err := cdp.Call(ctx, e.fr.t, dom.GetContentQuads, dom.GetContentQuadsParams{ObjectID: e.fr.obj})
	if err != nil || len(res.Quads) == 0 || len(res.Quads[0]) < 8 {
		return [4]float64{}, fmt.Errorf("%s has no box on the page (hidden?): %v", e.name, err)
	}
	q := res.Quads[0]
	x0, y0, x1, y1 := q[0], q[1], q[0], q[1]
	for i := 0; i < 8; i += 2 {
		x0, x1 = min(x0, q[i]), max(x1, q[i])
		y0, y1 = min(y0, q[i+1]), max(y1, q[i+1])
	}
	return [4]float64{x0, y0, x1 - x0, y1 - y0}, nil
}

// place readies an element outside the tab's own page for the pointer: it
// scrolls it into view, updates e.fr's offset, and returns the element's
// box on the tab's page and a JavaScript stand-in for the element that
// the tab's pointer code can aim at.
func (sess *tabSession) place(ctx context.Context, e elem) (box [4]float64, stand string, err error) {
	name, err := evalEl[string](ctx, e, fmt.Sprintf(placeJS, e.js))
	if err != nil {
		return box, "", fmt.Errorf("place %s: %v", e.name, err)
	}
	if box, err = boxOf(ctx, e); err != nil {
		return box, "", err
	}
	e.fr.ox, e.fr.oy = 0, 0
	if e.fr.f != nil {
		if e.fr.ox, e.fr.oy, err = sess.offset(ctx, e.fr.f); err != nil {
			return box, "", err
		}
	}
	box[0], box[1] = box[0]+e.fr.ox, box[1]+e.fr.oy
	n, _ := json.Marshal(name)
	stand = fmt.Sprintf(`({getBoundingClientRect: () => new DOMRect(%f, %f, %f, %f), scrollIntoView() {}, getAttribute: () => null, innerText: %s})`,
		box[0], box[1], box[2], box[3], n)
	return box, stand, nil
}

// centerOf scrolls an element outside the tab's own page into view and
// returns its middle in its target's coordinates.
func centerOf(ctx context.Context, e elem) (float64, float64, error) {
	if _, err := evalEl[string](ctx, e, fmt.Sprintf(placeJS, e.js)); err != nil {
		return 0, 0, err
	}
	b, err := boxOf(ctx, e)
	return b[0] + b[2]/2, b[1] + b[3]/2, err
}

// clickIn clicks an element outside the tab's own page at its middle,
// through its target.
func clickIn(ctx context.Context, e elem, double bool) error {
	x, y, err := centerOf(ctx, e)
	if err != nil {
		return err
	}
	return clickAt(ctx, e.fr.t, x, y, double)
}

// clickAt presses and releases the left button at x, y of target t.
func clickAt(ctx context.Context, t *chromedp.Target, x, y float64, double bool) error {
	ev := func(typ input.DispatchMouseEventType, buttons int64, n int) error {
		p := input.DispatchMouseEventParams{Type: typ, X: x, Y: y, Buttons: buttons, ClickCount: int64(n)}
		if typ != input.DispatchMouseEventTypeMouseMoved {
			p.Button = input.MouseButtonLeft
		}
		_, err := cdp.Call(ctx, t, input.DispatchMouseEvent, p)
		return err
	}
	if err := ev(input.DispatchMouseEventTypeMouseMoved, 0, 0); err != nil {
		return err
	}
	clicks := 1
	if double {
		clicks = 2
	}
	for n := 1; n <= clicks; n++ {
		if err := ev(input.DispatchMouseEventTypeMousePressed, 1, n); err != nil {
			return err
		}
		if err := ev(input.DispatchMouseEventTypeMouseReleased, 0, n); err != nil {
			return err
		}
	}
	return nil
}

// shownJS reports whether an element is on the page and has a box.
const shownJS = `(e => !!e && e.isConnected && e.getClientRects().length > 0)(%s)`

// waitShown waits until an element outside the tab's own page has a box.
func waitShown(ctx context.Context, e elem) error {
	for {
		if ok, err := evalEl[bool](ctx, e, fmt.Sprintf(shownJS, e.js)); err == nil && ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// clipOf is a screenshot of the tab clipped to an element outside the
// tab's own page: a frame's target can't take one.
func (sess *tabSession) clipOf(ctx context.Context, e elem) (chromedp.Action[[]byte], error) {
	box, _, err := sess.place(ctx, e)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, t *chromedp.Target) ([]byte, error) {
		r, err := cdp.Call(ctx, t, page.CaptureScreenshot, page.CaptureScreenshotParams{
			Format: page.CaptureScreenshotFormatPng,
			Clip:   &page.Viewport{X: box[0], Y: box[1], Width: box[2], Height: box[3], Scale: 1},
		})
		if err != nil {
			return nil, err
		}
		return r.Data, nil
	}, nil
}
