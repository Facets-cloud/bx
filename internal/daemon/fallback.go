package daemon

import (
	"context"
	"fmt"

	"github.com/chromedp/chromedp"
)

// overlayOKJS reports whether bx's on-page layer is really showing: still
// in the page, not hidden by page CSS, and in the top layer where the
// browser supports it.
const overlayOKJS = `(() => {
	const h = document.querySelector("bx-indicator");
	if (!h || !h.__bx || !h.isConnected) return false;
	const cs = getComputedStyle(h);
	if (cs.display === "none" || cs.visibility === "hidden" || +cs.opacity === 0) return false;
	return !h.showPopover || h.matches(":popover-open");
})()`

// reattachJS puts bx's layer back after the page removed or hid it,
// under another parent: 1 is <body>, 2 is the page's open modal dialog,
// whose own content pages leave alone.
const reattachJS = `((where) => {
	const h = window[Symbol.for("bx.host")];
	if (!h) return false;
	const parent = where === 1 ? document.body : document.querySelector(":modal");
	if (!parent) return false;
	h.style.setProperty("display", "block", "important");
	h.style.setProperty("visibility", "visible", "important");
	h.style.setProperty("opacity", "1", "important");
	parent.append(h);
	try { if (h.showPopover && !h.matches(":popover-open")) h.showPopover(); } catch (e) {}
	return true;
})(%d)`

// ensurePointer makes sure the page shows bx's pointer. Some pages remove
// or hide elements they didn't create; then bx puts its layer back under
// <body>, then inside the page's open modal, checking after each, at most
// once per action, so it never fights a page in a loop. The page gets its
// turn (its observers run) between bx's calls.
func (sess *tabSession) ensurePointer(ctx context.Context) {
	for where := 1; where <= 2; where++ {
		if ok, err := chromedp.Run(ctx, chromedp.Evaluate[bool](overlayOKJS)); err != nil || ok {
			return
		}
		if moved, err := chromedp.Run(ctx, chromedp.Evaluate[bool](fmt.Sprintf(reattachJS, where))); err != nil || !moved {
			continue
		}
	}
}
