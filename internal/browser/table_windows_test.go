//go:build windows

package browser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandEnv(t *testing.T) {
	t.Setenv("TEST_VAR", "hello")
	cases := []struct {
		in   string
		want string
	}{
		{`%TEST_VAR%\world`, `hello\world`},
		{`no_percent`, `no_percent`},
		{`bare%percent`, `bare%percent`},
		{`%UNKNOWN_VAR_BX%\path`, `\path`},
		{`%TEST_VAR%_%TEST_VAR%`, `hello_hello`},
	}
	for _, c := range cases {
		if got := expandEnv(c.in); got != c.want {
			t.Errorf("expandEnv(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFirstExisting(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "exists1.exe")
	f2 := filepath.Join(dir, "exists2.exe")
	if err := os.WriteFile(f2, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Falls back to first candidate if none exist.
	missing1 := filepath.Join(dir, "missing1.exe")
	missing2 := filepath.Join(dir, "missing2.exe")
	if got := firstExisting([]string{missing1, missing2}); got != missing1 {
		t.Errorf("got %q, want %q", got, missing1)
	}

	// Picks the first one that exists.
	if got := firstExisting([]string{missing1, f2, f1}); got != f2 {
		t.Errorf("got %q, want %q", got, f2)
	}
}

func TestWinTable(t *testing.T) {
	tb := winTable()
	if len(tb) != 5 {
		t.Fatalf("len(winTable) = %d, want 5", len(tb))
	}
	seen := map[string]bool{}
	for _, b := range tb {
		if seen[b.Name] {
			t.Errorf("duplicate name %q in winTable", b.Name)
		}
		seen[b.Name] = true
		if !filepath.IsAbs(b.DataDir) {
			t.Errorf("DataDir %q is not absolute for %s", b.DataDir, b.Name)
		}
		if b.App == "" {
			t.Errorf("App is empty for %s", b.Name)
		}
	}
	if !seen[Default] {
		t.Fatalf("Default %q not in winTable", Default)
	}
}
