package daemon

import (
	"context"
	"iter"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"

	"github.com/Facets-cloud/bx/internal/exit"
)

// Mock is a made-up response for the requests a tab makes to URLs that
// match a pattern, to see how a page handles an error, an empty list or
// a slow API without changing the server. It lasts until it is removed,
// the tab closes or bx stops.
type Mock struct {
	URL         string `json:"url"` // pattern: * is any run of characters, ? one character
	Status      int64  `json:"status"`
	Body        string `json:"body,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	re          *regexp.Regexp
}

// Mocks lists the mocks on a tab.
type Mocks struct {
	Mocks []Mock `json:"mocks"`
}

// tabMocks is a tab's mocks and whether it already listens for the
// requests they catch.
type tabMocks struct {
	mu        sync.Mutex
	list      []Mock
	listening bool
}

// globRE turns a URL pattern into a regular expression for the whole URL.
func globRE(p string) *regexp.Regexp {
	q := regexp.QuoteMeta(p)
	q = strings.NewReplacer(`\*`, `.*`, `\?`, `.`).Replace(q)
	return regexp.MustCompile("^" + q + "$")
}

// find is the latest mock whose pattern matches url.
func (m *tabMocks) find(url string) (Mock, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.list) - 1; i >= 0; i-- {
		if m.list[i].re.MatchString(url) {
			return m.list[i], true
		}
	}
	return Mock{}, false
}

// put adds a mock, replacing one with the same pattern; remove drops the
// one with that pattern, or all with "".
func (m *tabMocks) put(k Mock) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.list = append(m.without(k.URL), k)
}

func (m *tabMocks) remove(url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if url == "" {
		m.list = nil
		return
	}
	m.list = m.without(url)
}

func (m *tabMocks) without(url string) []Mock {
	var out []Mock
	for _, k := range m.list {
		if k.URL != url {
			out = append(out, k)
		}
	}
	return out
}

func (m *tabMocks) snapshot() []Mock {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Mock{}, m.list...)
}

// mock adds a mock to the tab, or with remove, takes the one for url away
// (every one when url is empty). It returns the tab's mocks.
func (s *server) mock(id string, a map[string]string, remove bool) (Mocks, error) {
	sess, err := s.session(id)
	if err != nil {
		return Mocks{}, err
	}
	m := &sess.mocks
	if remove {
		m.remove(a["url"])
	} else {
		k, err := parseMock(a)
		if err != nil {
			return Mocks{}, err
		}
		m.put(k)
	}
	if err := sess.applyMocks(); err != nil {
		return Mocks{}, err
	}
	return Mocks{Mocks: m.snapshot()}, nil
}

func parseMock(a map[string]string) (Mock, error) {
	k := Mock{URL: a["url"], Body: a["body"], ContentType: a["content_type"], Status: 200}
	if k.URL == "" {
		return k, exit.New(exit.Validation, "mock needs a url pattern, such as */api/orders*")
	}
	if a["status"] != "" {
		n, err := strconv.ParseInt(a["status"], 10, 64)
		if err != nil || n < 100 || n > 599 {
			return k, exit.New(exit.Validation, "status must be an HTTP status from 100 to 599, not %q", a["status"])
		}
		k.Status = n
	}
	if k.ContentType == "" {
		k.ContentType = "application/json"
	}
	k.re = globRE(k.URL)
	return k, nil
}

// applyMocks tells the browser which requests to hold for bx: those that
// match a mock. With none left, it stops holding requests.
func (sess *tabSession) applyMocks() error {
	ctx := sess.ctx
	list := sess.mocks.snapshot()
	if len(list) == 0 {
		if _, err := cdp.Call(ctx, sess.t, fetch.Disable, cdp.Empty{}); err != nil {
			return upstream("stop mocking", err)
		}
		return nil
	}
	pats := make([]*fetch.RequestPattern, len(list))
	for i, k := range list {
		pats[i] = &fetch.RequestPattern{URLPattern: k.URL}
	}
	sess.mocks.mu.Lock()
	start := !sess.mocks.listening
	sess.mocks.listening = true
	sess.mocks.mu.Unlock()
	if start {
		// Subscribe before enabling, so no held request goes unanswered.
		go sess.answerMocked(cdp.Events(sess.ctx, sess.t, fetch.RequestPaused))
	}
	if _, err := cdp.Call(ctx, sess.t, fetch.Enable, fetch.EnableParams{Patterns: pats}); err != nil {
		return upstream("mock requests", err)
	}
	return nil
}

// answerMocked answers each held request with its mock, or lets it through
// when no mock matches it any more.
func (sess *tabSession) answerMocked(paused iter.Seq2[fetch.EventRequestPaused, error]) {
	for ev, err := range paused {
		if err != nil {
			return
		}
		go sess.answer(ev)
	}
}

func (sess *tabSession) answer(ev fetch.EventRequestPaused) {
	ctx, cancel := context.WithCancel(sess.ctx)
	defer cancel()
	k, ok := sess.mocks.find(ev.Request.URL)
	if !ok {
		_, _ = cdp.Call(ctx, sess.t, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: ev.RequestID})
		return
	}
	_, _ = cdp.Call(ctx, sess.t, fetch.FulfillRequest, fetch.FulfillRequestParams{RequestID: ev.RequestID, ResponseCode: k.Status,
		ResponseHeaders: []*fetch.HeaderEntry{{Name: "Content-Type", Value: k.ContentType}, {Name: "Access-Control-Allow-Origin", Value: "*"}},
		Body:            []byte(k.Body)})
}
