package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// frame is a part of a tab that the browser runs in its own process: a
// cross-site or sandboxed iframe. JavaScript in the tab can't reach into
// it, and mouse events sent to the tab don't reliably arrive in it, so bx
// works in it through its own DevTools target: scripts run there, and
// input goes there in the frame's own coordinates.
type frame struct {
	id     target.ID
	parent target.ID // the tab, or the frame this one sits in
	ctx    context.Context
	cancel context.CancelFunc
	t      *chromedp.Target
}

// frameRef is where an element in a frame is: the frame, and the offset
// of the frame's top left corner on the tab's page, in CSS pixels, as of
// the last time bx placed the element (see place).
type frameRef struct {
	f      *frame
	ox, oy float64
}

// on returns ctx moved into the element's frame when it is in one, so
// chromedp actions on the element run there; it ends when ctx ends.
func (e elem) on(ctx context.Context) context.Context {
	if e.fr == nil {
		return ctx
	}
	c, cancel := context.WithCancel(e.fr.f.ctx)
	context.AfterFunc(ctx, cancel)
	return c
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
// use. A frame that went away (its page navigated) is attached again.
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

// attachedFrames attaches to every frame of the tab that runs in its own
// process and returns them, each after its parent. A frame bx can't attach
// to is left out.
func (sess *tabSession) attachedFrames() []*frame {
	infos, err := sess.childFrames()
	if err != nil {
		return nil
	}
	var out []*frame
	for _, info := range infos {
		if f, err := sess.frameSession(info); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// targetOf is the DevTools target of the tab or of one of its frames.
func (sess *tabSession) targetOf(id target.ID) *chromedp.Target {
	if id == "" || id == sess.id {
		return sess.t
	}
	if f := sess.frames[id]; f != nil {
		return f.t
	}
	return nil
}

// owner returns the iframe element, in the frame's parent, that shows it.
func (sess *tabSession) owner(ctx context.Context, f *frame) (cdp.BackendNodeID, error) {
	p := sess.targetOf(f.parent)
	if p == nil {
		return 0, fmt.Errorf("frame %s: its parent is not attached", f.id)
	}
	res, err := cdp.Call(ctx, p, dom.GetFrameOwner, dom.GetFrameOwnerParams{FrameID: cdp.FrameID(f.id)})
	if err != nil {
		return 0, err
	}
	return res.BackendNodeID, nil
}

// frameTrees reads the accessibility tree of each of the tab's frames that
// runs in its own process, for the snapshot. A frame that can't be read is
// left out.
func (sess *tabSession) frameTrees(ctx context.Context) []frameTree {
	var out []frameTree
	for _, f := range sess.attachedFrames() {
		owner, err := sess.owner(ctx, f)
		if err != nil {
			continue
		}
		tree, err := cdp.Call(ctx, f.t, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{})
		if err != nil {
			continue
		}
		parent := f.parent
		if parent == sess.id {
			parent = ""
		}
		out = append(out, frameTree{id: f.id, parent: parent, owner: owner, nodes: tree.Nodes})
	}
	return out
}

// offset is where the frame's content starts on the tab's page, in CSS
// pixels: the sum of each iframe element's content box in its parent.
func (sess *tabSession) offset(ctx context.Context, f *frame) (x, y float64, err error) {
	for f != nil {
		owner, err := sess.owner(ctx, f)
		if err != nil {
			return 0, 0, err
		}
		box, err := cdp.Call(ctx, sess.targetOf(f.parent), dom.GetBoxModel, dom.GetBoxModelParams{BackendNodeID: owner})
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
// own coordinates. It returns nil when the point is in the tab itself.
func (sess *tabSession) frameAt(ctx context.Context, x, y float64) (f *frame, fx, fy float64) {
	for _, c := range sess.attachedFrames() {
		owner, err := sess.owner(ctx, c)
		if err != nil {
			continue
		}
		ox, oy, err := sess.offset(ctx, c)
		if err != nil {
			continue
		}
		box, err := cdp.Call(ctx, sess.targetOf(c.parent), dom.GetBoxModel, dom.GetBoxModelParams{BackendNodeID: owner})
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
}

// placeJS scrolls an element into view when it is out of it (the scroll
// carries on through the frames around it) and returns its box and the
// name the pointer's tag shows for it.
const placeJS = `(el => {
	const r0 = el.getBoundingClientRect();
	if (r0.top < 0 || r0.bottom > innerHeight || r0.left < 0 || r0.right > innerWidth) el.scrollIntoView({block: "center", inline: "nearest"});
	const r = el.getBoundingClientRect();
	const label = el.labels && el.labels[0] ? el.labels[0].innerText : "";
	const name = (el.getAttribute && (el.getAttribute("aria-label") || el.placeholder || el.title)) || label || el.innerText || "";
	return [r.x, r.y, r.width, r.height, String(name).trim().slice(0, 80)];
})(%s)`

// place readies an element in a frame for the pointer: it scrolls it into
// view, updates e.fr's offset, and returns the element's box on the tab's
// page (x, y, width, height) and a JavaScript stand-in for the element
// that the tab's pointer code can aim at.
func (sess *tabSession) place(ctx context.Context, e elem) (box [4]float64, stand string, err error) {
	got, err := chromedp.Run(e.on(ctx), chromedp.Evaluate[[]any](fmt.Sprintf(placeJS, e.js)))
	if err != nil || len(got) != 5 {
		return box, "", fmt.Errorf("place %s: %v", e.name, err)
	}
	ox, oy, err := sess.offset(ctx, e.fr.f)
	if err != nil {
		return box, "", err
	}
	e.fr.ox, e.fr.oy = ox, oy
	num := func(v any) float64 { f, _ := v.(float64); return f }
	box = [4]float64{ox + num(got[0]), oy + num(got[1]), num(got[2]), num(got[3])}
	name, _ := json.Marshal(got[4])
	stand = fmt.Sprintf(`({getBoundingClientRect: () => new DOMRect(%f, %f, %f, %f), scrollIntoView() {}, getAttribute: () => null, innerText: %s})`,
		box[0], box[1], box[2], box[3], name)
	return box, stand, nil
}

// clipOf is a screenshot of the tab clipped to an element in a frame: a
// frame's own target can't take one.
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
