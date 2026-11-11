package browser

import (
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
