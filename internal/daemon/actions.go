package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/Facets-cloud/bx/internal/browser"
	"github.com/Facets-cloud/bx/internal/exit"
)

// actOn locks tab id, runs fn as an action, and reports the result.
func (s *server) actOn(id string, timeout time.Duration, fn func(ctx context.Context, sess *tabSession) error) (ActResult, error) {
	sess, err := s.lockSession(id)
	if err != nil {
		return ActResult{}, err
	}
	defer sess.mu.Unlock()
	return s.act(sess, timeout, func(ctx context.Context) error { return fn(ctx, sess) })
}

func (s *server) click(id, el string, double bool, timeout time.Duration) (ActResult, error) {
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		sel, err := sess.selector(ctx, el)
		if err != nil {
			return err
		}
		a := chromedp.Click(chromedp.CSS(sel))
		if double {
			a = chromedp.DoubleClick(chromedp.CSS(sel))
		}
		_, err = chromedp.Run(ctx, a)
		return tabErr(err, "click", el, timeout)
	})
}

// center scrolls the element into view and returns its middle point.
func center(ctx context.Context, sess *tabSession, el string, timeout time.Duration) (float64, float64, error) {
	sel, err := sess.selector(ctx, el)
	if err != nil {
		return 0, 0, err
	}
	if _, err := chromedp.Run(ctx, chromedp.ScrollIntoView(chromedp.CSS(sel))); err != nil {
		return 0, 0, tabErr(err, "scroll", el, timeout)
	}
	q, _ := json.Marshal(sel)
	xy, err := chromedp.Run(ctx, chromedp.Evaluate[[]float64](fmt.Sprintf(
		`(() => { const r = document.querySelector(%s).getBoundingClientRect(); return [r.x + r.width / 2, r.y + r.height / 2]; })()`, q)))
	if err != nil || len(xy) != 2 {
		return 0, 0, tabErr(err, "locate", el, timeout)
	}
	return xy[0], xy[1], nil
}

func (s *server) hover(id, el string, timeout time.Duration) (ActResult, error) {
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		x, y, err := center(ctx, sess, el, timeout)
		if err != nil {
			return err
		}
		_, err = chromedp.Run(ctx, chromedp.MouseEvent(input.DispatchMouseEventTypeMouseMoved, x, y))
		return err
	})
}

// drag presses the mouse on from, moves in steps to to, and releases.
func (s *server) drag(id, from, to string, timeout time.Duration) (ActResult, error) {
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		x1, y1, err := center(ctx, sess, from, timeout)
		if err != nil {
			return err
		}
		x2, y2, err := center(ctx, sess, to, timeout)
		if err != nil {
			return err
		}
		steps := []chromedp.Action[chromedp.Void]{
			chromedp.MouseEvent(input.DispatchMouseEventTypeMouseMoved, x1, y1),
			chromedp.MouseEvent(input.DispatchMouseEventTypeMousePressed, x1, y1, chromedp.ButtonLeft, chromedp.ClickCount(1)),
		}
		for i := 1; i <= 10; i++ {
			f := float64(i) / 10
			steps = append(steps, chromedp.MouseEvent(input.DispatchMouseEventTypeMouseMoved,
				x1+(x2-x1)*f, y1+(y2-y1)*f, chromedp.ButtonLeft))
		}
		steps = append(steps, chromedp.MouseEvent(input.DispatchMouseEventTypeMouseReleased, x2, y2, chromedp.ButtonLeft, chromedp.ClickCount(1)))
		_, err = chromedp.Run(ctx, chromedp.Steps(steps...))
		return err
	})
}

// fieldKindJS classifies the element so fill knows how to set it.
const fieldKindJS = `(el => {
	if (el.tagName === "SELECT") return "select";
	if (el.tagName === "INPUT" && (el.type === "checkbox" || el.type === "radio")) return el.type;
	const role = el.getAttribute("role");
	if (role === "checkbox" || role === "switch" || role === "radio") return "checkbox";
	return "text";
})(document.querySelector(%s))`

