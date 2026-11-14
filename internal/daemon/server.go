package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"

	"github.com/Facets-cloud/bx/internal/browser"
	"github.com/Facets-cloud/bx/internal/exit"
)

// server holds the one DevTools connection and what it has learned about
// which browser context belongs to which profile.
type server struct {
	b     browser.Browser
	ctx   context.Context // browser-level chromedp context
	since time.Time

	launchMu sync.Mutex // one launch at a time, so new tabs are attributable

	mu        sync.Mutex
	profileOf map[cdp.BrowserContextID]string // context ID -> profile dir
	sessions  map[target.ID]*tabSession       // tabs the daemon stays attached to
	rec       *recorder                       // the running recording, if any
	tasks     map[string]*task                // agent sessions, by id
	auditMu   sync.Mutex                      // one audit log write at a time
	lastURL   map[string]string               // each tab's URL as of its last logged request

	stopOnce sync.Once
	stop     chan struct{}
}

// Serve is the body of `bx __daemon`. It connects, reports the outcome on
// stdout as one JSON line, then serves the socket until the browser goes
// away or a stop request arrives.
func Serve(browserName string) int {
	b, err := browser.Lookup(browserName)
	if err != nil {
		return reportStart(err)
	}
	ctx, err := connect(b)
	if err != nil {
		return reportStart(err)
	}
	sock, _, _, err := paths(b.Name)
	if err != nil {
		return reportStart(err)
	}
	_ = os.Remove(sock) // stale; the starting client holds the lock
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return reportStart(exit.New(exit.Upstream, "listen on %s: %v", sock, err))
	}
	defer os.Remove(sock)
	defer ln.Close()
	_ = os.Chmod(sock, 0o600)

	s := &server{b: b, ctx: ctx, since: time.Now(), profileOf: map[cdp.BrowserContextID]string{},
		sessions: map[target.ID]*tabSession{},
		tasks:    map[string]*task{}, lastURL: map[string]string{}, stop: make(chan struct{})}
	s.forgetClosedTabs()
	go s.tidyLogs()
	reportStart(nil)
	go s.accept(ln)

	select {
	case <-chromedp.FromContext(ctx).Browser.LostConnection:
		log.Printf("%s closed the connection; exiting", b.Label)
	case <-s.stop:
		log.Printf("stop requested; exiting")
	}
	return 0
}

// reportStart writes the startup handshake line and closes stdout.
func reportStart(err error) int {
	r := Response{}
	if err != nil {
		r = errorResponse(err)
		log.Printf("start failed: %s", r.Error)
	}
	_ = json.NewEncoder(os.Stdout).Encode(r)
	_ = os.Stdout.Close()
	if err != nil {
		return r.Code
	}
	return 0
}

// connect opens the single WebSocket. The browser shows an Allow dialog and
// the first call blocks until the user answers it.
func connect(b browser.Browser) (context.Context, error) {
	ws, err := b.WSURL()
	if err != nil {
		return nil, err
	}
	// The dial waits for the user to answer the Allow dialog, so it gets
	// the full connectWait, not chromedp's 10s default.
	alloc, _ := remote.NewAllocator(context.Background(), ws, remote.NoModifyURL, remote.WithDialTimeout(connectWait))
	ctx, _ := chromedp.NewContext(alloc)
	errc := make(chan error, 1)
	go func() {
		_, err := chromedp.Targets(ctx)
		errc <- err
	}()
	select {
	case err := <-errc:
		if err != nil {
			return nil, connectError(b, err)
		}
		return ctx, nil
	case <-time.After(connectWait):
		return nil, exit.New(exit.Permission, "nobody clicked Allow in %s within %s", b.Label, connectWait)
	}
}

func connectError(b browser.Browser, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return exit.New(exit.Permission, "nobody clicked Allow in %s within %s", b.Label, connectWait)
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return exit.New(exit.Upstream, "%s is not listening on its DevTools port: is it running with remote debugging on (%s)?", b.Label, b.Inspect)
	}
	return exit.New(exit.Permission, "%s refused the DevTools connection (was Allow clicked?): %v", b.Label, err)
}

