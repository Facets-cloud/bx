package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/bx/internal/exit"
)

func TestParsePort(t *testing.T) {
	got, err := parsePort([]byte("9222\n/devtools/browser/abc-123\n"))
	if err != nil || got != "ws://127.0.0.1:9222/devtools/browser/abc-123" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", "9222", "9222\n/other"} {
		if _, err := parsePort([]byte(bad)); exit.CodeOf(err) != exit.Upstream {
			t.Errorf("parsePort(%q): want upstream error, got %v", bad, err)
		}
	}
}

const localState = `{"profile":{"info_cache":{
	"Profile 3":{"name":"Client"},
	"Default":{"name":"Personal"},
	"Profile 1":{"name":"Work"}}}}`

func TestProfiles(t *testing.T) {
	ps, err := parseProfiles([]byte(localState))
	if err != nil {
		t.Fatal(err)
	}
	want := []Profile{{"Default", "Personal"}, {"Profile 1", "Work"}, {"Profile 3", "Client"}}
	if len(ps) != len(want) {
		t.Fatalf("got %v", ps)
	}
	for i := range want {
		if ps[i] != want[i] {
			t.Errorf("ps[%d] = %v, want %v", i, ps[i], want[i])
		}
	}
	for _, ref := range []string{"Client", "Profile 3"} {
		if p, err := FindProfile(ps, ref); err != nil || p.Dir != "Profile 3" {
			t.Errorf("FindProfile(%q) = %v, %v", ref, p, err)
		}
	}
	// Exact match only: case and prefixes do not match.
	for _, ref := range []string{"client", "Cli", "profile 3"} {
		if _, err := FindProfile(ps, ref); exit.CodeOf(err) != exit.User {
			t.Errorf("FindProfile(%q): want user error, got %v", ref, err)
		}
	}
}

func TestCheckURL(t *testing.T) {
	for _, ok := range []string{"https://example.com", "http://localhost:3000/x", "about:blank", "file:///tmp/a.html"} {
		if err := CheckURL(ok); err != nil {
			t.Errorf("CheckURL(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"--renderer-cmd-prefix=sh", "example.com", "javascript:alert(1)", "chrome://settings"} {
		if exit.CodeOf(CheckURL(bad)) != exit.Validation {
			t.Errorf("CheckURL(%q): want validation error", bad)
		}
	}
}

func TestLookup(t *testing.T) {
	if b, err := Lookup("brave"); err != nil || b.Label != "Brave" {
		t.Fatalf("Lookup(brave) = %v, %v", b, err)
	}
	if _, err := Lookup("Brave"); exit.CodeOf(err) != exit.Validation {
		t.Errorf("Lookup is exact: want validation error")
	}
}

func TestTable(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range table {
		if seen[b.Name] || b.Label == "" || !strings.HasPrefix(b.App, "/Applications/") ||
			strings.HasPrefix(b.DataDir, "/") || !strings.HasSuffix(b.Inspect, "#remote-debugging") {
			t.Errorf("bad table entry %+v", b)
		}
		seen[b.Name] = true
	}
	if !seen[Default] {
		t.Fatal("Default is not in the table")
	}
}

func TestDetect(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if b := Detect(); b.Name != Default {
		t.Fatalf("no browser running: got %s, want %s", b.Name, Default)
	}
	v, _ := Lookup("vivaldi")
	if err := os.MkdirAll(v.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.Dir(), "DevToolsActivePort"), []byte("9222\n/devtools/browser/x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := Detect(); b.Name != "vivaldi" {
		t.Fatalf("got %s, want vivaldi", b.Name)
	}
}

func TestAgentLookup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	b, err := Lookup("brave-agent")
	if err != nil || !b.Agent || b.Name != "brave-agent" || !filepath.IsAbs(b.DataDir) {
		t.Fatalf("got %+v, %v", b, err)
	}
	if !strings.HasSuffix(b.Dir(), filepath.Join("bx", "brave-agent")) {
		t.Errorf("agent dir %s", b.Dir())
	}
	if _, err := Lookup("nope-agent"); exit.CodeOf(err) != exit.Validation {
		t.Errorf("unknown base browser must fail")
	}
}

func TestEnableDebugging(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	b, _ := Lookup("brave")
	os.MkdirAll(b.Dir(), 0o700)
	orig := `{"browser":{"last_redo_time":"13406999999999999"},"big":13406999999999999,"devtools":{"other":1}}`
	path := filepath.Join(b.Dir(), "Local State")
	os.WriteFile(path, []byte(orig), 0o600)

	if on, _ := b.DebuggingEnabled(); on {
		t.Fatal("starts off")
	}
	if err := b.SetDebugging(true); err != nil {
		t.Fatal(err)
	}
	if on, err := b.DebuggingEnabled(); !on || err != nil {
		t.Fatalf("after enable: %v, %v", on, err)
	}
	got, _ := os.ReadFile(path)
	// Other settings survive, including integers past float64 precision.
	for _, want := range []string{`"big":13406999999999999`, `"other":1`, `"last_redo_time":"13406999999999999"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("lost %s in %s", want, got)
		}
	}
	if err := b.SetDebugging(false); err != nil {
		t.Fatal(err)
	}
	if on, _ := b.DebuggingEnabled(); on {
		t.Error("SetDebugging(false) left it on")
	}

	// A lock left by a dead browser does not count as running.
	lock := filepath.Join(b.Dir(), "SingletonLock")
	os.Symlink("host-999999", lock)
	if b.Running() {
		t.Fatal("a stale lock must not count as running")
	}
	os.Remove(lock)
	// A running browser would overwrite the change: refuse. (Our own PID
	// stands in for a live browser.)
	os.Symlink(fmt.Sprintf("host-%d", os.Getpid()), lock)
	if err := b.SetDebugging(true); exit.CodeOf(err) != exit.User {
		t.Fatalf("running browser: got %v", err)
	}
}

func TestAgentHasDefaultProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	b, _ := Lookup("brave-agent")
	ps, err := b.Profiles()
	if err != nil || len(ps) != 1 || ps[0].Dir != "Default" {
		t.Fatalf("got %v, %v", ps, err)
	}
}
