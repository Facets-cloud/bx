package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chromedp/cdproto/accessibility"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/performance"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/Facets-cloud/bx/internal/exit"
)

// tabSession is one tab the daemon stays attached to. Staying attached
// lets bx keep focus emulation on (a background tab then accepts typing),
// watch for dialogs, and keep snapshot refs between commands.
type tabSession struct {
	id     target.ID
	ctx    context.Context // chromedp context attached to the tab
	cancel context.CancelFunc
	t      *chromedp.Target

	mu sync.Mutex // one command at a time on this tab

	dmu       sync.Mutex
	dialog    *Dialog
	interrupt context.CancelCauseFunc // ends the running action when a dialog opens

	refs      refState     // refs of the latest snapshot
	refLoader cdp.LoaderID // the document those refs belong to
	snap      []SnapNode   // the latest snapshot, for the changes an action reports
	pointer   *[2]float64  // where bx's on-page pointer last stood, to carry it across pages
	fast      atomic.Bool  // full pace, the default; false when the request asked for human pace (pace human)
	keyTyping atomic.Bool  // the current request asked to type key by key (BX_TYPING=keys)
	title     atomic.Value // string: the title of the agent session acting on the tab, if it gave one
	step      atomic.Value // string: "3/32" while a batch runs, shown in the pointer's tag
	named     atomic.Value // map[string]string: each ref of the latest snapshot as role and name, for the log
	clicked   atomic.Int64 // unix nanoseconds of the last click on the tab, see paste

	tmu      sync.Mutex
	takeover page.ScriptIdentifier // the takeover marker's script, while an agent session marks the tab

	log *devLog // console and network activity, from the moment bx attached

	mocks tabMocks // made-up responses for matching requests, see mock

	bctx   context.Context                       // the browser connection, for attaching to frames
	frames map[target.ID]*frame                  // the tab's frames that run in their own process, by target
	worlds map[docKey]runtime.ExecutionContextID // bx's own JavaScript world in each document, see world
}

// Dialog is a JavaScript alert, confirm, prompt or beforeunload dialog.
type Dialog struct {
	Type          string `json:"type"`
	Message       string `json:"message"`
	DefaultPrompt string `json:"default_prompt,omitempty"`
}

var errDialog = errors.New("a dialog opened")

// session returns the attached session for tab id, attaching on first use.
func (s *server) session(id string) (*tabSession, error) {
	info, err := s.findTab(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	sess := s.sessions[info.TargetID]
	s.mu.Unlock()
	if sess != nil && sess.ctx.Err() == nil {
		return sess, nil
	}
	sess, err = s.attach(info.TargetID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.sessions[info.TargetID] = sess
	s.mu.Unlock()
	return sess, nil
}

func (s *server) attach(id target.ID) (*tabSession, error) {
	// WithDetachOnCancel: ending the context detaches, it never closes the tab.
	l := newDevLog()
	tctx, cancel := chromedp.NewContext(s.ctx, chromedp.WithTargetID(id), chromedp.WithDetachOnCancel(),
		chromedp.WithListener(l.listen))
	sess := &tabSession{id: id, ctx: tctx, cancel: cancel, log: l, bctx: s.ctx}
	done := make(chan error, 1)
	go func() {
		_, err := chromedp.Run(tctx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			sess.t = t
			if _, err := cdp.Call(ctx, t, emulation.SetFocusEmulationEnabled, emulation.SetFocusEmulationEnabledParams{Enabled: true}); err != nil {
				return err
			}
			if _, err := cdp.Call(ctx, t, page.Enable, page.EnableParams{}); err != nil {
				return err
			}
			// Runtime counters (script, layout, style time) count only
			// while the Performance domain is on; best effort.
			_, _ = cdp.Call(ctx, t, performance.Enable, performance.EnableParams{})
			return nil
		}))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			cancel()
			return nil, upstream("attach to tab", err)
		}
	case <-time.After(15 * time.Second):
		cancel()
		return nil, exit.New(exit.Upstream, "attach to tab: timed out")
	}
	sess.watchDialogs()
	return sess, nil
}

// watchDialogs records dialogs as they open and close. A dialog that opens
// during an action ends that action, which then reports the dialog.
func (sess *tabSession) watchDialogs() {
	opened := cdp.Events(sess.ctx, sess.t, page.JavascriptDialogOpening)
	closed := cdp.Events(sess.ctx, sess.t, page.JavascriptDialogClosed)
	go func() {
		for ev, err := range opened {
			if err != nil {
				return
			}
			sess.dmu.Lock()
			sess.dialog = &Dialog{Type: string(ev.Type), Message: ev.Message, DefaultPrompt: ev.DefaultPrompt}
			if sess.interrupt != nil {
				sess.interrupt(errDialog)
			}
			sess.dmu.Unlock()
		}
	}()
	go func() {
		for _, err := range closed {
			if err != nil {
				return
			}
			sess.dmu.Lock()
			sess.dialog = nil
			sess.dmu.Unlock()
		}
	}()
}

