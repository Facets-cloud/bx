package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.9", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.1.10", "v0.1.9", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v0.2.0-rc1", "v0.1.0", true},
		{"v0.2.0", "dev", false},
		{"", "v0.1.0", false},
		{"garbage", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestMaybeRefreshSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := filepath.Join(home, ".claude", "skills", "bx")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(claude, "SKILL.md"), []byte("old skill"), 0o644)
	os.WriteFile(filepath.Join(claude, "VERSION"), []byte("v0.1.0\n"), 0o644)
	// Codex is present but has no bx skill: it must stay that way.
	os.MkdirAll(filepath.Join(home, ".codex"), 0o755)

	old := Version
	Version = "v0.2.0"
	defer func() { Version = old }()
	maybeRefreshSkill(os.Stderr)

	b, _ := os.ReadFile(filepath.Join(claude, "SKILL.md"))
	if string(b) != string(skillMD) {
		t.Error("the Claude skill was not refreshed")
	}
	stamp, _ := os.ReadFile(filepath.Join(claude, "VERSION"))
	if string(stamp) != "v0.2.0\n" {
		t.Errorf("stamp = %q", stamp)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "bx", "SKILL.md")); err == nil {
		t.Error("the skill was installed into Codex, where the user never installed it")
	}

	// A dev build never rewrites the skill.
	os.WriteFile(filepath.Join(claude, "SKILL.md"), []byte("hand edited"), 0o644)
	Version = "dev"
	maybeRefreshSkill(os.Stderr)
	if b, _ := os.ReadFile(filepath.Join(claude, "SKILL.md")); string(b) != "hand edited" {
		t.Error("a dev build rewrote the skill")
	}
}

func TestCachedLatestUsesFreshCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := cachePath()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	b, _ := json.Marshal(updateCache{CheckedAt: now.Add(-time.Hour), Latest: "v9.9.9"})
	os.WriteFile(path, b, 0o600)
	// A fresh cache answers without calling GitHub.
	if got := cachedLatest(now); got != "v9.9.9" {
		t.Fatalf("got %q", got)
	}
}
