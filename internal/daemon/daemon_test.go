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

func TestFollows(t *testing.T) {
	s := &server{tasks: map[string]*task{}}
	if s.follows(map[string]string{}) {
		t.Error("fast mode with no BX_FOLLOW should not follow")
	}
	if !s.follows(map[string]string{"mode": "watch"}) {
		t.Error("a watch-mode request should follow")
	}
	s.taskFor("a").mode = "watch"
	if !s.follows(map[string]string{"session": "a"}) {
		t.Error("a task started in watch mode should follow")
	}
	s.taskFor("b").mode = "fast"
	if s.follows(map[string]string{"session": "b", "mode": "watch"}) {
		t.Error("a task that chose fast mode should not follow")
	}
}

func TestHowToWork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, k := range []string{"BX_MODE", "BX_PACE", "BX_TYPING", "BX_FOLLOW"} {
		t.Setenv(k, "")
	}
	want := func(mode, pace, typing string) {
		t.Helper()
		got := howToWork()
		if got["mode"] != mode || got["pace"] != pace || got["typing"] != typing {
			t.Errorf("howToWork() = %v, want mode %s pace %s typing %s", got, mode, pace, typing)
		}
	}
	want("fast", "full", "paste")
	c := LoadConfig()
	for k, v := range map[string]string{"mode": "watch", "pace": "human"} {
		if err := c.Set(k, v, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	want("watch", "human", "paste")
	t.Setenv("BX_MODE", "fast")
	t.Setenv("BX_TYPING", "keys")
	want("fast", "human", "keys")
	t.Setenv("BX_MODE", "")
	t.Setenv("BX_FOLLOW", "1") // the old name for watch
	c.Mode = ""
	if err := SaveConfig(c); err != nil {
		t.Fatal(err)
	}
	want("watch", "human", "keys")
	if err := c.Set("pace", "slow", false); err == nil {
		t.Error("pace slow should be refused")
	}
}