func (s *server) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *server) handle(conn net.Conn) {
	defer conn.Close()
	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}
	resp := Response{}
	began, title := time.Now(), s.taskTitle(req.Args["session"])
	data, err := s.dispatch(req)
	took := time.Since(began)
	if title == "" {
		title = s.taskTitle(req.Args["session"]) // set by this request (start)
	}
	if err == nil {
		resp.Data, err = json.Marshal(data)
	}
	if err != nil {
		resp = errorResponse(err)
	}
	_ = json.NewEncoder(conn).Encode(resp)
	_ = conn.Close() // the client has its answer; the log write doesn't hold it up
	s.audit(req, title, data, err, took)
}

func (s *server) dispatch(req Request) (any, error) {
	a := req.Args
	t := timeoutArg(a)
	switch req.Op {
	case "status":
		return s.status(), nil
	case "stop":
		s.stopOnce.Do(func() { close(s.stop) })
		return nil, nil
	case "tabs":
		return s.tabs(a["profile"])
	case "open":
		tab, err := s.open(a["profile"], a["url"], t)
		if sess, serr := s.session(tab.ID); err == nil && serr == nil {
			tk := s.taskFor(a["session"])
			tk.noteOpened(sess.id)
			tk.apply(sess, a)
			s.recordFollow(sess)
			ctx, cancel := context.WithTimeout(sess.ctx, 2*time.Second)
			sess.say(ctx, "Opened the page")
			cancel()
			sess.idle()
		}
		if err == nil && a["follow"] == "true" {
			_, _ = s.front(tab.ID)
		}
		return tab, err
	case "show":
		return s.show(a["tab"])
	case "start":
		return s.start(a)
	case "done":
		return s.finish(a["session"], a["summary"])
	case "record-start":
		return s.recordStart(a["dir"], a["blur"])
	case "record-stop":
		return s.recordStop()
	case "close":
		return s.closeTab(a["tab"])
	case "cdp":
		return s.cdp(a["tab"], a["method"], a["params"], t)
	}
	if a["tab"] != "" {
		// The mode comes with each request: one daemon serves clients
		// that may have set BX_MODE differently.
		if sess, err := s.session(a["tab"]); err == nil {
			s.taskFor(a["session"]).apply(sess, a)
			s.recordFollow(sess)
			sess.narrate(req.Op, a)
		}
	}
	if a["follow"] == "true" && a["tab"] != "" && req.Op != "front" {
		_, _ = s.front(a["tab"]) // best effort: never fail the op over it
	}
	res, err := s.dispatchTab(req.Op, a, t)
	if sess, serr := s.session(a["tab"]); serr == nil && req.Op != "dialog" {
		defer sess.idle() // after the changes below are read, so they don't see the tag change
	}
	if err != nil {
		return res, err
	}
	switch r := res.(type) {
	case ActResult:
		r.Changes = s.changes(a["tab"])
		return r, nil
	case BatchResult:
		r.Changes = s.changes(a["tab"])
		return r, nil
	}
	return res, nil
}

// dispatchTab handles the ops that read or act on one tab.
func (s *server) dispatchTab(op string, a map[string]string, t time.Duration) (any, error) {
	id := a["tab"]
	switch op {
	case "read":
		return s.read(id, a["selector"], a["format"], t)
	case "snapshot":
		return s.snapshot(id, t)
	case "screenshot":
		return s.screenshot(id, a["selector"], a["full"] == "true", t)
	case "eval":
		return s.eval(id, a["script"], t)
	case "wait":
		return s.wait(id, a["text"], a["selector"], a["gone"] == "true", t)
	case "click":
		return s.click(id, a["selector"], a["double"] == "true", t)
	case "hover":
		return s.hover(id, a["selector"], t)
	case "drag":
		return s.drag(id, a["from"], a["to"], t)
	case "fill":
		var pairs [][2]string
		if err := json.Unmarshal([]byte(a["pairs"]), &pairs); err != nil {
			return nil, exit.New(exit.Validation, "fill: bad element/value pairs")
		}
		return s.fill(id, pairs, t)
	case "type":
		return s.typeText(id, a["selector"], a["text"], a["submit"], t)
	case "batch":
		return s.batch(id, a["steps"], t)
	case "key":
		return s.key(id, a["keys"], t)
	case "upload":
		var files []string
		if err := json.Unmarshal([]byte(a["files"]), &files); err != nil {
			return nil, exit.New(exit.Validation, "upload: bad file list")
		}
		return s.upload(id, a["selector"], files, t)
	case "dialog":
		return s.dialog(id, a["action"] == "accept", a["text"])
	case "front":
		return s.front(id)
	case "goto":
		return s.goTo(id, a["url"], t)
	case "storage":
		return s.storage(id, t)
	case "emulate":
		return s.emulate(id, EmulateOptions{Viewport: a["viewport"], Network: a["network"], CPU: a["cpu"],
			Geo: a["geo"], Color: a["color"], Reset: a["reset"] == "true"}, t)
	case "performance":
		return s.performance(id, a["reload"] == "true", t)
	case "console":
		return s.consoleList(id, a["errors"] == "true", atoi(a["since"]), atoi(a["limit"]))
	case "network":
		if a["request"] != "" {
			return s.networkRequest(id, a["request"], t)
		}
		return s.networkList(id, a["errors"] == "true", atoi(a["since"]), atoi(a["limit"]))
	}
	return nil, exit.New(exit.Validation, "unknown daemon op %q", op)
}

