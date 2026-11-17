package daemon

import (
	"context"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// takeoverPrefix goes in front of the title of a tab an agent session is
// working in, so the tab strip shows which tabs bx controls.
const takeoverPrefix = "🔵 bx · "

// stripTakeover gives back the page's own title, without bx's prefix, so
// agents see the title the page set.
func stripTakeover(title string) string {
	for strings.HasPrefix(title, takeoverPrefix) {
		title = strings.TrimPrefix(title, takeoverPrefix)
	}
	return title
}

// takeoverJS marks a page as under agent control until bx releases it: a
// blue glow pulsing around the viewport and the prefix on the title. The
// glow lives in a closed shadow root in the browser's top layer, like the
// pointer, and takes no clicks. A page that rewrites its title (an unread
// count) gets the prefix back. It runs on every new document of the tab
// and once on the current one; a second run on the same page does nothing.
const takeoverJS = `(() => {
	if (window !== window.top) return; // the tab's page, not its iframes
	const K = Symbol.for("bx.takeover"), P = "🔵 bx · ";
	if (window[K]) return;
	const t = window[K] = {};
	const strip = () => { let s = document.title; while (s.startsWith(P)) s = s.slice(P.length); return s; };
	const mark = () => { if (!document.title.startsWith(P)) document.title = P + strip(); };
	const setup = () => {
		if (window[K] !== t) return; // released before the page was ready
		const host = document.createElement("bx-takeover");
		host.setAttribute("aria-hidden", "true");
		host.style.cssText = "position:fixed;inset:0;width:100vw;height:100vh;margin:0;padding:0;border:0;background:transparent;overflow:visible;pointer-events:none;z-index:2147483646;display:block";
		const root = host.attachShadow({mode: "closed"});
		const glow = document.createElement("div");
		Object.assign(glow.style, {position: "fixed", inset: "0", pointerEvents: "none",
			boxShadow: "inset 0 0 0 3px #3b82f6, inset 0 0 40px 10px rgba(59,130,246,.55)"});
		glow.animate([{opacity: .7}, {opacity: 1}], {duration: 1600, direction: "alternate", iterations: Infinity, easing: "ease-in-out"});
		root.append(glow);
		document.documentElement.append(host);
		try { host.popover = "manual"; host.showPopover(); } catch (e) {} // no Popover API: the z-index still covers most pages
		t.host = host;
		mark();
		t.obs = new MutationObserver(mark);
		if (document.head) t.obs.observe(document.head, {subtree: true, childList: true, characterData: true});
	};
	// hide and show keep the glow out of bx's screenshots and PDFs.
	t.hide = () => { if (t.host) t.host.style.visibility = "hidden"; };
	t.show = () => { if (t.host) t.host.style.visibility = ""; };
	// release gives the page its title back and fades the glow out.
	t.release = () => {
		delete window[K];
		if (t.obs) t.obs.disconnect();
		if (document.title.startsWith(P)) document.title = strip();
		const h = t.host;
		if (!h) return;
		h.animate([{opacity: 1}, {opacity: 0}], {duration: 400, easing: "ease-out", fill: "forwards"});
		setTimeout(() => h.remove(), 400);
	};
	if (document.readyState === "loading") addEventListener("DOMContentLoaded", setup, {once: true});
	else setup();
})()`

const (
	hideTakeoverJS    = `window[Symbol.for("bx.takeover")]?.hide()`
	showTakeoverJS    = `window[Symbol.for("bx.takeover")]?.show()`
	releaseTakeoverJS = `window[Symbol.for("bx.takeover")]?.release()`
)

// markTakeover puts the takeover marker on the tab, on this page and on
// every page it loads, unless it is already there. Best effort.
func (sess *tabSession) markTakeover() {
	sess.tmu.Lock()
	defer sess.tmu.Unlock()
	if sess.takeover != "" {
		return
	}
	ctx, cancel := context.WithTimeout(sess.ctx, 2*time.Second)
	defer cancel()
	res, err := cdp.Call(ctx, sess.t, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: takeoverJS})
	if err != nil {
		return
	}
	sess.takeover = res.Identifier
	_, _ = chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](takeoverJS))
}

// releaseTakeover takes the marker off the tab: pages it loads no longer
// get it, and the current one gets its title back and its glow fades out.
func (sess *tabSession) releaseTakeover(ctx context.Context) {
	sess.tmu.Lock()
	defer sess.tmu.Unlock()
	if sess.takeover == "" {
		return
	}
	_, _ = cdp.Call(ctx, sess.t, page.RemoveScriptToEvaluateOnNewDocument, page.RemoveScriptToEvaluateOnNewDocumentParams{Identifier: sess.takeover})
	sess.takeover = ""
	_, _ = chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](releaseTakeoverJS))
}
