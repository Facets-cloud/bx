package daemon

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/performance"
	"github.com/chromedp/cdproto/runtime"

	"github.com/Facets-cloud/bx/internal/exit"
)

// Cookie is one cookie the page can see; long values are cut.
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  string `json:"expires"` // RFC 3339, or "session"
	HTTPOnly bool   `json:"http_only"`
	Secure   bool   `json:"secure"`
	SameSite string `json:"same_site,omitempty"`
	Size     int64  `json:"size"`
}

// Storage is what the page stores: cookies and web storage.
type Storage struct {
	URL     string            `json:"url"`
	Cookies []Cookie          `json:"cookies"`
	Local   map[string]string `json:"local_storage"`
	Session map[string]string `json:"session_storage"`
}

const maxStoredValue = 200

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(" + strconv.Itoa(len(s)) + " bytes)"
}

// storage reads the cookies the page's URLs get, and its localStorage and
// sessionStorage. It changes nothing.
func (s *server) storage(id string, timeout time.Duration) (Storage, error) {
	sess, err := s.lockSession(id)
	if err != nil {
		return Storage{}, err
	}
	defer sess.mu.Unlock()
	if err := sess.errIfDialog(); err != nil {
		return Storage{}, err
	}
	ctx, cancel := context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	res, err := cdp.Call(ctx, sess.t, network.GetCookies, network.GetCookiesParams{})
	if err != nil {
		return Storage{}, upstream("read cookies", err)
	}
	st := Storage{Cookies: []Cookie{}}
	for _, c := range res.Cookies {
		exp := "session"
		if !c.Session && c.Expires > 0 {
			exp = time.Unix(int64(c.Expires), 0).UTC().Format(time.RFC3339)
		}
		st.Cookies = append(st.Cookies, Cookie{Name: c.Name, Value: cut(c.Value, maxStoredValue), Domain: c.Domain, Path: c.Path,
			Expires: exp, HTTPOnly: c.HTTPOnly, Secure: c.Secure, SameSite: string(c.SameSite), Size: c.Size})
	}
	var web struct {
		URL     string            `json:"url"`
		Local   map[string]string `json:"local"`
		Session map[string]string `json:"session"`
	}
	if err := evalJSON(ctx, sess, storageJS, &web); err != nil {
		return Storage{}, err
	}
	st.URL, st.Local, st.Session = web.URL, web.Local, web.Session
	for _, m := range []map[string]string{st.Local, st.Session} {
		for k, v := range m {
			m[k] = cut(v, maxStoredValue)
		}
	}
	return st, nil
}

// storageJS copies both web storages; a sandboxed page can refuse access.
const storageJS = `(() => {
	const dump = name => { try { return Object.fromEntries(Object.entries(window[name])); } catch { return {}; } };
	return {url: location.href, local: dump("localStorage"), session: dump("sessionStorage")};
})()`

func evalJSON(ctx context.Context, sess *tabSession, expr string, v any) error {
	yes := true
	res, err := cdp.Call(ctx, sess.t, runtime.Evaluate, runtime.EvaluateParams{Expression: expr, ReturnByValue: &yes, AwaitPromise: &yes})
	if err != nil {
		return upstream("evaluate", err)
	}
	if res.ExceptionDetails != nil {
		return exit.New(exit.Upstream, "page script failed: %s", res.ExceptionDetails.Text)
	}
	if res.Result == nil || len(res.Result.Value) == 0 {
		return exit.New(exit.Upstream, "page script returned nothing")
	}
	return json.Unmarshal(res.Result.Value, v)
}

// EmulateOptions say what to emulate; an empty field leaves that alone,
// and "off" turns it off.
type EmulateOptions struct {
	Viewport string // WIDTHxHEIGHT[@SCALE][,mobile]
	Network  string // offline, slow-3g, fast-3g
	CPU      string // slowdown factor, such as 4
	Geo      string // LATITUDE,LONGITUDE
	Color    string // dark or light
	Reset    bool
}

