package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"github.com/Facets-cloud/bx/internal/browser"
	"github.com/Facets-cloud/bx/internal/exit"
)

// Tab is one browser tab as the CLI shows it. Profile is empty when bx has
// not yet learned which profile the tab's context belongs to.
type Tab struct {
	ID         string `json:"id"`
	Profile    string `json:"profile"`
	ProfileDir string `json:"profile_dir"`
	Title      string `json:"title"`
	URL        string `json:"url"`
}

func (s *server) tabInfo(t *target.Info, names map[string]string) Tab {
	s.mu.Lock()
	dir := s.profileOf[t.BrowserContextID]
	s.mu.Unlock()
	return Tab{ID: string(t.TargetID), Profile: names[dir], ProfileDir: dir, Title: t.Title, URL: t.URL}
}

// profileNames maps profile dir -> display name. A failure leaves names out.
func (s *server) profileNames() map[string]string {
	m := map[string]string{}
	ps, _ := s.b.Profiles()
	for _, p := range ps {
		m[p.Dir] = p.Name
	}
	return m
}

func (s *server) resolveProfile(ref string) (browser.Profile, error) {
	if ref == "" {
		return browser.Profile{}, exit.New(exit.Validation, "--profile is required")
	}
	ps, err := s.b.Profiles()
	if err != nil {
		return browser.Profile{}, err
	}
	return browser.FindProfile(ps, ref)
}

// contextFor returns the browser context of profile dir. When bx does not
// know it, or the profile has no tabs left (a reopened profile gets a new
// context), it opens a probe tab in the profile and reads the probe's
// context. The caller must hold launchMu and close the returned probe tab
// (if any) once done.
func (s *server) contextFor(dir string) (cdp.BrowserContextID, target.ID, error) {
	ps, err := s.pages()
	if err != nil {
		return "", "", err
	}
	s.mu.Lock()
	for _, p := range ps {
		if s.profileOf[p.BrowserContextID] == dir {
			s.mu.Unlock()
			return p.BrowserContextID, "", nil
		}
	}
	s.mu.Unlock()

	nonce := newNonce()
	if err := s.b.LaunchProbe(dir, nonce); err != nil {
		return "", "", err
	}
	probe, err := s.waitFor(func(t *target.Info) bool { return strings.Contains(t.URL, browser.ProbeMarker+nonce) })
	if err != nil {
		return "", "", err
	}
	s.learn(probe.BrowserContextID, dir)
	return probe.BrowserContextID, probe.TargetID, nil
}

// learn records that ctx belongs to dir, forgetting any older context of dir.
func (s *server) learn(ctx cdp.BrowserContextID, dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c, d := range s.profileOf {
		if d == dir {
			delete(s.profileOf, c)
		}
	}
	s.profileOf[ctx] = dir
}

func (s *server) closeTarget(id target.ID) error {
	_, err := chromedp.CallBrowser(s.ctx, target.CloseTarget, target.CloseTargetParams{TargetID: id})
	return err
}

func (s *server) tabs(profileRef string) ([]Tab, error) {
	var ctxID cdp.BrowserContextID
	if profileRef != "" {
		p, err := s.resolveProfile(profileRef)
		if err != nil {
			return nil, err
		}
		s.launchMu.Lock()
		defer s.launchMu.Unlock()
		var probe target.ID
		if ctxID, probe, err = s.contextFor(p.Dir); err != nil {
			return nil, err
		}
		if probe != "" {
			defer s.closeTarget(probe)
		}
	}
	ps, err := s.pages()
	if err != nil {
		return nil, err
	}
	names := s.profileNames()
	out := []Tab{}
	for _, p := range ps {
		if ctxID == "" || p.BrowserContextID == ctxID {
			out = append(out, s.tabInfo(p, names))
		}
	}
	return out, nil
}

func (s *server) open(profileRef, rawURL string, timeout time.Duration) (Tab, error) {
	if err := browser.CheckURL(rawURL); err != nil {
		return Tab{}, err
	}
	p, err := s.resolveProfile(profileRef)
	if err != nil {
		return Tab{}, err
	}
	s.launchMu.Lock()
	defer s.launchMu.Unlock()
	ctxID, probe, err := s.contextFor(p.Dir)
	if err != nil {
		return Tab{}, err
	}
	// Close the probe only after the new tab exists: closing a profile's
	// last tab closes its window, and the profile then gets a new context.
	if probe != "" {
		defer s.closeTarget(probe)
	}
	before, err := s.pages()
	if err != nil {
		return Tab{}, err
	}
	seen := map[target.ID]bool{}
	for _, t := range before {
		seen[t.TargetID] = true
	}
	if err := s.b.Launch(p.Dir, rawURL); err != nil {
		return Tab{}, err
	}
	t, err := s.waitFor(func(t *target.Info) bool {
		return !seen[t.TargetID] && t.TargetID != probe && t.BrowserContextID == ctxID
	})
	if err != nil {
		return Tab{}, err
	}
	title := ""
	if !strings.HasPrefix(rawURL, "about:") {
		title = s.waitLoaded(t.TargetID, timeout)
	}
	tab, err := s.show(string(t.TargetID))
	if tab.Title == "" {
		tab.Title = title // the target list can lag behind the page
	}
	return tab, err
}

