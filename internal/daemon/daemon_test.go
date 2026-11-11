package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Facets-cloud/bx/internal/exit"
)

func TestHandshake(t *testing.T) {
	if err := handshake("{}\n", "log"); err != nil {
		t.Fatalf("ok handshake: %v", err)
	}
	err := handshake(`{"error":"Allow refused","code":4}`+"\n", "log")
	if exit.CodeOf(err) != exit.Permission || err.Error() != "Allow refused" {
		t.Fatalf("got %v", err)
	}
	// A daemon that died before writing anything.
	if exit.CodeOf(handshake("", "log")) != exit.Upstream {
		t.Fatal("empty handshake should be an upstream error")
	}
}

func TestErrorResponse(t *testing.T) {
	r := errorResponse(exit.New(exit.User, "no tab"))
	if r.Code != exit.User || r.Error != "no tab" {
		t.Fatalf("got %+v", r)
	}
	r = errorResponse(errors.New("boom"))
	if r.Code != exit.Upstream {
		t.Fatalf("plain errors are upstream: %+v", r)
	}
}

func TestTimeoutArg(t *testing.T) {
	if d := timeoutArg(map[string]string{"timeout": "5s"}); d != 5*time.Second {
		t.Fatalf("got %v", d)
	}
	if d := timeoutArg(nil); d != 30*time.Second {
		t.Fatalf("default: got %v", d)
	}
}

func TestDispatchValidation(t *testing.T) {
	s := &server{}
	for _, req := range []Request{
		{Op: "nope"},
		{Op: "open", Args: map[string]string{"url": "--flag", "profile": "x"}},
		{Op: "cdp", Args: map[string]string{"method": ""}},
	} {
		if _, err := s.dispatch(req); exit.CodeOf(err) != exit.Validation {
			t.Errorf("%+v: want validation error, got %v", req, err)
		}
	}
}

func TestTabErr(t *testing.T) {
	d := 5 * time.Second
	if tabErr(nil, "read", "h1", d) != nil {
		t.Fatal("nil stays nil")
	}
	err := tabErr(context.DeadlineExceeded, "read", "h1", d)
	if exit.CodeOf(err) != exit.User || err.Error() != `no visible element matches "h1" (waited 5s)` {
		t.Fatalf("selector timeout: %v", err)
	}
	if exit.CodeOf(tabErr(context.DeadlineExceeded, "Page.navigate", "", d)) != exit.Upstream {
		t.Fatal("timeout without a selector is upstream")
	}
	if exit.CodeOf(tabErr(exit.New(exit.User, "no tab"), "read", "h1", d)) != exit.User {
		t.Fatal("exit errors pass through")
	}
}
