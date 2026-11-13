package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// indicatorJS shows what bx is doing on the page itself: a pointer with a
// "bx" name tag that glides along a curve to the element and stays where
// it stopped, a tag that says what it is doing ("bx · Click "Save""), a
// ripple when it clicks. It lives in a
// closed shadow root that screen readers and snapshots skip, and takes no
// clicks. It returns the pointer's new position and how long the glide
// takes, or null when the pointer did not move.
const indicatorJS = `((verb, el, last, quick) => {
	// Bring the element into view first, so whoever watches the tab sees the
	// change happen: a page scrolled to the bottom still shows an edit at the top.
	if (el && el !== document.body && el.getBoundingClientRect) {
		const r = el.getBoundingClientRect();
		if (r.top < 0 || r.bottom > innerHeight || r.left < 0 || r.right > innerWidth) el.scrollIntoView({block: "center", inline: "nearest"});
	}
	// Where a person would aim: near the middle (near the start of a wide
	// field), a little off from one action to the next.
	let tx = null, ty = null;
	// A hidden control (a file input behind a styled "Choose file" label)
	// has no box: point at its visible label instead, or don't move.
	if (el && el.getBoundingClientRect && el.getBoundingClientRect().width === 0) {
		const lab = el.labels && [...el.labels].find(l => l.getBoundingClientRect().width > 0);
		el = lab || null;
	}
	if (el && el.getBoundingClientRect) {
		const r = el.getBoundingClientRect();
		tx = r.x + Math.min(r.width / 2, 24) + (Math.random() - .5) * Math.min(r.width * .3, 12);
		ty = r.y + r.height / 2 + (Math.random() - .5) * Math.min(r.height * .3, 6);
	}
	const ui = ` + pointerUIJS + `(last);
	let name = "";
	if (el && el.getAttribute) {
		const label = el.labels && el.labels[0] ? el.labels[0].innerText : "";
		name = el.getAttribute("aria-label") || label || el.innerText || el.placeholder || el.name || el.title || "";
		name = name.trim().replace(/\s+/g, " ");
		// Don't read out what a recording blurs.
		const bs = window[Symbol.for("bx.blurSel")];
		if (bs && el.closest && el.closest(bs)) name = "";
		if (name.length > 40) name = name.slice(0, 39) + "…";
	}
	ui.say(verb + (name ? " \u201c" + name + "\u201d" : ""));
	if (!el || !el.getBoundingClientRect) return null;
	const r = el.getBoundingClientRect();
	const x = tx, y = ty;
	ui.quick = quick;
	ui.anchor(el, x - r.x, y - r.y);
	const ms = ui.glide(x, y);
	if (/click/i.test(verb)) setTimeout(() => {
		Object.assign(ui.ripple.style, {left: x + "px", top: y + "px"});
		ui.ripple.animate([{opacity: 1, transform: "scale(.4)"}, {opacity: 0, transform: "scale(1.7)"}], {duration: 500, easing: "ease-out"});
	}, ms);
	return [x, y, ms];
})(%s, %s, %s, %t)`

