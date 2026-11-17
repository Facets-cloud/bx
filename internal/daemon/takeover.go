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
// count) gets the prefix back. While marked, the tab's favicon is the bx
// tag: the page's own icon links come out of the document (a page that
// swaps its icon, as Gmail does for unread counts, has the new one taken
// out too) and go back on release; a page that had none gets a link to
// its /favicon.ico, since the browser would otherwise keep showing ours.
// It runs on every new document of the tab and once on the current one; a
// second run on the same page does nothing.
const takeoverJS = `(() => {
	if (window !== window.top) return; // the tab's page, not its iframes
	const K = Symbol.for("bx.takeover"), P = "🔵 bx · ";
	if (window[K]) return;
	const t = window[K] = {};
	const strip = () => { let s = document.title; while (s.startsWith(P)) s = s.slice(P.length); return s; };
	const ICON = 'link[rel~="icon" i]';
	t.icon = document.createElement("link");
	t.icon.rel = "icon";
	t.icon.href = "data:image/svg+xml," + encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><path d="M3 0 H25 Q32 0 32 7 V25 Q32 32 25 32 H7 Q0 32 0 25 V3 Q0 0 3 0 Z" fill="#3b82f6"/><text x="16" y="21.5" text-anchor="middle" font-family="ui-monospace, Menlo, monospace" font-weight="800" font-size="15" letter-spacing="-.5" fill="#fff">bx</text></svg>');
	t.icons = []; // the page's latest icon links, put back on release
	const mark = () => {
		if (!document.title.startsWith(P)) document.title = P + strip();
		if (!document.head) return;
		const own = [...document.querySelectorAll(ICON)].filter(l => l !== t.icon);
		if (own.length) { t.icons = own; own.forEach(l => l.remove()); }
		if (t.icon.parentNode !== document.head) document.head.append(t.icon);
	};
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
		if (!h) return; // setup never ran: no glow, no icon swap
		t.icon.remove();
		if (document.head && !t.icons.length) { // the browser keeps a removed icon unless it gets another one
			const l = document.createElement("link");
			l.rel = "icon";
			l.href = location.origin + "/favicon.ico";
			t.icons = [l];
		}
		if (document.head) document.head.append(...t.icons);
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
