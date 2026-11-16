package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"

	"github.com/Facets-cloud/bx/internal/exit"
)

// MouseOptions say what to do at a point. X and Y are pixels of a
// screenshot of the tab, so an agent can act on what it sees where there
// are no elements to name: a canvas, a map, a chart, a spreadsheet grid.
type MouseOptions struct {
	Action string  // click, double, right, move or wheel
	X, Y   float64 // screenshot pixels
	DX, DY float64 // for wheel: how far to scroll, in screenshot pixels
}

// parseMouse reads the mouse op's arguments.
func parseMouse(a map[string]string) (MouseOptions, error) {
	o := MouseOptions{Action: a["action"]}
	if o.Action == "" {
		o.Action = "click"
	}
	nums := []struct {
		key string
		v   *float64
		req bool
	}{{"x", &o.X, true}, {"y", &o.Y, true}, {"dx", &o.DX, false}, {"dy", &o.DY, false}}
	for _, n := range nums {
		if a[n.key] == "" {
			if n.req {
				return o, exit.New(exit.Validation, "mouse needs %s", n.key)
			}
			continue
		}
		f, err := strconv.ParseFloat(a[n.key], 64)
		if err != nil {
			return o, exit.New(exit.Validation, "mouse: %s must be a number, not %q", n.key, a[n.key])
		}
		*n.v = f
	}
	switch o.Action {
	case "click", "double", "right", "move", "wheel":
		return o, nil
	}
	return o, exit.New(exit.Validation, "mouse action must be click, double, right, move or wheel, not %q", o.Action)
}

// mouse clicks, moves or scrolls at a point of the tab.
func (s *server) mouse(id string, o MouseOptions, timeout time.Duration) (ActResult, error) {
	return s.actOn(id, timeout, func(ctx context.Context, sess *tabSession) error {
		dpr, err := chromedp.Run(ctx, chromedp.Evaluate[float64]("window.devicePixelRatio"))
		if err != nil || dpr <= 0 {
			dpr = 1
		}
		x, y := o.X/dpr, o.Y/dpr
		sess.point(ctx, x, y)
		sess.say(ctx, mouseVerb(o.Action))
		sess.clicked.Store(time.Now().UnixNano())
		// A point inside a frame that runs in its own process goes to the
		// frame's own target, in its coordinates (see frame).
		t, tx, ty := sess.t, x, y
		if f, fx, fy := sess.frameAt(ctx, x, y); f != nil {
			t, tx, ty = f.t, fx, fy
		}
		if err := dispatchMouse(ctx, t, o, tx, ty, dpr); err != nil {
			return upstream(fmt.Sprintf("mouse %s at %g,%g", o.Action, o.X, o.Y), err)
		}
		return nil
	})
}

func mouseVerb(action string) string {
	switch action {
	case "double":
		return "Double-click"
	case "right":
		return "Right-click"
	case "move":
		return "Move"
	case "wheel":
		return "Scroll"
	}
	return "Click"
}

// dispatchMouse sends the input events for one mouse action at x, y (CSS
// pixels) to target t: the tab, or a frame of it.
func dispatchMouse(ctx context.Context, t *chromedp.Target, o MouseOptions, x, y, dpr float64) error {
	ev := func(p input.DispatchMouseEventParams) error {
		p.X, p.Y = x, y
		_, err := cdp.Call(ctx, t, input.DispatchMouseEvent, p)
		return err
	}
	if err := ev(input.DispatchMouseEventParams{Type: input.DispatchMouseEventTypeMouseMoved}); err != nil {
		return err
	}
	switch o.Action {
	case "move":
		return nil
	case "wheel":
		// Sent raw: the browser needs deltaX and deltaY even when one is 0,
		// which the typed params leave out.
		p, _ := json.Marshal(map[string]any{"type": "mouseWheel", "x": x, "y": y, "deltaX": o.DX / dpr, "deltaY": o.DY / dpr})
		return t.Call(ctx, "Input.dispatchMouseEvent", json.RawMessage(p), nil)
	}
	// buttons is what is held while pressed (see held): 1 left, 2 right.
	button, buttons, clicks := input.MouseButtonLeft, int64(1), 1
	if o.Action == "right" {
		button, buttons = input.MouseButtonRight, 2
	}
	if o.Action == "double" {
		clicks = 2
	}
	for c := 1; c <= clicks; c++ {
		if err := ev(input.DispatchMouseEventParams{Type: input.DispatchMouseEventTypeMousePressed, Button: button, Buttons: buttons, ClickCount: int64(c)}); err != nil {
			return err
		}
		if err := ev(input.DispatchMouseEventParams{Type: input.DispatchMouseEventTypeMouseReleased, Button: button, ClickCount: int64(c)}); err != nil {
			return err
		}
	}
	return nil
}