// pointerUIJS returns the overlay's parts and its two moves, glide (along
// a curve, eased, frame by frame) and jump, drawing the overlay on first
// use with the pointer at last ([x, y], where it stood on the previous
// page) or in the middle of the window.
const pointerUIJS = `((last) => {
	let host = document.querySelector("bx-indicator");
	if (host && host.__bx) { host.__bx.raise(); return host.__bx; }
	host = document.createElement("bx-indicator");
	host.setAttribute("aria-hidden", "true");
	// The layer covers the viewport and takes no clicks. It goes in the
	// browser's top layer as a popover, the layer modal dialogs and
	// fullscreen use, so a page's modal (LinkedIn's post composer) can't
	// cover the pointer the way it covers anything with a mere z-index.
	host.style.cssText = "position:fixed;inset:0;width:100vw;height:100vh;margin:0;padding:0;border:0;background:transparent;overflow:visible;pointer-events:none;z-index:2147483647;display:block";
	const root = host.attachShadow({mode: "closed"});
	const blue = "#3b82f6";
	const ripple = document.createElement("div");
	Object.assign(ripple.style, {position: "fixed", width: "28px", height: "28px", margin: "-14px 0 0 -14px", borderRadius: "50%%",
		border: "2px solid " + blue, zIndex: 2147483646, pointerEvents: "none", opacity: "0"});
	// The pointer and its name tag move together as one layer, by transform.
	const ptr = document.createElement("div");
	Object.assign(ptr.style, {position: "fixed", left: "0", top: "0", zIndex: 2147483647, pointerEvents: "none", willChange: "transform", transition: "opacity .4s"});
	// An arrow drawn in SVG (no innerHTML, so pages with Trusted Types
	// accept it), tip at the top-left corner.
	const ns = "http://www.w3.org/2000/svg";
	const svg = document.createElementNS(ns, "svg"), path = document.createElementNS(ns, "path");
	svg.setAttribute("width", "22"); svg.setAttribute("height", "26"); svg.setAttribute("viewBox", "0 0 22 26");
	path.setAttribute("d", "M2 2 L2 21 L7.5 16 L11 24 L14.5 22.5 L11 14.5 L18.5 14.5 Z");
	path.setAttribute("fill", blue); path.setAttribute("stroke", "#fff"); path.setAttribute("stroke-width", "1.6"); path.setAttribute("stroke-linejoin", "round");
	svg.append(path);
	svg.style.filter = "drop-shadow(0 2px 3px rgba(0,0,0,.35))";
	const tag = document.createElement("div"), badge = document.createElement("b"), text = document.createElement("span");
	badge.textContent = "bx";
	Object.assign(tag.style, {position: "absolute", left: "16px", top: "20px", display: "flex", alignItems: "center",
		font: "600 12px/1 ui-sans-serif, -apple-system, system-ui, sans-serif", color: "#fff", background: blue,
		padding: "5px 9px", borderRadius: "4px 10px 10px 10px", whiteSpace: "nowrap", boxShadow: "0 4px 14px rgba(0,0,0,.25)"});
	Object.assign(badge.style, {font: "800 12px/1 ui-monospace, Menlo, monospace", letterSpacing: "-.02em"});
	Object.assign(text.style, {maxWidth: "0", marginLeft: "0", overflow: "hidden", textOverflow: "ellipsis", opacity: "0",
		transition: "max-width .3s ease, margin-left .3s ease, opacity .25s ease"});
	tag.append(badge, text);
	// A ring behind the arrow's tip that pulses while the agent thinks.
	const halo = document.createElement("div");
	Object.assign(halo.style, {position: "absolute", left: "-10px", top: "-10px", width: "24px", height: "24px", borderRadius: "50%%",
		background: "rgba(59,130,246,.35)", opacity: "0", pointerEvents: "none"});
	ptr.append(halo, svg, tag);
	root.append(ripple, ptr);
	document.documentElement.append(host);
	window[Symbol.for("bx.host")] = host; // kept, so bx can put it back if the page removes it
	try { host.popover = "manual"; host.showPopover(); } catch (e) {} // no Popover API: the z-index still covers most pages

	let [cx, cy] = last || [innerWidth / 2, innerHeight / 2];
	let frame = 0;
	const put = (x, y) => { cx = x; cy = y; ptr.style.transform = "translate3d(" + x + "px," + y + "px,0)"; host.__pos = [x, y]; };
	// The element the pointer is on, and where on it. A pointer pinned to
	// the screen drifts off its element as soon as the page scrolls.
	let target = null, offX = 0, offY = 0;
	const follow = () => {
		if (frame || !target || !target.isConnected) return; // mid-glide, or the element is gone: stay put
		const r = target.getBoundingClientRect();
		if (!r.width && !r.height) return;
		put(r.x + offX, r.y + offY);
	};
	addEventListener("scroll", () => requestAnimationFrame(follow), {capture: true, passive: true});
	addEventListener("resize", () => requestAnimationFrame(follow), {passive: true});
	new ResizeObserver(() => requestAnimationFrame(follow)).observe(document.documentElement);
	const ease = t => t < .5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;
	const ui = {host, root, ripple,
		// raise puts the layer back on top of the top layer: a modal or popover
		// the page opened after it would otherwise sit above the pointer.
		raise() {
			if (!host.showPopover) return;
			try {
				const other = document.querySelector(":modal, :fullscreen, :popover-open:not(bx-indicator)");
				if (other || !host.matches(":popover-open")) {
					if (host.matches(":popover-open")) host.hidePopover();
					host.showPopover();
				}
			} catch (e) {}
		},
		// glide moves the pointer to x, y along a slight arc and returns how
		// long that takes in ms.
		glide(x, y) {
			cancelAnimationFrame(frame);
			const x0 = cx, y0 = cy, dx = x - x0, dy = y - y0, d = Math.hypot(dx, dy);
			if (d < 1) return 0;
			if (this.quick) { put(x, y); return 0; } // fast mode: jump there, no wait
			const ms = Math.round(Math.min(650, Math.max(250, 200 + d * .6)));
			// The arc's control point sits off the straight line by a fraction of its length.
			const bend = Math.min(80, d * .18), mx = x0 + dx / 2 - dy / d * bend, my = y0 + dy / 2 + dx / d * bend;
			const t0 = performance.now();
			const step = now => {
				const t = Math.min(1, (now - t0) / ms), e = ease(t), u = 1 - e;
				put(u * u * x0 + 2 * u * e * mx + e * e * x, u * u * y0 + 2 * u * e * my + e * e * y);
				if (t < 1) frame = requestAnimationFrame(step);
				else { frame = 0; follow(); } // the page may have moved during the glide
			};
			frame = requestAnimationFrame(step);
			return ms;
		},
		jump(x, y) { cancelAnimationFrame(frame); target = null; put(x, y); },
		// land ends a glide on its element. bx calls it when the glide's time
		// is up: a hidden or covered window pauses animation frames and slows
		// timers, which would leave the pointer partway.
		land() { cancelAnimationFrame(frame); frame = 0; follow(); },
		// anchor pins the pointer, at offset dx, dy, to el: when the page
		// scrolls, resizes or reflows, the pointer moves with it instead of
		// staying at the same spot on the screen.
		anchor(el, dx, dy) { target = el; offX = dx; offY = dy; },
		// say shows what bx is doing in the name tag, then shrinks back to "bx".
		// say shows what bx is doing in the tag, until the next status.
		say(msg) {
			clearTimeout(host.__think); clearTimeout(host.__rest);
			halo.getAnimations().forEach(a => a.cancel());
			Object.assign(ptr.style, {opacity: "1"});
			text.textContent = msg;
			Object.assign(text.style, {maxWidth: "360px", marginLeft: "6px", opacity: "1"});
		},
		// idle marks the end of a bx action. If no new action starts soon,
		// the agent is deciding what to do next: the tag says so, with moving
		// dots and a soft pulse. After 20 quiet seconds the agent has stopped
		// using bx, so the pointer fades out and the overlay goes away; the
		// next action draws it again where it was.
		idle(msg, quiet) {
			clearTimeout(host.__think); clearTimeout(host.__rest);
			host.__think = setTimeout(() => {
				text.textContent = msg;
				const dots = document.createElement("span");
				text.append(dots);
				dots.animate([{opacity: .2}, {opacity: 1}], {duration: 600, iterations: Infinity, direction: "alternate"});
				dots.textContent = "...";
				halo.animate([{transform: "scale(.6)", opacity: .55}, {transform: "scale(1.5)", opacity: 0}], {duration: 1400, iterations: Infinity});
			}, 700);
			host.__rest = setTimeout(() => ui.leave(), quiet);
		},
		// leave fades the overlay out and removes it.
		leave() {
			clearTimeout(host.__think); clearTimeout(host.__rest);
			halo.getAnimations().forEach(a => a.cancel());
			Object.assign(ptr.style, {opacity: "0"});
			setTimeout(() => host.remove(), 450);
		},
		// done says the agent has finished, with its summary if any, then leaves.
		done(summary) {
			ui.say(summary ? "Done: " + summary : "Done");
			host.__rest = setTimeout(() => ui.leave(), summary ? 3000 : 1500);
		}};
	put(cx, cy);
	host.__bx = ui;
	return ui;
})`