func (sess *tabSession) pendingDialog() *Dialog {
	sess.dmu.Lock()
	defer sess.dmu.Unlock()
	return sess.dialog
}

// errIfDialog refuses to act while a dialog blocks the page.
func (sess *tabSession) errIfDialog() error {
	if d := sess.pendingDialog(); d != nil {
		return exit.New(exit.User, "the page is showing a dialog (%s: %q): run bx dialog %s accept|dismiss", d.Type, d.Message, sess.id)
	}
	return nil
}

// run runs a on the tab within timeout, for reads that change nothing.
func run[T any](sess *tabSession, timeout time.Duration, a chromedp.Action[T]) (T, error) {
	var zero T
	if err := sess.errIfDialog(); err != nil {
		return zero, err
	}
	ctx, cancel := context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	return chromedp.Run(ctx, a)
}

// ActResult is what every action reports: where the tab ended up, and
// whether the action navigated, opened tabs or opened a dialog.
type ActResult struct {
	URL       string   `json:"url"`
	Title     string   `json:"title"`
	Navigated bool     `json:"navigated"`
	NewTabs   []string `json:"new_tabs"`
	Dialog    *Dialog  `json:"dialog,omitempty"`
	// Changes lists what changed on the page since the last snapshot, by
	// ref: + added, - removed, ~ changed. Empty until the tab has a snapshot.
	Changes []string `json:"changes,omitempty"`
}

// act runs an action, then waits the way a person would before the next
// step: for a navigation the action started to load, and for the page to
// stop changing. A dialog that opens ends the wait and is reported.
func (s *server) act(sess *tabSession, timeout time.Duration, fn func(ctx context.Context) error) (ActResult, error) {
	if err := sess.errIfDialog(); err != nil {
		return ActResult{}, err
	}
	actx, interrupt := context.WithCancelCause(sess.ctx)
	defer interrupt(nil)
	sess.dmu.Lock()
	sess.interrupt = interrupt
	sess.dmu.Unlock()
	defer func() { sess.dmu.Lock(); sess.interrupt = nil; sess.dmu.Unlock() }()

	before := s.openedBy(sess.id, nil)
	loader := sess.loaderID(actx)
	var started atomic.Bool
	go func() {
		for _, err := range cdp.Events(actx, sess.t, page.FrameStartedLoading) {
			if err != nil {
				return
			}
			started.Store(true)
		}
	}()

	ctx, cancel := context.WithTimeout(actx, timeout)
	err := fn(ctx)
	cancel()
	if errors.Is(context.Cause(actx), errDialog) {
		return ActResult{NewTabs: []string{}, Dialog: sess.pendingDialog()}, nil
	}
	if err != nil {
		return ActResult{}, err
	}
	s.settle(actx, sess, &started)
	if errors.Is(context.Cause(actx), errDialog) {
		return ActResult{NewTabs: []string{}, Dialog: sess.pendingDialog()}, nil
	}
	r := ActResult{NewTabs: s.openedBy(sess.id, before)}
	r.Navigated = sess.loaderID(actx) != loader
	r.URL, r.Title = sess.location(actx)
	if r.Navigated {
		sess.say(actx, "Page loaded") // the new page has no pointer until bx draws it again
	}
	return r, nil
}