// forgetClosedTabs drops a tab's session when the browser closes the tab.
func (s *server) forgetClosedTabs() {
	_ = chromedp.ListenBrowser(s.ctx, func(ev any) {
		switch e := ev.(type) {
		case *target.EventTargetDestroyed:
			s.dropSession(e.TargetID)
		case target.EventTargetDestroyed:
			s.dropSession(e.TargetID)
		}
	})
}

// timeoutArg reads the "timeout" arg; the CLI has already validated it.
func timeoutArg(a map[string]string) time.Duration {
	if d, err := time.ParseDuration(a["timeout"]); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

// Version is the bx version this process runs; app sets it at startup.
// A daemon keeps running the version it started with until it stops.
var Version = "dev"

// Status is what `bx status` shows.
type Status struct {
	Version       string    `json:"version"`
	Browser       string    `json:"browser"`
	PID           int       `json:"pid"`
	Since         time.Time `json:"since"`
	KnownProfiles []string  `json:"known_profiles"`
}

func (s *server) status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Version: Version, Browser: s.b.Name, PID: os.Getpid(), Since: s.since, KnownProfiles: []string{}}
	for _, dir := range s.profileOf {
		st.KnownProfiles = append(st.KnownProfiles, dir)
	}
	return st
}

func newNonce() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// upstream wraps a browser-side failure, naming timeouts plainly.
func upstream(what string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return exit.New(exit.Upstream, "%s: timed out", what)
	}
	var e *exit.Error
	if errors.As(err, &e) {
		return e
	}
	return exit.New(exit.Upstream, "%s: %v", what, err)
}

// pages lists the page targets (tabs), leaving out bx's own probe tabs.
func (s *server) pages() ([]*target.Info, error) {
	all, err := chromedp.Targets(s.ctx)
	if err != nil {
		return nil, upstream("list targets", err)
	}
	var out []*target.Info
	for _, t := range all {
		if t.Type == "page" && !strings.Contains(t.URL, browser.ProbeMarker) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *server) findTab(id string) (*target.Info, error) {
	if id == "" {
		return nil, exit.New(exit.Validation, "a tab ID is required")
	}
	ps, err := s.pages()
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if string(p.TargetID) == id {
			return p, nil
		}
	}
	return nil, exit.New(exit.User, "no tab with ID %q (see bx list tabs)", id)
}

// waitFor polls until a page target matches.
func (s *server) waitFor(match func(*target.Info) bool) (*target.Info, error) {
	for i := 0; i < 70; i++ {
		all, err := chromedp.Targets(s.ctx)
		if err != nil {
			return nil, upstream("list targets", err)
		}
		for _, t := range all {
			if t.Type == "page" && match(t) {
				return t, nil
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil, exit.New(exit.Upstream, "no matching tab appeared within 10s")
}

// narrate shows, in watch mode, what an op that has no element to point at
// is about to do. Actions that point at an element say so themselves.
func (sess *tabSession) narrate(op string, a map[string]string) {
	msg := map[string]string{
		"read": "Read the page", "eval": "Run a script on the page", "console": "Check the console",
		"network": "Check network requests", "storage": "Read cookies and storage",
		"performance": "Measure performance", "emulate": "Change how the page is shown",
	}[op]
	switch {
	case op == "read" && a["selector"] != "":
		msg = "Read " + a["selector"]
	case op == "wait" && a["text"] != "":
		msg = fmt.Sprintf("Wait for %q", a["text"])
	case op == "wait":
		msg = "Wait for " + a["selector"]
	}
	if msg == "" {
		return
	}
	ctx, cancel := context.WithTimeout(sess.ctx, 2*time.Second)
	defer cancel()
	sess.say(ctx, msg)
}
