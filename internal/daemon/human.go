package daemon

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"
	"unicode"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// In watch mode (BX_MODE=watch on the client, sent with each request) bx
// acts at human pace: the mouse travels to what it clicks, clicks land a
// little off-centre and are held briefly, and batch steps pause between
// them, the way a person uses a page, which some sites expect. In fast
// mode, the default, it does none of that.

// between returns a random duration in [lo, hi). Timing jitter, not a secret.
func between(lo, hi time.Duration) time.Duration {
	return lo + time.Duration(rand.Int64N(int64(hi-lo)))
}

// moveMouse moves the real mouse from where it last was to x, y over d,
// along the same eased arc the on-page pointer draws.
func (sess *tabSession) moveMouse(ctx context.Context, x, y float64, d time.Duration) {
	x0, y0 := x-160, y+90 // first move of the session: come in from below left
	if sess.pointer != nil {
		x0, y0 = sess.pointer[0], sess.pointer[1]
	}
	dx, dy := x-x0, y-y0
	dist := math.Hypot(dx, dy)
	if dist < 1 {
		return
	}
	bend := math.Min(80, dist*.18)
	mx, my := x0+dx/2-dy/dist*bend, y0+dy/2+dx/dist*bend
	n := max(8, int(d/(16*time.Millisecond)))
	for i := 1; i <= n; i++ {
		e := ease(float64(i) / float64(n))
		u := 1 - e
		px, py := u*u*x0+2*u*e*mx+e*e*x, u*u*y0+2*u*e*my+e*e*y
		if _, err := chromedp.Run(ctx, chromedp.MouseEvent(input.DispatchMouseEventTypeMouseMoved, px, py)); err != nil {
			return
		}
		time.Sleep(d / time.Duration(n))
	}
}

// ease is ease-in-out cubic, as the pointer's glide uses.
func ease(t float64) float64 {
	if t < .5 {
		return 4 * t * t * t
	}
	return 1 - math.Pow(-2*t+2, 3)/2
}

// press clicks e: at human pace by moving the mouse there and pressing and
// releasing with a short hold, otherwise with chromedp's instant click.
func (sess *tabSession) press(ctx context.Context, e elem, verb string, double bool) error {
	sess.clicked.Store(time.Now().UnixNano())
	x, y, ok := sess.indicate(ctx, verb, e.js)
	if !ok {
		a := chromedp.Click(e.q())
		if double {
			a = chromedp.DoubleClick(e.q())
		}
		_, err := chromedp.Run(ctx, a)
		return err
	}
	clicks := 1
	if double {
		clicks = 2
	}
	for c := 1; c <= clicks; c++ {
		if _, err := chromedp.Run(ctx, chromedp.MouseEvent(input.DispatchMouseEventTypeMousePressed, x, y, chromedp.ButtonLeft, chromedp.ClickCount(c))); err != nil {
			return err
		}
		time.Sleep(between(50*time.Millisecond, 110*time.Millisecond))
		if _, err := chromedp.Run(ctx, chromedp.MouseEvent(input.DispatchMouseEventTypeMouseReleased, x, y, chromedp.ButtonLeft, chromedp.ClickCount(c))); err != nil {
			return err
		}
		if c < clicks {
			time.Sleep(between(60*time.Millisecond, 120*time.Millisecond))
		}
	}
	return nil
}

// enter puts text where the focus is. By default it pastes it in one go
// (Input.insertText, which fires the same beforeinput and input events as
// a paste or an IME). With BX_TYPING=keys it types key by key instead: at
// once in fast mode, and in watch mode with a 35-110ms gap between keys
// and a little longer after spaces and punctuation.
func (sess *tabSession) enter(ctx context.Context, text string) error {
	if !sess.keyTyping.Load() && sess.paste(ctx, text) {
		return nil
	}
	if sess.fast.Load() {
		_, err := chromedp.Run(ctx, chromedp.KeyEvent(text))
		return err
	}
	for _, r := range text {
		if _, err := chromedp.Run(ctx, chromedp.KeyEvent(string(r))); err != nil {
			return err
		}
		gap := between(35*time.Millisecond, 110*time.Millisecond)
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			gap += between(0, 120*time.Millisecond)
		}
		time.Sleep(gap)
	}
	return nil
}

// focusOrClick gets an element ready to type into, quickly: focus it, and
// if focus didn't land on it (a spreadsheet cell or other element that
// can't take focus, whose page selects it on a click), click it. Either
// counts as the agent pointing at it, for paste's fallback.
func (sess *tabSession) focusOrClick(ctx context.Context, e elem) error {
	sess.clicked.Store(time.Now().UnixNano())
	if _, err := chromedp.Run(ctx, chromedp.Focus(e.q())); err == nil {
		in, err := chromedp.Run(ctx, chromedp.Evaluate[bool](fmt.Sprintf(holdsFocusJS, e.js)))
		if err == nil && in {
			return nil
		}
	}
	_, err := chromedp.Run(ctx, chromedp.Click(e.q()))
	return err
}