// settle waits up to 100ms for a navigation to start, up to 3s for one to
// finish loading, then for the DOM to go 100ms without a change (3s at most).
func (s *server) settle(ctx context.Context, sess *tabSession, started *atomic.Bool) {
	time.Sleep(100 * time.Millisecond)
	if started.Load() {
		lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		for lctx.Err() == nil {
			state, err := chromedp.Run(lctx, chromedp.Evaluate[string]("document.readyState"))
			if err == nil && state == "complete" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		cancel()
	}
	sctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	_, _ = chromedp.Run(sctx, chromedp.Evaluate[bool](stableDOMJS, chromedp.EvalAwaitPromise))
}

const stableDOMJS = `new Promise(resolve => {
	let quiet;
	const done = () => { observer.disconnect(); resolve(true); };
	const observer = new MutationObserver(() => { clearTimeout(quiet); quiet = setTimeout(done, 100); });
	observer.observe(document, {subtree: true, childList: true, attributes: true, characterData: true});
	quiet = setTimeout(done, 100);
	setTimeout(done, 3000);
})`

// openedBy lists page targets opened by tab id, leaving out those in skip.
func (s *server) openedBy(id target.ID, skip []string) []string {
	all, err := chromedp.Targets(s.ctx)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, k := range skip {
		seen[k] = true
	}
	out := []string{}
	for _, t := range all {
		if t.Type == "page" && t.OpenerID == id && !seen[string(t.TargetID)] {
			out = append(out, string(t.TargetID))
		}
	}
	return out
}

func (sess *tabSession) loaderID(ctx context.Context) cdp.LoaderID {
	tree, err := cdp.Call(ctx, sess.t, page.GetFrameTree, cdp.Empty{})
	if err != nil || tree.FrameTree == nil || tree.FrameTree.Frame == nil {
		return ""
	}
	return tree.FrameTree.Frame.LoaderID
}

func (sess *tabSession) location(ctx context.Context) (string, string) {
	v, err := chromedp.Run(ctx, chromedp.Evaluate[[]string]("[location.href, document.title]"))
	if err != nil || len(v) != 2 {
		return "", ""
	}
	return v[0], stripTakeover(v[1])
}

var refPattern = regexp.MustCompile(`^e[0-9]+$`)

// elem is an element an op works on, as a JavaScript expression that
// evaluates to it. Expressions reach into shadow roots, which CSS through
// DOM.querySelector cannot.
type elem struct {
	name string    // what the caller passed, for messages
	js   string    // in the element's own frame
	fr   *frameRef // the frame it is in, if it runs in its own process; nil in the tab itself
}

// q is the element as a chromedp selector.
func (e elem) q() chromedp.JSPath { return chromedp.JSPath(e.js) }

// deepQueryJS finds the first element matching a CSS selector in the page
// or, failing that, inside any open shadow root.
const deepQueryJS = `((s) => {
	const find = (root) => {
		const e = root.querySelector(s);
		if (e) return e;
		for (const n of root.querySelectorAll("*")) if (n.shadowRoot) { const f = find(n.shadowRoot); if (f) return f; }
		return null;
	};
	return find(document);
})(%s)`

// refsJS is where the page keeps the elements bx resolved refs to.
const refsJS = `window[Symbol.for("bx.refs")]`

// namePattern is an element named by role and accessible name, the way a
// snapshot line shows it: button "Save", or textbox "Name" #2 for the second.
var namePattern = regexp.MustCompile(`^([A-Za-z]+) "(.*)"(?: #([0-9]+))?$`)

// element turns an element argument into an elem. A snapshot ref such as
// e12, or a role and name such as button "Save", is resolved to its DOM
// node, which is then reachable from JavaScript wherever it is, in a shadow
// root or not. Anything else is a CSS selector.
func (sess *tabSession) element(ctx context.Context, el string) (elem, error) {
	if el == "" {
		return elem{}, exit.New(exit.Validation, "an element (snapshot ref such as e12, role and name such as button \"Save\", or CSS selector) is required")
	}
	var at nodeKey
	switch m := namePattern.FindStringSubmatch(el); {
	case refPattern.MatchString(el):
		id, ok := sess.refs.refs[el]
		if !ok || sess.loaderID(ctx) != sess.refLoader {
			return elem{}, exit.New(exit.User, "%s is not in this tab's latest snapshot: run bx snapshot %s again", el, sess.id)
		}
		at = nodeKey{sess.refs.in[el], id}
	case m != nil:
		nth, _ := strconv.Atoi(m[3])
		k, err := sess.findByName(ctx, m[1], m[2], max(nth, 1))
		if err != nil {
			return elem{}, err
		}
		at = k
	default:
		q, _ := json.Marshal(el)
		js := fmt.Sprintf(deepQueryJS, q)
		// The tab's own page first; the frames only when it has no match,
		// so a selector costs one extra check on a page that has it.
		if here, err := chromedp.Run(ctx, chromedp.Evaluate[bool]("!!"+js)); err != nil || here {
			return elem{name: el, js: js}, nil
		}
		k, ok := sess.findCSS(ctx, el)
		if !ok {
			return elem{name: el, js: js}, nil // not there yet: waits look in the tab
		}
		at = k
	}
	return sess.markAt(ctx, el, at)
}

// markAt marks the element at k (see mark), in the tab's own page or in
// one of its frames.
func (sess *tabSession) markAt(ctx context.Context, el string, k nodeKey) (elem, error) {
	if k.doc == (docKey{}) {
		return sess.mark(ctx, sess.t, el, k.backend, nil)
	}
	t := sess.targetOf(k.doc)
	if t == nil || (k.doc.target != "" && sess.frames[k.doc.target].ctx.Err() != nil) {
		return elem{}, exit.New(exit.User, "the frame holding %s has gone: run bx snapshot %s again", el, sess.id)
	}
	fr := &frameRef{f: sess.frames[k.doc.target], t: t}
	return sess.mark(ctx, t, el, k.backend, fr)
}

// findByName waits, up to nameWait or the context's deadline, for the nth
// element with this role and accessible name to be on the page. Waiting
// lets a batch name an element that an earlier step makes appear: the
// next page of a wizard, or a suggestion that loads late.
func (sess *tabSession) findByName(ctx context.Context, role, name string, nth int) (nodeKey, error) {
	wctx, cancel := context.WithTimeout(ctx, nameWait)
	defer cancel()
	found := 0
	for {
		// The tab's own page first; its frames, in the order they nest,
		// only when it has too few.
		var keys []nodeKey
		for _, id := range queryByName(wctx, sess.t, "", role, name) {
			keys = append(keys, nodeKey{docKey{}, id})
		}
		if len(keys) < nth {
			for _, d := range sess.docs(wctx)[1:] {
				obj, err := sess.docObject(wctx, d)
				if err != nil {
					continue
				}
				for _, id := range queryByName(wctx, d.t, obj, role, name) {
					keys = append(keys, nodeKey{d.key, id})
				}
			}
		}
		if found = len(keys); found >= nth {
			return keys[nth-1], nil
		}
		select {
		case <-wctx.Done():
			if found == 0 {
				return nodeKey{}, exit.New(exit.User, "no %s named %q on the page (waited %s): check the snapshot for the exact role and name", role, name, nameWait)
			}
			return nodeKey{}, exit.New(exit.User, "only %d %s named %q on the page, not #%d", found, role, name, nth)
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// nameWait bounds how long an element named by role and name may take to
// appear.
const nameWait = 10 * time.Second

// queryByName lists the DOM nodes, in page order, whose accessibility role
// and name match, in the document doc of target t (the target's own when
// doc is ""). Ignored nodes (hidden ones) are left out.
func queryByName(ctx context.Context, t *chromedp.Target, doc runtime.RemoteObjectID, role, name string) []cdp.BackendNodeID {
	if doc == "" {
		// The document as a JavaScript object: DOM.getDocument would reset
		// the node IDs chromedp keeps for this tab.
		res, err := cdp.Call(ctx, t, runtime.Evaluate, runtime.EvaluateParams{Expression: "document"})
		if err != nil || res.Result == nil || res.Result.ObjectID == "" {
			return nil
		}
		doc = res.Result.ObjectID
		defer func() {
			_, _ = cdp.Call(ctx, t, runtime.ReleaseObject, runtime.ReleaseObjectParams{ObjectID: doc})
		}()
	}
	res, err := cdp.Call(ctx, t, accessibility.QueryAXTree, accessibility.QueryAXTreeParams{
		ObjectID: doc, AccessibleName: name, Role: role})
	if err != nil {
		return nil
	}
	var ids []cdp.BackendNodeID
	for _, n := range res.Nodes {
		if !n.Ignored && n.BackendDOMNodeID != 0 {
			ids = append(ids, n.BackendDOMNodeID)
		}
	}
	return ids
}

// mark makes the node reachable from JavaScript as el, in its own window,
// and checks that it is still in the page: apps that redraw a region
// replace its elements, and a removed one would never become clickable.
// For an element in a frame (fr set), it keeps the element as an object
// there, so scripts on it run in the frame's window (see evalEl).
func (sess *tabSession) mark(ctx context.Context, t *chromedp.Target, el string, backend cdp.BackendNodeID, fr *frameRef) (elem, error) {
	node, err := cdp.Call(ctx, t, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: backend})
	if err != nil || node.Object == nil {
		return elem{}, exit.New(exit.User, "%s is no longer on the page: run bx snapshot %s again", el, sess.id)
	}
	key, _ := json.Marshal(el)
	byValue := fr == nil
	res, err := cdp.Call(ctx, t, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		// A ref on text names its parent element.
		FunctionDeclaration: fmt.Sprintf(`function() { const e = this.nodeType === 3 ? this.parentElement : this; const m = %s ||= {}; m[%s] = e; return !!e && e.isConnected ? (%t || e) : null; }`, refsJS, key, byValue),
		ObjectID:            node.Object.ObjectID,
		ReturnByValue:       &byValue,
	})
	if err != nil {
		return elem{}, upstream("mark "+el, err)
	}
	gone := res.Result == nil || (byValue && string(res.Result.Value) != "true") || (!byValue && res.Result.ObjectID == "")
	if gone {
		return elem{}, exit.New(exit.User, "%s was removed from the page (the page redrew it): use its new ref from the last action's changes, or take a snapshot", el)
	}
	if fr != nil {
		fr.obj = res.Result.ObjectID
	}
	return elem{name: el, js: fmt.Sprintf("%s[%s]", refsJS, key), fr: fr}, nil
}

// dropSession forgets a closed tab.
func (s *server) dropSession(id target.ID) {
	s.mu.Lock()
	sess := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if sess != nil {
		sess.dropFrames()
		sess.cancel()
	}
}