// networkPresets match the DevTools throttling presets.
var networkPresets = map[string]network.Conditions{
	"offline": {Offline: true},
	"slow-3g": {Latency: 2000, DownloadThroughput: 50000, UploadThroughput: 50000},
	"fast-3g": {Latency: 562.5, DownloadThroughput: 180000, UploadThroughput: 84375},
}

// emulate changes how the tab looks to the page: viewport, network speed,
// CPU speed, location and color scheme. Settings last while bx stays
// attached to the tab (until bx stop or the browser quits).
func (s *server) emulate(id string, o EmulateOptions, timeout time.Duration) (map[string]string, error) {
	if o.Reset {
		o = EmulateOptions{Viewport: "off", Network: "off", CPU: "off", Geo: "off", Color: "off"}
	}
	steps, err := emulationSteps(o)
	if err != nil {
		return nil, err
	}
	if len(steps) == 0 {
		return nil, exit.New(exit.Validation, "emulate needs at least one of --viewport, --network, --cpu, --geo, --color or --reset")
	}
	sess, err := s.lockSession(id)
	if err != nil {
		return nil, err
	}
	defer sess.mu.Unlock()
	ctx, cancel := context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	done := map[string]string{}
	for _, st := range steps {
		if err := st.run(ctx, sess.t); err != nil {
			return done, upstream("emulate "+st.name, err)
		}
		done[st.name] = st.value
	}
	return done, nil
}

type emuStep struct {
	name, value string
	run         func(ctx context.Context, t cdp.Session) error
}

func call[P, R any](c cdp.Command[P, R], p P) func(context.Context, cdp.Session) error {
	return func(ctx context.Context, t cdp.Session) error { _, err := cdp.Call(ctx, t, c, p); return err }
}

func emulationSteps(o EmulateOptions) ([]emuStep, error) {
	var steps []emuStep
	for _, f := range []struct {
		name, value string
		build       func(string) (func(context.Context, cdp.Session) error, error)
	}{
		{"viewport", o.Viewport, viewportStep}, {"network", o.Network, networkStep},
		{"cpu", o.CPU, cpuStep}, {"geo", o.Geo, geoStep}, {"color", o.Color, colorStep},
	} {
		if f.value == "" {
			continue
		}
		run, err := f.build(f.value)
		if err != nil {
			return nil, err
		}
		steps = append(steps, emuStep{f.name, f.value, run})
	}
	return steps, nil
}

func viewportStep(v string) (func(context.Context, cdp.Session) error, error) {
	if v == "off" {
		return func(ctx context.Context, t cdp.Session) error {
			if _, err := cdp.Call(ctx, t, emulation.SetTouchEmulationEnabled, emulation.SetTouchEmulationEnabledParams{}); err != nil {
				return err
			}
			_, err := cdp.Call(ctx, t, emulation.ClearDeviceMetricsOverride, cdp.Empty{})
			return err
		}, nil
	}
	w, h, scale, mobile, err := parseViewport(v)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, t cdp.Session) error {
		if _, err := cdp.Call(ctx, t, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
			Width: int64(w), Height: int64(h), DeviceScaleFactor: scale, Mobile: mobile}); err != nil {
			return err
		}
		_, err := cdp.Call(ctx, t, emulation.SetTouchEmulationEnabled, emulation.SetTouchEmulationEnabledParams{Enabled: mobile, MaxTouchPoints: 5})
		return err
	}, nil
}