// holdsFocusJS reports whether the element, or something inside it, has focus.
const holdsFocusJS = `(e => {
	let a = document.activeElement;
	while (a && a.shadowRoot && a.shadowRoot.activeElement) a = a.shadowRoot.activeElement;
	return !!a && a !== document.body && (a === e || e.contains(a) || (e.shadowRoot && e.shadowRoot.contains(a)));
})(%s)`

// paste puts text into the focused element in one go and reports whether
// it took. Some pages only react to key presses: a grid cell that is not a
// text field, or an editor that ignores input it didn't see typed. There
// a paste does nothing, so it reads the focused element before and after,
// and on false enter types the text key by key instead, without the agent
// having to notice and ask for keys typing.
func (sess *tabSession) paste(ctx context.Context, text string) bool {
	before, ok := focusedText(ctx)
	if ok && before.Element && !before.Editable {
		return false // the focused element doesn't take pasted text: type it
	}
	watch := ok && !before.Element && time.Since(time.Unix(0, sess.clicked.Load())) < clickedRecently
	if watch {
		_, _ = chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](watchChangesJS))
	}
	if _, err := chromedp.Run(ctx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		_, err := cdp.Call(ctx, t, input.InsertText, input.InsertTextParams{Text: text})
		return err
	})); err != nil {
		return false
	}
	if watch {
		// Nothing has focus, but the agent just clicked something to type
		// into, such as a spreadsheet cell whose page reads key presses
		// on the whole document. If the paste changed nothing on the page,
		// type it. Without that recent click it stays a paste: keys sent
		// to a page with nothing focused could set off its shortcuts.
		n, err := chromedp.Run(ctx, chromedp.Evaluate[int](changesSinceJS, chromedp.EvalAwaitPromise))
		return err != nil || n > 0
	}
	after, ok := focusedText(ctx)
	// Can't tell, nothing has focus, or the text is there: leave it as pasted.
	return !ok || !after.Element || strings.Contains(after.Text, text) || after.Text != before.Text
}

// clickedRecently is how soon after a click, with nothing focused, a paste
// that changed nothing is retyped key by key.
const clickedRecently = 5 * time.Second

// watchChangesJS counts changes to the page from now on; changesSinceJS
// waits two frames (at most 300 ms, for a hidden tab) and returns the count.
const watchChangesJS = `(() => {
	const k = Symbol.for("bx.changes");
	if (window[k]) window[k].o.disconnect();
	const w = {n: 0};
	w.o = new MutationObserver(m => { w.n += m.length; });
	w.o.observe(document, {subtree: true, childList: true, characterData: true});
	window[k] = w;
})()`

const changesSinceJS = `new Promise(done => {
	const k = Symbol.for("bx.changes"), w = window[k];
	const end = () => { if (!w) return done(1); w.o.disconnect(); delete window[k]; done(w.n); };
	const t = setTimeout(end, 300);
	requestAnimationFrame(() => requestAnimationFrame(() => { clearTimeout(t); end(); }));
})`

type focused struct {
	Element  bool   `json:"element"` // something other than the page itself has focus
	Editable bool   `json:"editable"`
	Text     string `json:"text"`
}

// focusedText reads the element that has focus, through shadow roots and
// same-origin frames: whether it takes text, and the text it holds.
func focusedText(ctx context.Context) (focused, bool) {
	f, err := chromedp.Run(ctx, chromedp.Evaluate[focused](focusedTextJS))
	return f, err == nil
}

const focusedTextJS = `(() => {
	let a = document.activeElement;
	for (;;) {
		if (a && a.shadowRoot && a.shadowRoot.activeElement) { a = a.shadowRoot.activeElement; continue; }
		if (a && a.tagName === "IFRAME") {
			try { const d = a.contentDocument; if (d && d.activeElement) { a = d.activeElement; continue; } } catch (e) {}
		}
		break;
	}
	if (!a || a === document.body || a === document.documentElement) return {element: false, editable: false, text: ""};
	const field = a.tagName === "TEXTAREA" || (a.tagName === "INPUT" && !/^(checkbox|radio|button|submit|reset|file|image|range|color)$/i.test(a.type));
	return {element: true, editable: field || a.isContentEditable, text: field ? a.value : (a.textContent || "")};
})()`

// typingVerb says how enter will put text in, for the pointer's tag.
func (sess *tabSession) typingVerb(text string) string {
	n := len([]rune(text))
	if sess.keyTyping.Load() {
		return fmt.Sprintf("Type %d characters", n)
	}
	return fmt.Sprintf("Paste %d characters", n)
}

// stepPause is the pause between batch steps in watch mode.
func (sess *tabSession) stepPause() {
	if !sess.fast.Load() {
		time.Sleep(between(150*time.Millisecond, 450*time.Millisecond))
	}
}