// waitLoaded waits, best effort, for the tab's document to finish loading,
// and returns the document title. A new tab starts on about:blank, which
// counts as loaded, so the wait also requires that the tab has moved on.
func (s *server) waitLoaded(id target.ID, timeout time.Duration) string {
	sess, err := s.session(string(id))
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	for ctx.Err() == nil {
		title, err := chromedp.Run(ctx, chromedp.Evaluate[string](
			`location.href !== "about:blank" && document.readyState === "complete" ? document.title || " " : ""`))
		if err == nil && title != "" {
			return strings.TrimSpace(title)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return ""
}

func (s *server) show(id string) (Tab, error) {
	t, err := s.findTab(id)
	if err != nil {
		return Tab{}, err
	}
	return s.tabInfo(t, s.profileNames()), nil
}

// tabErr names an element that matched nothing in time as a user error,
// and anything else as a browser failure.
func tabErr(err error, what, sel string, timeout time.Duration) error {
	if err == nil {
		return nil
	}
	var e *exit.Error
	if errors.As(err, &e) {
		return e
	}
	if sel != "" && errors.Is(err, context.DeadlineExceeded) {
		return exit.New(exit.User, "no visible element matches %q (waited %s)", sel, timeout)
	}
	return upstream(what, err)
}

// Content is the result of `bx read`.
type Content struct {
	Selector string `json:"selector"`
	Format   string `json:"format"`
	Content  string `json:"content"`
}

func (s *server) read(id, el, format string, timeout time.Duration) (Content, error) {
	if el == "" {
		el = "body"
	}
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "html" {
		return Content{}, exit.New(exit.Validation, "--format must be text or html")
	}
	sess, err := s.lockSession(id)
	if err != nil {
		return Content{}, err
	}
	defer sess.mu.Unlock()
	sel, err := sess.selector(sess.ctx, el)
	if err != nil {
		return Content{}, err
	}
	a := chromedp.Text(chromedp.CSS(sel))
	if format == "html" {
		a = chromedp.OuterHTML(chromedp.CSS(sel))
	}
	v, err := run(sess, timeout, a)
	return Content{Selector: el, Format: format, Content: v}, tabErr(err, "read", el, timeout)
}

// lockSession returns the tab's session with its lock held.
func (s *server) lockSession(id string) (*tabSession, error) {
	sess, err := s.session(id)
	if err != nil {
		return nil, err
	}
	sess.mu.Lock()
	return sess, nil
}

// Image is the result of `bx screenshot`: PNG bytes, base64 in JSON.
type Image struct {
	PNG []byte `json:"png"`
}

func (s *server) screenshot(id, el string, full bool, timeout time.Duration) (Image, error) {
	sess, err := s.lockSession(id)
	if err != nil {
		return Image{}, err
	}
	defer sess.mu.Unlock()
	a := chromedp.CaptureScreenshot()
	switch {
	case el != "":
		sel, err := sess.selector(sess.ctx, el)
		if err != nil {
			return Image{}, err
		}
		a = chromedp.Screenshot(chromedp.CSS(sel))
	case full:
		a = chromedp.FullScreenshot(100)
	}
	png, err := run(sess, timeout, a)
	return Image{PNG: png}, tabErr(err, "screenshot", el, timeout)
}

func (s *server) closeTab(id string) (any, error) {
	t, err := s.findTab(id)
	if err != nil {
		return nil, err
	}
	s.dropSession(t.TargetID)
	if err := s.closeTarget(t.TargetID); err != nil {
		return nil, upstream("close tab", err)
	}
	return nil, nil
}

// cdp sends one raw DevTools call, to the browser or (with a tab) to a page.
func (s *server) cdp(tab, method, params string, timeout time.Duration) (json.RawMessage, error) {
	if method == "" {
		return nil, exit.New(exit.Validation, "a DevTools method such as Page.navigate is required")
	}
	if params == "" {
		params = "{}"
	}
	if !json.Valid([]byte(params)) {
		return nil, exit.New(exit.Validation, "params must be a JSON object")
	}
	raw := json.RawMessage(params)
	var res json.RawMessage
	if tab == "" {
		ctx, cancel := context.WithTimeout(s.ctx, timeout)
		defer cancel()
		if err := chromedp.FromContext(s.ctx).Browser.Call(ctx, method, raw, &res); err != nil {
			return nil, upstream(method, err)
		}
		return res, nil
	}
	sess, err := s.session(tab)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	if err := sess.t.Call(ctx, method, raw, &res); err != nil {
		return nil, upstream(method, err)
	}
	return res, nil
}