// selectOptionJS picks the option whose value or visible text is v.
const selectOptionJS = `((el, v) => {
	const o = [...el.options].find(o => o.value === v || o.text.trim() === v);
	if (!o) return false;
	el.value = o.value;
	el.dispatchEvent(new Event("input", {bubbles: true}));
	el.dispatchEvent(new Event("change", {bubbles: true}));
	return true;
})(document.querySelector(%s), %s)`

// checkedJS reports whether a checkbox, radio or ARIA switch is on.
const checkedJS = `(el => el.checked === true || el.getAttribute("aria-checked") === "true")(document.querySelector(%s))`

// fill sets each element to its value: text fields are typed into,
// selects pick an option by value or text, and checkboxes and radios are
// clicked when their state differs from "true"/"false".
func (s *server) fill(id string, pairs [][2]string, timeout time.Duration) (ActResult, error) {
	if len(pairs) == 0 {
		return ActResult{}, exit.New(exit.Validation, "fill needs an element and a value")
	}
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		for _, p := range pairs {
			if err := fillOne(ctx, sess, p[0], p[1], timeout); err != nil {
				return err
			}
		}
		return nil
	})
}

func fillOne(ctx context.Context, sess *tabSession, el, value string, timeout time.Duration) error {
	sel, err := sess.selector(ctx, el)
	if err != nil {
		return err
	}
	if _, err := chromedp.Run(ctx, chromedp.WaitVisible(chromedp.CSS(sel))); err != nil {
		return tabErr(err, "fill", el, timeout)
	}
	q, _ := json.Marshal(sel)
	kind, err := chromedp.Run(ctx, chromedp.Evaluate[string](fmt.Sprintf(fieldKindJS, q)))
	if err != nil {
		return upstream("fill "+el, err)
	}
	switch kind {
	case "select":
		v, _ := json.Marshal(value)
		ok, err := chromedp.Run(ctx, chromedp.Evaluate[bool](fmt.Sprintf(selectOptionJS, q, v)))
		if err != nil {
			return upstream("fill "+el, err)
		}
		if !ok {
			return exit.New(exit.User, "%s has no option with value or text %q", el, value)
		}
		return nil
	case "checkbox", "radio":
		return setChecked(ctx, sel, el, value, timeout)
	}
	return typeInto(ctx, sel, el, value, timeout)
}

func setChecked(ctx context.Context, sel, el, value string, timeout time.Duration) error {
	want := value == "true"
	if !want && value != "false" {
		return exit.New(exit.Validation, "%s is a checkbox or radio: the value must be true or false", el)
	}
	q, _ := json.Marshal(sel)
	on, err := chromedp.Run(ctx, chromedp.Evaluate[bool](fmt.Sprintf(checkedJS, q)))
	if err != nil {
		return upstream("fill "+el, err)
	}
	if on == want {
		return nil
	}
	_, err = chromedp.Run(ctx, chromedp.Click(chromedp.CSS(sel)))
	return tabErr(err, "fill", el, timeout)
}

// typeInto focuses a text field (by a real click when it refuses
// programmatic focus), selects what it holds and inserts value, which
// replaces the selection and fires input events as typing does.
func typeInto(ctx context.Context, sel, el, value string, timeout time.Duration) error {
	if _, err := chromedp.Run(ctx, chromedp.Focus(chromedp.CSS(sel))); err != nil {
		if _, err := chromedp.Run(ctx, chromedp.Click(chromedp.CSS(sel))); err != nil {
			return tabErr(err, "fill", el, timeout)
		}
	}
	steps := chromedp.Steps(
		chromedp.Evaluate[chromedp.Void](selectFocusedJS),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			if value == "" {
				_, err := chromedp.Evaluate[chromedp.Void](`document.execCommand("delete")`)(ctx, t)
				return err
			}
			_, err := cdp.Call(ctx, t, input.InsertText, input.InsertTextParams{Text: value})
			return err
		}),
	)
	_, err := chromedp.Run(ctx, steps)
	return tabErr(err, "fill", el, timeout)
}

