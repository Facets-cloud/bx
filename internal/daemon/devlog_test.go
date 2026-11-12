package daemon

import (
	"strings"
	"testing"

	"github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
)

func TestDevLogRecords(t *testing.T) {
	l := newDevLog()
	l.listen(&runtime.EventConsoleAPICalled{Type: "log", Args: []*runtime.RemoteObject{
		{Type: "string", Value: []byte(`"cart"`)},
		{Type: "object", Preview: &runtime.ObjectPreview{Properties: []*runtime.PropertyPreview{
			{Name: "items", Type: "number", Value: "2"}, {Name: "coupon", Type: "string", Value: "SAVE10"}}}},
	}})
	l.listen(&runtime.EventExceptionThrown{ExceptionDetails: &runtime.ExceptionDetails{
		Text: "Uncaught", URL: "http://x/app.js", LineNumber: 8, ColumnNumber: 58,
		Exception: &runtime.RemoteObject{Description: "TypeError: boom"}}})
	l.listen(&log.EventEntryAdded{Entry: &log.Entry{Level: "error", Source: "network", Text: "Failed to load resource", URL: "http://x/api"}})

	l.listen(&network.EventRequestWillBeSent{RequestID: "1", Type: "Fetch", Timestamp: 10,
		Request: &network.Request{Method: "POST", URL: "http://x/api"}})
	l.listen(&network.EventResponseReceived{RequestID: "1", Response: &network.Response{Status: 500, StatusText: "Internal Server Error"}})
	l.listen(&network.EventLoadingFinished{RequestID: "1", Timestamp: 10.25, EncodedDataLength: 60})
	l.listen(&network.EventRequestWillBeSent{RequestID: "2", Type: "Image", Timestamp: 11,
		Request: &network.Request{Method: "GET", URL: "http://x/ok.png"}})
	l.listen(&network.EventResponseReceived{RequestID: "2", Response: &network.Response{Status: 200}})
	l.listen(&network.EventRequestWillBeSent{RequestID: "3", Timestamp: 12, Request: &network.Request{Method: "GET", URL: "http://x:9/"}})
	l.listen(&network.EventLoadingFailed{RequestID: "3", Timestamp: 12.004, ErrorText: "net::ERR_UNSAFE_PORT"})

	if len(l.console) != 3 {
		t.Fatalf("want 3 console messages, got %+v", l.console)
	}
	if got := l.console[0].Text; got != `cart {items: 2, coupon: "SAVE10"}` {
		t.Errorf("console text: %q", got)
	}
	if ex := l.console[1]; ex.Level != "error" || ex.Text != "TypeError: boom" || ex.Where != "http://x/app.js:9:59" {
		t.Errorf("exception: %+v", ex)
	}
	if len(l.requests) != 3 {
		t.Fatalf("want 3 requests, got %d", len(l.requests))
	}
	r := l.requests[0]
	if r.Status != 500 || !r.Done || r.Ms != 250 || r.Bytes != 60 || !isErrorRequest(r) {
		t.Errorf("request 1: %+v", r)
	}
	if isErrorRequest(l.requests[1]) || !strings.HasPrefix(l.requests[2].Failed, "net::ERR_UNSAFE_PORT") || !isErrorRequest(l.requests[2]) {
		t.Errorf("requests 2 and 3: %+v %+v", l.requests[1], l.requests[2])
	}
}

func TestDevLogRedirectAndCap(t *testing.T) {
	l := newDevLog()
	l.listen(&network.EventRequestWillBeSent{RequestID: "1", Timestamp: 1, Request: &network.Request{Method: "GET", URL: "http://x/old"}})
	l.listen(&network.EventRequestWillBeSent{RequestID: "1", Timestamp: 2, Request: &network.Request{Method: "GET", URL: "http://x/new"},
		RedirectResponse: &network.Response{Status: 301}})
	if len(l.requests) != 2 || l.requests[0].Status != 301 || !l.requests[0].Done || l.byRaw["1"] != l.requests[1] {
		t.Fatalf("redirect: %+v", l.requests)
	}
	for i := 0; i < keepEntries+5; i++ {
		l.listen(&runtime.EventConsoleAPICalled{Type: "log"})
	}
	if len(l.console) != keepEntries {
		t.Errorf("console kept %d, want %d", len(l.console), keepEntries)
	}
	if got := lastN([]int{1, 2, 3}, 2); len(got) != 2 || got[0] != 2 {
		t.Errorf("lastN: %v", got)
	}
}
