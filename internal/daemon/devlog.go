package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"

	"github.com/Facets-cloud/bx/internal/exit"
)

// keepEntries bounds each tab's console and network records; the oldest go.
const keepEntries = 1000

// maxBody bounds a request or response body returned by network --request.
const maxBody = 64 << 10

// ConsoleMessage is one console call, uncaught exception or browser log
// entry (a failed load, a CSP or CORS error) in a tab.
type ConsoleMessage struct {
	ID     int      `json:"id"`
	Level  string   `json:"level"`  // debug, log, info, warning or error
	Source string   `json:"source"` // console, exception, network, security, ...
	Text   string   `json:"text"`
	Where  string   `json:"where,omitempty"` // url:line:column
	Stack  []string `json:"stack,omitempty"`
}

// NetRequest is one network request a tab made.
type NetRequest struct {
	ID         string `json:"id"` // such as n12, for network --request
	Method     string `json:"method"`
	URL        string `json:"url"`
	Type       string `json:"type"`
	Status     int64  `json:"status,omitempty"`
	StatusText string `json:"status_text,omitempty"`
	MimeType   string `json:"mime_type,omitempty"`
	Failed     string `json:"failed,omitempty"`
	Ms         int64  `json:"ms,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
	Done       bool   `json:"done"`

	raw         network.RequestID
	seq         int
	start       float64
	reqHeaders  network.Headers
	respHeaders network.Headers
	hasPost     bool
}

// NetRequestDetail is one request with its headers and bodies.
type NetRequestDetail struct {
	NetRequest
	RequestHeaders  network.Headers `json:"request_headers,omitempty"`
	ResponseHeaders network.Headers `json:"response_headers,omitempty"`
	RequestBody     string          `json:"request_body,omitempty"`
	ResponseBody    string          `json:"response_body,omitempty"`
	BodyNote        string          `json:"body_note,omitempty"`
}

// devLog records a tab's console and network activity from the moment bx
// attaches. Chrome replays console messages logged before that.
type devLog struct {
	mu       sync.Mutex
	console  []ConsoleMessage
	requests []*NetRequest
	byRaw    map[network.RequestID]*NetRequest
	seq      int
}

func newDevLog() *devLog { return &devLog{byRaw: map[network.RequestID]*NetRequest{}} }

// listen is a chromedp listener: it must return fast and run no commands.
func (l *devLog) listen(ev any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch e := ev.(type) {
	case *runtime.EventConsoleAPICalled:
		l.addConsole(consoleLevel(string(e.Type)), "console", argsText(e.Args), e.StackTrace)
	case *runtime.EventExceptionThrown:
		l.addException(e.ExceptionDetails)
	case *log.EventEntryAdded:
		l.addEntry(e.Entry)
	default:
		l.netEvent(ev)
	}
}

func (l *devLog) addEntry(en *log.Entry) {
	if en == nil {
		return
	}
	m := ConsoleMessage{Level: string(en.Level), Source: string(en.Source), Text: en.Text}
	if en.URL != "" {
		m.Where = fmt.Sprintf("%s:%d", en.URL, en.LineNumber+1)
	}
	l.push(m)
}

func (l *devLog) netEvent(ev any) {
	switch e := ev.(type) {
	case *network.EventRequestWillBeSent:
		l.requestSent(e)
	case *network.EventResponseReceived:
		if r := l.byRaw[e.RequestID]; r != nil && e.Response != nil {
			setResponse(r, e.Response)
		}
	case *network.EventLoadingFinished:
		if r := l.byRaw[e.RequestID]; r != nil {
			r.Done, r.Bytes, r.Ms = true, int64(e.EncodedDataLength), ms(r.start, float64(e.Timestamp))
		}
	case *network.EventLoadingFailed:
		if r := l.byRaw[e.RequestID]; r != nil {
			r.Done, r.Ms, r.Failed = true, ms(r.start, float64(e.Timestamp)), failText(e)
		}
	}
}

func failText(e *network.EventLoadingFailed) string {
	t := e.ErrorText
	if e.BlockedReason != "" {
		t += " (blocked: " + string(e.BlockedReason) + ")"
	}
	if e.CorsErrorStatus != nil {
		t += " (CORS: " + string(e.CorsErrorStatus.CorsError) + ")"
	}
	return t
}

func (l *devLog) push(m ConsoleMessage) {
	l.seq++
	m.ID = l.seq
	l.console = append(l.console, m)
	if len(l.console) > keepEntries {
		l.console = l.console[len(l.console)-keepEntries:]
	}
}

func (l *devLog) addConsole(level, source, text string, st *runtime.StackTrace) {
	m := ConsoleMessage{Level: level, Source: source, Text: text}
	if frames := stack(st); len(frames) > 0 {
		m.Where = frames[0]
		if level == "error" || level == "warning" {
			m.Stack = frames
		}
	}
	l.push(m)
}

func (l *devLog) addException(d *runtime.ExceptionDetails) {
	if d == nil {
		return
	}
	text := d.Text
	if d.Exception != nil && d.Exception.Description != "" {
		text = d.Exception.Description
	}
	m := ConsoleMessage{Level: "error", Source: "exception", Text: text, Stack: stack(d.StackTrace)}
	if d.URL != "" {
		m.Where = fmt.Sprintf("%s:%d:%d", d.URL, d.LineNumber+1, d.ColumnNumber+1)
	} else if len(m.Stack) > 0 {
		m.Where = m.Stack[0]
	}
	l.push(m)
}

func (l *devLog) requestSent(e *network.EventRequestWillBeSent) {
	if e.Request == nil {
		return
	}
	if prev := l.byRaw[e.RequestID]; prev != nil && e.RedirectResponse != nil {
		// The same request ID continues after a redirect: close out the hop.
		setResponse(prev, e.RedirectResponse)
		prev.Done, prev.Ms = true, ms(prev.start, float64(e.Timestamp))
	}
	l.seq++
	r := &NetRequest{
		ID: fmt.Sprintf("n%d", l.seq), Method: e.Request.Method, URL: e.Request.URL + e.Request.URLFragment,
		Type: string(e.Type), raw: e.RequestID, seq: l.seq, start: float64(e.Timestamp),
		reqHeaders: e.Request.Headers, hasPost: e.Request.HasPostData,
	}
	l.byRaw[e.RequestID] = r
	l.requests = append(l.requests, r)
	if len(l.requests) > keepEntries {
		drop := l.requests[0]
		if l.byRaw[drop.raw] == drop {
			delete(l.byRaw, drop.raw)
		}
		l.requests = l.requests[1:]
	}
}

func setResponse(r *NetRequest, resp *network.Response) {
	r.Status, r.StatusText, r.MimeType, r.respHeaders = resp.Status, resp.StatusText, resp.MimeType, resp.Headers
}

// ms converts two monotonic timestamps in seconds to milliseconds.
func ms(from, to float64) int64 {
	if to < from {
		return 0
	}
	return int64((to - from) * 1000)
}

func consoleLevel(t string) string {
	switch t {
	case "error", "assert":
		return "error"
	case "warning":
		return "warning"
	case "debug", "trace":
		return "debug"
	case "info":
		return "info"
	}
	return "log"
}

// argsText joins console arguments the way the DevTools console shows them.
func argsText(args []*runtime.RemoteObject) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, remoteText(a))
	}
	return strings.Join(parts, " ")
}

func remoteText(a *runtime.RemoteObject) string {
	if a == nil {
		return ""
	}
	if len(a.Value) > 0 {
		var s string
		if json.Unmarshal(a.Value, &s) == nil {
			return s
		}
		return string(a.Value)
	}
	if a.UnserializableValue != "" {
		return string(a.UnserializableValue)
	}
	if a.Preview != nil && a.Type == "object" && a.Subtype != "error" {
		return previewText(a.Preview)
	}
	if a.Description != "" {
		return a.Description
	}
	return string(a.Type)
}

// previewText renders an object preview as {a: 1, b: "x"} or [1, 2].
func previewText(p *runtime.ObjectPreview) string {
	parts := make([]string, 0, len(p.Properties))
	for _, pr := range p.Properties {
		v := pr.Value
		if pr.Type == "string" {
			v = strconv.Quote(v)
		}
		if p.Subtype == "array" {
			parts = append(parts, v)
		} else {
			parts = append(parts, pr.Name+": "+v)
		}
	}
	if p.Overflow {
		parts = append(parts, "…")
	}
	if p.Subtype == "array" {
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// stack lists up to five frames as "function url:line:column".
func stack(st *runtime.StackTrace) []string {
	if st == nil {
		return nil
	}
	var out []string
	for _, f := range st.CallFrames {
		if len(out) == 5 {
			break
		}
		name := f.FunctionName
		if name == "" {
			name = "(anonymous)"
		}
		out = append(out, fmt.Sprintf("%s %s:%d:%d", name, f.URL, f.LineNumber+1, f.ColumnNumber+1))
	}
	return out
}

// isErrorRequest is a request that failed or got a 4xx or 5xx answer.
func isErrorRequest(r *NetRequest) bool { return r.Failed != "" || r.Status >= 400 }

// consoleList returns the tab's messages after since, newest last.
func (s *server) consoleList(id string, errorsOnly bool, since, limit int) ([]ConsoleMessage, error) {
	sess, err := s.session(id)
	if err != nil {
		return nil, err
	}
	l := sess.log
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []ConsoleMessage{}
	for _, m := range l.console {
		if m.ID > since && (!errorsOnly || m.Level == "error" || m.Level == "warning") {
			out = append(out, m)
		}
	}
	return lastN(out, limit), nil
}

// networkList returns the tab's requests after since, newest last.
func (s *server) networkList(id string, errorsOnly bool, since, limit int) ([]NetRequest, error) {
	sess, err := s.session(id)
	if err != nil {
		return nil, err
	}
	l := sess.log
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []NetRequest{}
	for _, r := range l.requests {
		if r.seq > since && (!errorsOnly || isErrorRequest(r)) {
			out = append(out, *r)
		}
	}
	return lastN(out, limit), nil
}

// networkRequest returns one request with its headers and, while the
// browser still holds them, its request and response bodies.
func (s *server) networkRequest(id, reqID string, timeout time.Duration) (NetRequestDetail, error) {
	sess, err := s.session(id)
	if err != nil {
		return NetRequestDetail{}, err
	}
	sess.log.mu.Lock()
	var found *NetRequest
	for _, r := range sess.log.requests {
		if r.ID == reqID {
			c := *r
			found = &c
			break
		}
	}
	sess.log.mu.Unlock()
	if found == nil {
		return NetRequestDetail{}, exit.New(exit.User, "no request %s in this tab: run bx network %s to list them", reqID, id)
	}
	d := NetRequestDetail{NetRequest: *found, RequestHeaders: found.reqHeaders, ResponseHeaders: found.respHeaders}
	ctx, cancel := context.WithTimeout(sess.ctx, timeout)
	defer cancel()
	if found.hasPost {
		if res, err := cdp.Call(ctx, sess.t, network.GetRequestPostData, network.GetRequestPostDataParams{RequestID: found.raw}); err == nil {
			d.RequestBody = bodyText(res.PostData, res.Base64encoded)
		}
	}
	res, err := cdp.Call(ctx, sess.t, network.GetResponseBody, network.GetResponseBodyParams{RequestID: found.raw})
	switch {
	case err != nil:
		d.BodyNote = "the browser no longer holds the response body (it keeps them only for recent requests on the current page)"
	default:
		d.ResponseBody = bodyText(res.Body, res.Base64encoded)
	}
	return d, nil
}

// bodyText returns a body as text, cut to maxBody; binary bodies are
// described rather than returned.
func bodyText(b []byte, b64 bool) string {
	if b64 {
		raw, err := base64.StdEncoding.DecodeString(string(b))
		if err == nil {
			b = raw
		}
		if !looksText(b) {
			return fmt.Sprintf("(%d bytes of binary data)", len(b))
		}
	}
	if len(b) > maxBody {
		return string(b[:maxBody]) + fmt.Sprintf("\n… (cut at %d of %d bytes)", maxBody, len(b))
	}
	return string(b)
}

func looksText(b []byte) bool {
	n := min(len(b), 512)
	for _, c := range b[:n] {
		if c == 0 {
			return false
		}
	}
	return true
}

// lastN keeps the newest n entries; n of 0 means the default of 50.
func lastN[T any](xs []T, n int) []T {
	if n == 0 {
		n = 50
	}
	if len(xs) > n {
		return xs[len(xs)-n:]
	}
	return xs
}

// atoi reads a non-negative count argument; anything else counts as 0.
func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