// selectFocusedJS selects all content of the focused field or editable.
const selectFocusedJS = `(() => {
	const e = document.activeElement;
	if (e && typeof e.select === "function") { e.select(); } else { document.execCommand("selectAll"); }
})()`

// typeText types text into whatever has focus, key by key, then presses
// submit (a key such as Enter) if given.
func (s *server) typeText(id, text, submit string, timeout time.Duration) (ActResult, error) {
	var combo keyCombo
	if submit != "" {
		c, err := parseCombo(submit)
		if err != nil {
			return ActResult{}, err
		}
		combo = c
	}
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		if _, err := chromedp.Run(ctx, chromedp.KeyEvent(text)); err != nil {
			return upstream("type", err)
		}
		if submit == "" {
			return nil
		}
		if _, err := chromedp.Run(ctx, combo.action()); err != nil {
			return upstream("press "+submit, err)
		}
		return nil
	})
}

func (s *server) key(id, keys string, timeout time.Duration) (ActResult, error) {
	combo, err := parseCombo(keys)
	if err != nil {
		return ActResult{}, err
	}
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		_, err := chromedp.Run(ctx, combo.action())
		if err != nil {
			return upstream("press "+keys, err)
		}
		return nil
	})
}

func (s *server) upload(id, el string, files []string, timeout time.Duration) (ActResult, error) {
	if len(files) == 0 {
		return ActResult{}, exit.New(exit.Validation, "upload needs at least one file")
	}
	abs := make([]string, len(files))
	for i, f := range files {
		p, err := filepath.Abs(f)
		if err == nil {
			_, err = os.Stat(p)
		}
		if err != nil {
			return ActResult{}, exit.New(exit.User, "cannot read %s: %v", f, err)
		}
		abs[i] = p
	}
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		sel, err := sess.selector(ctx, el)
		if err != nil {
			return err
		}
		_, err = chromedp.Run(ctx, chromedp.SetUploadFiles(chromedp.CSS(sel), abs))
		return tabErr(err, "upload", el, timeout)
	})
}

// dialog accepts or dismisses the open dialog. It takes no tab lock: the
// action that opened the dialog has already returned.
func (s *server) dialog(id string, accept bool, text string) (Dialog, error) {
	sess, err := s.session(id)
	if err != nil {
		return Dialog{}, err
	}
	d := sess.pendingDialog()
	if d == nil {
		return Dialog{}, exit.New(exit.User, "no dialog is open on tab %s", id)
	}
	ctx, cancel := context.WithTimeout(sess.ctx, 10*time.Second)
	defer cancel()
	_, err = cdp.Call(ctx, sess.t, page.HandleJavaScriptDialog, page.HandleJavaScriptDialogParams{Accept: accept, PromptText: text})
	if err != nil {
		return Dialog{}, upstream("handle dialog", err)
	}
	sess.dmu.Lock()
	sess.dialog = nil
	sess.dmu.Unlock()
	return *d, nil
}