// pointJS puts the pointer at x, y at once, for drags that move it step
// by step.
const pointJS = `((x, y) => { ` + pointerUIJS + `(null).jump(x, y); })(%f, %f)`

// focusedJS is the focused element, inside shadow roots too.
const focusedJS = `(() => { let a = document.activeElement; while (a && a.shadowRoot && a.shadowRoot.activeElement) a = a.shadowRoot.activeElement; return a; })()`

// removeIndicatorJS takes the indicator off the page, so screenshots show
// the page as it is.
const removeIndicatorJS = `document.querySelectorAll("bx-indicator").forEach(e => e.remove())`

// indicate scrolls the element the expression js gives (or, when js is
// empty, nothing; for typing and keys pass focusedJS) into view, then shows
// verb on the page and glides the pointer to it. In
// both modes the pointer is shown, so a person can always see what bx is
// doing. In watch mode the glide takes 250-650ms and the real mouse travels
// the same path; in fast mode the pointer hops in 120ms and ok is false, so
// the action itself runs without human pacing. It returns once the pointer
// has arrived. It is best effort: a page that refuses it still gets the action.
func (sess *tabSession) indicate(ctx context.Context, verb, js string) (x, y float64, ok bool) {
	fast := sess.fast.Load()
	if js == "" {
		js = "null"
	}
	v, _ := json.Marshal(sess.label(verb))
	last := "null"
	if sess.pointer != nil {
		last = fmt.Sprintf("[%f, %f]", sess.pointer[0], sess.pointer[1])
	}
	got, _ := chromedp.Run(ctx, chromedp.Evaluate[[]float64](fmt.Sprintf(indicatorJS, v, js, last, fast)))
	if len(got) != 3 {
		return 0, 0, false
	}
	glide := time.Duration(got[2]) * time.Millisecond
	sess.pointer = &[2]float64{got[0], got[1]}
	if fast {
		time.Sleep(glide)
	} else {
		sess.moveMouse(ctx, got[0], got[1], glide)
	}
	_, _ = chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](landJS))
	sess.ensurePointer(ctx)
	return got[0], got[1], !fast
}

