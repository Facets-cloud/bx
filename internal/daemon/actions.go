package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		e, err := sess.element(ctx, el)
		if err != nil {
			return err
		}
		verb := "Click"
		if double {
			verb = "Double-click"
		}
		return tabErr(sess.press(ctx, e, verb, double), "click", el, timeout)
	})
}

// center scrolls the element into view and returns its middle point.
func center(ctx context.Context, sess *tabSession, el string, timeout time.Duration) (float64, float64, error) {
	e, err := sess.element(ctx, el)
	if err != nil {
		return 0, 0, err
	}
	if _, err := chromedp.Run(ctx, chromedp.ScrollIntoView(e.q())); err != nil {
		return 0, 0, tabErr(err, "scroll", el, timeout)
	}
	xy, err := chromedp.Run(ctx, chromedp.Evaluate[[]float64](fmt.Sprintf(
		`(() => { const r = (%s).getBoundingClientRect(); return [r.x + r.width / 2, r.y + r.height / 2]; })()`, e.js)))
	if err != nil || len(xy) != 2 {
		return 0, 0, tabErr(err, "locate", el, timeout)
	}
	return xy[0], xy[1], nil
}

func (s *server) hover(id, el string, timeout time.Duration) (ActResult, error) {
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		if e, err := sess.element(ctx, el); err == nil {
			sess.indicate(ctx, "Hover", e.js)
		}
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
		if e, err := sess.element(ctx, from); err == nil {
			sess.indicate(ctx, "Drag", e.js)
		}
		x1, y1, err := center(ctx, sess, from, timeout)
		if err != nil {
			return err
		}
		x2, y2, err := center(ctx, sess, to, timeout)
		if err != nil {
			return err
		}
		press := chromedp.Steps(
			chromedp.MouseEvent(input.DispatchMouseEventTypeMouseMoved, x1, y1),
			chromedp.MouseEvent(input.DispatchMouseEventTypeMousePressed, x1, y1, chromedp.ButtonLeft, chromedp.ClickCount(1)))
		if _, err := chromedp.Run(ctx, press); err != nil {
			return err
		}
		// Move in steps, with the pointer following the mouse, so the drag
		// is visible and the page sees intermediate moves.
		for i := 1; i <= 10; i++ {
			f := float64(i) / 10
			x, y := x1+(x2-x1)*f, y1+(y2-y1)*f
			if _, err := chromedp.Run(ctx, chromedp.MouseEvent(input.DispatchMouseEventTypeMouseMoved, x, y, chromedp.ButtonLeft)); err != nil {
				return err
			}
			sess.point(ctx, x, y)
			if !sess.fast.Load() {
				time.Sleep(between(12*time.Millisecond, 28*time.Millisecond))
			}
		}
		_, err = chromedp.Run(ctx, chromedp.MouseEvent(input.DispatchMouseEventTypeMouseReleased, x2, y2, chromedp.ButtonLeft, chromedp.ClickCount(1)))
		return err
	})
}

// fieldKindJS classifies the element so fill knows how to set it.
const fieldKindJS = `(el => {
	if (el.tagName === "SELECT") return "select";
	if (el.tagName === "INPUT" && (el.type === "checkbox" || el.type === "radio")) return el.type;
	if (el.tagName === "INPUT" && ["date", "time", "datetime-local", "month", "week", "range", "color"].includes(el.type)) return "value";
	const role = el.getAttribute("role");
	if (role === "checkbox" || role === "switch" || role === "radio") return "checkbox";
	return "text";
})(%s)`

// selectOptionJS picks the option whose value or visible text is v.
const selectOptionJS = `((el, v) => {
	const o = [...el.options].find(o => o.value === v || o.text.trim() === v);
	if (!o) return false;
	el.value = o.value;
	el.dispatchEvent(new Event("input", {bubbles: true}));
	el.dispatchEvent(new Event("change", {bubbles: true}));
	return true;
})(%s, %s)`