func (s *server) front(id string) (any, error) {
	sess, err := s.session(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(sess.ctx, 10*time.Second)
	defer cancel()
	if _, err := cdp.Call(ctx, sess.t, page.BringToFront, cdp.Empty{}); err != nil {
		return nil, upstream("bring to front", err)
	}
	return nil, nil
}

// evalSource wraps a function such as "() => document.title" in a call,
// and leaves a plain expression as it is.
func evalSource(src string) string {
	t := strings.TrimSpace(src)
	if strings.HasPrefix(t, "function") || strings.HasPrefix(t, "async ") ||
		(strings.HasPrefix(t, "(") && strings.Contains(t, "=>")) || isArrowParam(t) {
		return "(" + t + ")()"
	}
	return t
}

// isArrowParam reports a single-parameter arrow such as "x => x".
func isArrowParam(t string) bool {
	head, _, ok := strings.Cut(t, "=>")
	head = strings.TrimSpace(head)
	return ok && head != "" && !strings.ContainsAny(head, " ().;")
}

func (s *server) eval(id, src string, timeout time.Duration) (json.RawMessage, error) {
	if strings.TrimSpace(src) == "" {
		return nil, exit.New(exit.Validation, "eval needs a script")
	}
	sess, err := s.lockSession(id)
	if err != nil {
		return nil, err
	}
	defer sess.mu.Unlock()
	if err := sess.errIfDialog(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	yes := true
	res, err := cdp.Call(ctx, sess.t, runtime.Evaluate, runtime.EvaluateParams{
		Expression: evalSource(src), ReturnByValue: &yes, AwaitPromise: &yes, UserGesture: &yes,
	})
	if err != nil {
		return nil, upstream("eval", err)
	}
	if ex := res.ExceptionDetails; ex != nil {
		msg := ex.Text
		if ex.Exception != nil && ex.Exception.Description != "" {
			msg = ex.Exception.Description
		}
		return nil, exit.New(exit.User, "script threw: %s", msg)
	}
	if res.Result == nil || len(res.Result.Value) == 0 {
		return json.RawMessage("null"), nil
	}
	return json.RawMessage(res.Result.Value), nil
}

// wait polls until text appears, or an element matches (or, with gone,
// until it no longer does).
func (s *server) wait(id, text, el string, gone bool, timeout time.Duration) (any, error) {
	if (text == "") == (el == "") {
		return nil, exit.New(exit.Validation, "wait needs exactly one of --text or --selector")
	}
	sess, err := s.lockSession(id)
	if err != nil {
		return nil, err
	}
	defer sess.mu.Unlock()
	cond := ""
	if text != "" {
		q, _ := json.Marshal(text)
		cond = fmt.Sprintf(`document.body && document.body.innerText.includes(%s)`, q)
	} else {
		sel, err := sess.selector(sess.ctx, el)
		if err != nil {
			return nil, err
		}
		q, _ := json.Marshal(sel)
		cond = fmt.Sprintf(`(e => !!e && e.getClientRects().length > 0)(document.querySelector(%s))`, q)
	}
	if gone {
		cond = "!(" + cond + ")"
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok, err := run(sess, 5*time.Second, chromedp.Evaluate[bool](cond))
		if err == nil && ok {
			return nil, nil
		}
		if exit.CodeOf(err) == exit.User { // a dialog opened
			return nil, err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return nil, exit.New(exit.User, "condition not met within %s", timeout)
}

// goTo navigates the tab to a URL, or back, forward or reload.
func (s *server) goTo(id, where string, timeout time.Duration) (ActResult, error) {
	var a chromedp.Action[chromedp.Void]
	switch where {
	case "back":
		a = chromedp.NavigateBack()
	case "forward":
		a = chromedp.NavigateForward()
	case "reload":
		a = chromedp.Reload()
	default:
		if err := browser.CheckURL(where); err != nil {
			return ActResult{}, err
		}
		a = chromedp.Navigate(where)
	}
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		_, err := chromedp.Run(ctx, a)
		return tabErr(err, "go to "+where, "", timeout)
	})
}

// snapshot reads the tab's accessibility tree and numbers its elements.
// The refs stay valid until the tab navigates.
func (s *server) snapshot(id string, timeout time.Duration) (Snapshot, error) {
	sess, err := s.lockSession(id)
	if err != nil {
		return Snapshot{}, err
	}
	defer sess.mu.Unlock()
	if err := sess.errIfDialog(); err != nil {
		return Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	tree, err := cdp.Call(ctx, sess.t, accessibility.GetFullAXTree, accessibility.GetFullAXTreeParams{})
	if err != nil {
		return Snapshot{}, upstream("snapshot", err)
	}
	nodes, refs := buildSnapshot(tree.Nodes)
	sess.refs, sess.refLoader = refs, sess.loaderID(ctx)
	url, title := sess.location(ctx)
	return Snapshot{URL: url, Title: title, Nodes: nodes}, nil
}