// point moves the pointer to x, y at once, best effort.
func (sess *tabSession) point(ctx context.Context, x, y float64) {
	_, _ = chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](fmt.Sprintf(pointJS, x, y)))
	sess.pointer = &[2]float64{x, y}
}

// selectMenuJS draws, on the overlay, the options of a native select
// unfolding under it, and glides the pointer to the one matching v (by
// value or visible text), which then lights up. Native select menus are
// drawn by the operating system, out of reach of the page and of bx, so
// this shows what a person would see while bx sets the value itself. It
// returns where the pointer went and how long the glide takes, or null when
// no option matches.
const selectMenuJS = `((el, v) => {
	const opts = [...el.options];
	let i = opts.findIndex(o => o.value === v);
	if (i < 0) i = opts.findIndex(o => o.text.trim() === v);
	if (i < 0) return null;
	const ui = ` + pointerUIJS + `(null);
	const r = el.getBoundingClientRect();
	const menu = document.createElement("div");
	Object.assign(menu.style, {position: "fixed", left: r.x + "px", top: r.bottom + 4 + "px", minWidth: r.width + "px", maxHeight: "260px",
		overflow: "hidden", zIndex: 2147483646, pointerEvents: "none", background: "#fff", color: "#1c1c1c", border: "1px solid #d0d4dc",
		borderRadius: "8px", boxShadow: "0 12px 32px rgba(0,0,0,.18)", font: "14px/1.2 -apple-system, system-ui, sans-serif",
		padding: "4px", transformOrigin: "top", transform: "scaleY(.6)", opacity: "0", transition: "transform .16s ease-out, opacity .16s"});
	const rows = opts.map(o => {
		const d = document.createElement("div");
		d.textContent = o.text;
		Object.assign(d.style, {padding: "7px 10px", borderRadius: "5px", whiteSpace: "nowrap"});
		menu.append(d);
		return d;
	});
	ui.root.append(menu);
	requestAnimationFrame(() => Object.assign(menu.style, {transform: "none", opacity: "1"}));
	menu.scrollTop = Math.max(0, rows[i].offsetTop - 110);
	const rr = rows[i].getBoundingClientRect();
	const x = rr.x + Math.min(rr.width / 2, 40), y = rr.y + rr.height / 2;
	ui.say("Choose \u201c" + opts[i].text.trim() + "\u201d");
	const ms = ui.glide(x, y);
	setTimeout(() => {
		Object.assign(rows[i].style, {background: "#3b82f6", color: "#fff"});
		Object.assign(ui.ripple.style, {left: x + "px", top: y + "px"});
		ui.ripple.animate([{opacity: 1, transform: "scale(.4)"}, {opacity: 0, transform: "scale(1.7)"}], {duration: 450, easing: "ease-out"});
	}, ms);
	setTimeout(() => { menu.style.opacity = "0"; }, ms + 260);
	setTimeout(() => menu.remove(), ms + 460);
	return [x, y, ms];
})(%s, %s)`