// setValueJS sets a date, time, range or color input, which ignore typed
// text, through the native setter (so framework-bound inputs see it) and
// reports whether the browser kept the value.
const setValueJS = `((el, v) => {
	Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set.call(el, v);
	el.dispatchEvent(new Event("input", {bubbles: true}));
	el.dispatchEvent(new Event("change", {bubbles: true}));
	return el.value === v;
})(%s, %s)`

// checkedJS reports whether a checkbox, radio or ARIA switch is on.
const checkedJS = `(el => el.checked === true || el.getAttribute("aria-checked") === "true")(%s)`

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
	e, err := sess.element(ctx, el)
	if err != nil {
		return err
	}
	if _, err := chromedp.Run(ctx, chromedp.WaitVisible(e.q())); err != nil {
		return tabErr(err, "fill", el, timeout)
	}
	sess.indicate(ctx, "Fill", e.js)
	q := e.js
	kind, err := chromedp.Run(ctx, chromedp.Evaluate[string](fmt.Sprintf(fieldKindJS, q)))
	if err != nil {
		return upstream("fill "+el, err)
	}
	switch kind {
	case "select":
		sess.showSelect(ctx, e, value)
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
		return setChecked(ctx, sess, e, value, timeout)
	case "value":
		v, _ := json.Marshal(value)
		ok, err := chromedp.Run(ctx, chromedp.Evaluate[bool](fmt.Sprintf(setValueJS, q, v)))
		if err != nil {
			return upstream("fill "+el, err)
		}
		if !ok {
			return exit.New(exit.User, "%s did not accept %q: dates are YYYY-MM-DD, times HH:MM, months YYYY-MM", el, value)
		}
		return nil
	}
	return typeInto(ctx, sess, e, value, timeout)
}

func setChecked(ctx context.Context, sess *tabSession, e elem, value string, timeout time.Duration) error {
	el := e.name
	want := value == "true"
	if !want && value != "false" {
		return exit.New(exit.Validation, "%s is a checkbox or radio: the value must be true or false", el)
	}
	on, err := chromedp.Run(ctx, chromedp.Evaluate[bool](fmt.Sprintf(checkedJS, e.js)))
	if err != nil {
		return upstream("fill "+el, err)
	}
	if on == want {
		return nil
	}
	return tabErr(sess.press(ctx, e, "Check", false), "fill", el, timeout)
}

// typeInto focuses a text field (by a real click when it refuses
// programmatic focus), selects what it holds and inserts value, which
// replaces the selection and fires input events as typing does.
func typeInto(ctx context.Context, sess *tabSession, e elem, value string, timeout time.Duration) error {
	el := e.name
	if sess.fast.Load() {
		if _, err := chromedp.Run(ctx, chromedp.Focus(e.q())); err != nil {
			if _, err := chromedp.Run(ctx, chromedp.Click(e.q())); err != nil {
				return tabErr(err, "fill", el, timeout)
			}
		}
	} else if err := sess.press(ctx, e, "Fill", false); err != nil { // click into the field, as a person would
		return tabErr(err, "fill", el, timeout)
	}
	if _, err := chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](fmt.Sprintf(selectAllJS, e.js))); err != nil {
		return upstream("fill "+el, err)
	}
	if value == "" {
		_, err := chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](`document.execCommand("delete")`))
		return tabErr(err, "fill", el, timeout)
	}
	sess.say(ctx, sess.typingVerb(value))
	return tabErr(sess.enter(ctx, value), "fill", el, timeout)
}

// selectAllJS selects everything in a field, or in an editable element
// such as a rich text editor, so the next insert replaces it.
const selectAllJS = `(e => {
	if (typeof e.select === "function") { e.select(); return; }
	const r = document.createRange(); r.selectNodeContents(e);
	const s = getSelection(); s.removeAllRanges(); s.addRange(r);
})(%s)`

