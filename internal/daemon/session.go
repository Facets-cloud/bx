package daemon

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
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

	refs      map[string]cdp.BackendNodeID
	refLoader cdp.LoaderID
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
	tctx, cancel := chromedp.NewContext(s.ctx, chromedp.WithTargetID(id), chromedp.WithDetachOnCancel())
	sess := &tabSession{id: id, ctx: tctx, cancel: cancel}
	done := make(chan error, 1)
	go func() {
		_, err := chromedp.Run(tctx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			sess.t = t
			if _, err := cdp.Call(ctx, t, emulation.SetFocusEmulationEnabled, emulation.SetFocusEmulationEnabledParams{Enabled: true}); err != nil {
				return err
			}
			_, err := cdp.Call(ctx, t, page.Enable, page.EnableParams{})
			return err
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
	return v[0], v[1]
}

var refPattern = regexp.MustCompile(`^e[0-9]+$`)

// selector turns an element argument into a CSS selector. A snapshot ref
// such as e12 is resolved to its DOM node, which gets a data-bx-ref
// attribute so every action can find it with plain CSS.
func (sess *tabSession) selector(ctx context.Context, el string) (string, error) {
	if el == "" {
		return "", exit.New(exit.Validation, "an element (CSS selector or snapshot ref such as e12) is required")
	}
	if !refPattern.MatchString(el) {
		return el, nil
	}
	backend, ok := sess.refs[el]
	if !ok || sess.loaderID(ctx) != sess.refLoader {
		return "", exit.New(exit.User, "%s is not in this tab's latest snapshot: run bx snapshot %s again", el, sess.id)
	}
	node, err := cdp.Call(ctx, sess.t, dom.ResolveNode, dom.ResolveNodeParams{BackendNodeID: backend})
	if err != nil || node.Object == nil {
		return "", exit.New(exit.User, "%s is no longer on the page: run bx snapshot %s again", el, sess.id)
	}
	// el matched ^e[0-9]+$, so it is safe inside the script.
	_, err = cdp.Call(ctx, sess.t, runtime.CallFunctionOn, runtime.CallFunctionOnParams{
		// A ref on text names its parent element.
		FunctionDeclaration: fmt.Sprintf(`function() { (this.nodeType === 3 ? this.parentElement : this).setAttribute("data-bx-ref", %q); }`, el),
		ObjectID:            node.Object.ObjectID,
	})
	if err != nil {
		return "", upstream("mark "+el, err)
	}
	return fmt.Sprintf(`[data-bx-ref=%q]`, el), nil
}

// dropSession forgets a closed tab.
func (s *server) dropSession(id target.ID) {
	s.mu.Lock()
	sess := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if sess != nil {
		sess.cancel()
	}
}