// showSelect plays the select menu for e choosing value, in watch mode:
// the pointer clicks the field, the options unfold, and the pointer moves
// to the chosen one. It returns once the option is lit, best effort.
func (sess *tabSession) showSelect(ctx context.Context, e elem, value string) {
	if _, _, ok := sess.indicate(ctx, "Click", e.js); !ok {
		return
	}
	time.Sleep(120 * time.Millisecond) // the click lands, then the menu opens
	v, _ := json.Marshal(value)
	got, _ := chromedp.Run(ctx, chromedp.Evaluate[[]float64](fmt.Sprintf(selectMenuJS, e.js, v)))
	if len(got) != 3 {
		return
	}
	sess.moveMouse(ctx, got[0], got[1], time.Duration(got[2])*time.Millisecond)
	sess.pointer = &[2]float64{got[0], got[1]}
	time.Sleep(180 * time.Millisecond) // let the choice show before the value changes
}

// sayJS shows msg in the pointer's tag without moving the pointer.
const sayJS = `((msg, last) => { ` + pointerUIJS + `(last).say(msg); })(%s, %s)`

// say shows what bx is doing in the pointer's tag, for the actions that
// have no element to point at: reading, scripts, waits, screenshots. It
// also puts the pointer back after a page loads. Best effort.
func (sess *tabSession) say(ctx context.Context, msg string) {
	m, _ := json.Marshal(sess.label(msg))
	last := "null"
	if sess.pointer != nil {
		last = fmt.Sprintf("[%f, %f]", sess.pointer[0], sess.pointer[1])
	}
	_, _ = chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](fmt.Sprintf(sayJS, m, last)))
	sess.ensurePointer(ctx)
}

// label puts the batch step, if one is running, in front of msg: "3/32 · Click".
func (sess *tabSession) label(msg string) string {
	if step, _ := sess.step.Load().(string); step != "" {
		return step + " · " + msg
	}
	return msg
}

// idleJS marks the end of a bx action on the page's pointer.
const idleJS = `((last, msg, quiet) => { ` + pointerUIJS + `(last).idle(msg, quiet); })(%s, %s, %d)`

// idle tells the page's pointer that an action ended: if no new one starts
// within a moment, it shows that the agent is thinking. Best effort.
func (sess *tabSession) idle() {
	last := "null"
	if sess.pointer != nil {
		last = fmt.Sprintf("[%f, %f]", sess.pointer[0], sess.pointer[1])
	}
	// A session the agent named is clearly still open: say what it's
	// working on and wait longer before giving up on it.
	msg, quiet := "Waiting for the agent", 20*time.Second
	if title, _ := sess.title.Load().(string); title != "" {
		msg, quiet = "Working on: "+clipText(title, 48), 2*time.Minute
	}
	m, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(sess.ctx, 2*time.Second)
	defer cancel()
	_, _ = chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](fmt.Sprintf(idleJS, last, m, quiet.Milliseconds())))
}

// doneJS ends the overlay on a page, if there is one.
const doneJS = `((summary) => { const h = document.querySelector("bx-indicator"); if (h && h.__bx) h.__bx.done(summary); })(%s)`

// done tells every page bx has drawn on that the agent finished: it called
// the done tool, or its MCP session ended. The pointer shows the summary,
// if any, then goes away. Best effort.
// Only the tabs in touched, when given: one agent's done doesn't end
// another agent's pointer.
func (s *server) done(summary string, touched map[target.ID]bool) (any, error) {
	if r := []rune(strings.TrimSpace(summary)); len(r) > 60 {
		summary = string(r[:59]) + "…"
	}
	q, _ := json.Marshal(strings.TrimSpace(summary))
	s.mu.Lock()
	var all []*tabSession
	for id, sess := range s.sessions {
		if touched == nil || touched[id] {
			all = append(all, sess)
		}
	}
	s.mu.Unlock()
	for _, sess := range all {
		ctx, cancel := context.WithTimeout(sess.ctx, 2*time.Second)
		_, _ = chromedp.Run(ctx, chromedp.Evaluate[chromedp.Void](fmt.Sprintf(doneJS, q)))
		cancel()
	}
	return nil, nil
}

// landJS ends the pointer's glide on its element.
const landJS = `(() => { const h = document.querySelector("bx-indicator"); if (h && h.__bx) h.__bx.land(); })()`
