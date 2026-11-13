package daemon

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
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
	if !sess.keyTyping.Load() {
		_, err := chromedp.Run(ctx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, err := cdp.Call(ctx, t, input.InsertText, input.InsertTextParams{Text: text})
			return err
		}))
		return err
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