// parseViewport reads WIDTHxHEIGHT[@SCALE][,mobile].
func parseViewport(v string) (w, h int, scale float64, mobile bool, err error) {
	bad := exit.New(exit.Validation, "--viewport must look like 390x844, 390x844@3 or 390x844@3,mobile")
	size, mobile := strings.CutSuffix(v, ",mobile")
	size, scaleStr, hasScale := strings.Cut(size, "@")
	ws, hs, ok := strings.Cut(size, "x")
	if !ok {
		return 0, 0, 0, false, bad
	}
	scale = 1
	if hasScale {
		if scale, err = strconv.ParseFloat(scaleStr, 64); err != nil {
			return 0, 0, 0, false, bad
		}
	}
	w, err1 := strconv.Atoi(ws)
	h, err2 := strconv.Atoi(hs)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 || scale <= 0 {
		return 0, 0, 0, false, bad
	}
	return w, h, scale, mobile, nil
}

func networkStep(v string) (func(context.Context, cdp.Session) error, error) {
	conds := []*network.Conditions{}
	if v != "off" {
		c, ok := networkPresets[v]
		if !ok {
			return nil, exit.New(exit.Validation, "--network must be offline, slow-3g, fast-3g or off")
		}
		conds = append(conds, &c)
	}
	return call(network.EmulateNetworkConditionsByRule, network.EmulateNetworkConditionsByRuleParams{MatchedNetworkConditions: conds}), nil
}

func cpuStep(v string) (func(context.Context, cdp.Session) error, error) {
	rate := 1.0
	if v != "off" {
		r, err := strconv.ParseFloat(v, 64)
		if err != nil || r < 1 || r > 20 {
			return nil, exit.New(exit.Validation, "--cpu must be a slowdown factor from 1 to 20, or off")
		}
		rate = r
	}
	return call(emulation.SetCPUThrottlingRate, emulation.SetCPUThrottlingRateParams{Rate: rate}), nil
}

func geoStep(v string) (func(context.Context, cdp.Session) error, error) {
	if v == "off" {
		return call(emulation.ClearGeolocationOverride, cdp.Empty{}), nil
	}
	la, lo, ok := strings.Cut(v, ",")
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(la), 64)
	lon, err2 := strconv.ParseFloat(strings.TrimSpace(lo), 64)
	if !ok || err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return nil, exit.New(exit.Validation, "--geo must be LATITUDE,LONGITUDE, such as 12.97,77.59, or off")
	}
	acc := 50.0
	return call(emulation.SetGeolocationOverride, emulation.SetGeolocationOverrideParams{Latitude: &lat, Longitude: &lon, Accuracy: &acc}), nil
}

func colorStep(v string) (func(context.Context, cdp.Session) error, error) {
	p := emulation.SetEmulatedMediaParams{Features: []*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: ""}}}
	switch v {
	case "dark", "light":
		p.Features[0].Value = v
	case "off":
	default:
		return nil, exit.New(exit.Validation, "--color must be dark, light or off")
	}
	return call(emulation.SetEmulatedMedia, p), nil
}