// typeText types text into whatever has focus, key by key, then presses
// submit (a key such as Enter) if given.
// typeText types text key by key into el, focusing it first, or into
// whatever has focus when el is empty, then presses submit (a key such as
// Enter) if given. Key by key is what rich editors and autocompletes
// listen to, where fill's insert can go unnoticed.
func (s *server) typeText(id, el, text, submit string, timeout time.Duration) (ActResult, error) {
	var combo keyCombo
	if submit != "" {
		c, err := parseCombo(submit)
		if err != nil {
			return ActResult{}, err
		}
		combo = c
	}
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		target := focusedJS
		if el != "" {
			e, err := sess.element(ctx, el)
			if err != nil {
				return err
			}
			if _, err := chromedp.Run(ctx, chromedp.Focus(e.q())); err != nil {
				if _, err := chromedp.Run(ctx, chromedp.Click(e.q())); err != nil {
					return tabErr(err, "type", el, timeout)
				}
			}
			target = e.js
		}
		sess.indicate(ctx, sess.typingVerb(text), target)
		if err := sess.enter(ctx, text); err != nil {
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
		sess.indicate(ctx, "Press "+keys, focusedJS)
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
		e, err := sess.element(ctx, el)
		if err != nil {
			return err
		}
		sess.indicate(ctx, fmt.Sprintf("Attach %d file(s)", len(abs)), e.js)
		_, err = chromedp.Run(ctx, chromedp.SetUploadFiles(e.q(), abs))
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
		e, err := sess.element(sess.ctx, el)
		if err != nil {
			return nil, err
		}
		cond = fmt.Sprintf(`(e => !!e && e.isConnected && e.getClientRects().length > 0)(%s)`, e.js)
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
		a = historyStep(-1)
	case "forward":
		a = historyStep(1)
	case "reload":
		a = chromedp.Reload()
	default:
		if err := browser.CheckURL(where); err != nil {
			return ActResult{}, err
		}
		a = chromedp.Navigate(where)
	}
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		sess.indicate(ctx, "Go "+where, "")
		_, err := chromedp.Run(ctx, a)
		return tabErr(err, "go to "+where, "", timeout)
	})
}

// historyStep goes back (-1) or forward (1) in the tab's history and waits
// until the page shows that entry. chromedp's own NavigateBack waits for a
// load event, which a page restored from the back-forward cache never
// fires, so it hung until the timeout.
func historyStep(delta int64) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		res, err := cdp.Call(ctx, t, page.GetNavigationHistory, cdp.Empty{})
		if err != nil {
			return err
		}
		i := res.CurrentIndex + delta
		if i < 0 || i >= int64(len(res.Entries)) {
			return exit.New(exit.User, "no page to go %s to in this tab's history", map[int64]string{-1: "back", 1: "forward"}[delta])
		}
		if _, err := cdp.Call(ctx, t, page.NavigateToHistoryEntry, page.NavigateToHistoryEntryParams{EntryID: res.Entries[i].ID}); err != nil {
			return err
		}
		// Done when the tab's current entry is the one asked for and the
		// document has finished loading.
		for {
			h, err := cdp.Call(ctx, t, page.GetNavigationHistory, cdp.Empty{})
			if err == nil && h.CurrentIndex == i {
				if state, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.readyState`)); err == nil && state == "complete" {
					return nil
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
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
	sess.indicate(ctx, "Read the page", "")
	return sess.takeSnapshot(ctx)
}

// scrollEndJS scrolls an element's content to the end, as a person
// dragging its scrollbar would, which fires its scroll events: terms
// boxes that unlock "I accept" at the bottom, lists that load more.
const scrollEndJS = `(e => { e.scrollTo({top: e.scrollHeight, left: 0}); return e.scrollTop; })(%s)`

// scroll scrolls el's own content to the end, or the page when el is
// empty.
func (s *server) scroll(id, el string, timeout time.Duration) (ActResult, error) {
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		target := "document.scrollingElement"
		if el != "" {
			e, err := sess.element(ctx, el)
			if err != nil {
				return err
			}
			sess.indicate(ctx, "Scroll", e.js)
			target = e.js
		}
		_, err := chromedp.Run(ctx, chromedp.Evaluate[float64](fmt.Sprintf(scrollEndJS, target)))
		return tabErr(err, "scroll", el, timeout)
	})
}