// performance reports how the page loaded and how busy it is: Core Web
// Vitals with Google's ratings, the slowest resources, long animation
// frames and the scripts behind them, and Chrome's runtime counters. With
// reload, it first reloads the page so the numbers cover a fresh load.
func (s *server) performance(id string, reload bool, timeout time.Duration) (json.RawMessage, error) {
	if reload {
		if _, err := s.goTo(id, "reload", timeout); err != nil {
			return nil, err
		}
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
	var report map[string]any
	if err := evalJSON(ctx, sess, perfJS, &report); err != nil {
		return nil, err
	}
	if m, err := cdp.Call(ctx, sess.t, performance.GetMetrics, cdp.Empty{}); err == nil {
		report["runtime"] = runtimeMetrics(m.Metrics)
	}
	return json.Marshal(report)
}

// runtimeMetrics keeps the counters that help explain a slow page.
func runtimeMetrics(ms []*performance.Metric) map[string]float64 {
	keep := map[string]string{
		"JSHeapUsedSize": "js_heap_used_mb", "Nodes": "dom_nodes", "JSEventListeners": "event_listeners",
		"LayoutCount": "layouts", "RecalcStyleCount": "style_recalcs", "ScriptDuration": "script_ms",
		"LayoutDuration": "layout_ms", "RecalcStyleDuration": "style_ms", "TaskDuration": "task_ms",
	}
	out := map[string]float64{}
	for _, m := range ms {
		name, ok := keep[m.Name]
		if !ok {
			continue
		}
		v := m.Value
		switch {
		case strings.HasSuffix(name, "_mb"):
			v /= 1 << 20
		case strings.HasSuffix(name, "_ms"):
			v *= 1000
		}
		out[name] = float64(int64(v*10)) / 10
	}
	return out
}

// perfJS reads the page's own performance timeline. Buffered observers
// return entries recorded before bx asked.
const perfJS = `(async () => {
	const entries = type => new Promise(resolve => {
		try {
			new PerformanceObserver((list, obs) => { obs.disconnect(); resolve(list.getEntries()); })
				.observe({type, buffered: true});
			setTimeout(() => resolve([]), 300);
		} catch { resolve([]); }
	});
	const rate = (v, good, poor) => v == null ? null : v <= good ? "good" : v <= poor ? "needs improvement" : "poor";
	const ms = v => v == null ? null : Math.round(v);
	const describe = el => !el ? null : el.tagName.toLowerCase() + (el.id ? "#" + el.id : "") +
		(el.className && typeof el.className === "string" ? "." + el.className.trim().split(/\s+/).join(".") : "");

	const nav = performance.getEntriesByType("navigation")[0];
	const fcp = performance.getEntriesByName("first-contentful-paint")[0];
	const lcps = await entries("largest-contentful-paint");
	const lcp = lcps[lcps.length - 1];
	let cls = 0, win = 0, first = 0, prev = 0;
	for (const s of await entries("layout-shift")) {
		if (s.hadRecentInput) continue;
		if (win && s.startTime - prev < 1000 && s.startTime - first < 5000) { win += s.value; }
		else { win = s.value; first = s.startTime; }
		prev = s.startTime; cls = Math.max(cls, win);
	}
	const frames = (await entries("long-animation-frame")).sort((a, b) => b.duration - a.duration);
	const resources = performance.getEntriesByType("resource");
	return {
		url: location.href,
		vitals: {
			lcp_ms: ms(lcp && lcp.startTime), lcp_rating: rate(lcp && lcp.startTime, 2500, 4000),
			lcp_element: describe(lcp && lcp.element),
			cls: Math.round(cls * 1000) / 1000, cls_rating: rate(cls, 0.1, 0.25),
			fcp_ms: ms(fcp && fcp.startTime), fcp_rating: rate(fcp && fcp.startTime, 1800, 3000),
			ttfb_ms: ms(nav && nav.responseStart), ttfb_rating: rate(nav && nav.responseStart, 800, 1800),
		},
		load: nav ? {dom_content_loaded_ms: ms(nav.domContentLoadedEventEnd), load_ms: ms(nav.loadEventEnd),
			transfer_kb: Math.round(nav.transferSize / 1024)} : null,
		blocking: {
			long_frames: frames.length,
			total_blocking_ms: ms(frames.reduce((t, f) => t + Math.max(0, f.blockingDuration || 0), 0)),
			worst: frames.slice(0, 5).map(f => ({at_ms: ms(f.startTime), duration_ms: ms(f.duration),
				scripts: (f.scripts || []).slice(0, 3).map(s => ({source: s.sourceURL, function: s.sourceFunctionName, invoker: s.invoker, ms: ms(s.duration)}))})),
		},
		resources: {
			count: resources.length,
			transfer_kb: Math.round(resources.reduce((t, r) => t + (r.transferSize || 0), 0) / 1024),
			slowest: [...resources].sort((a, b) => b.duration - a.duration).slice(0, 5)
				.map(r => ({url: r.name, type: r.initiatorType, ms: ms(r.duration), kb: Math.round((r.transferSize || 0) / 1024)})),
		},
		note: "INP needs real interactions; time them with network and console --since around an action.",
	};
})()`
