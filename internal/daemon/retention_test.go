package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeLog writes a session log of n bytes last changed at mod.
func writeLog(t *testing.T, dir, name string, n int, mod time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	line := `{"t":"2026-01-01T00:00:00Z","session":"` + strings.TrimSuffix(name, ".jsonl") + `","op":"click","ms":1}` + "\n"
	if err := os.WriteFile(p, []byte(strings.Repeat(line, n/len(line)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTidyGzipsThenDeletesOldest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, _ := sessionsDir()
	os.MkdirAll(dir, 0o700)
	now := time.Now()
	old := writeLog(t, dir, "bx-old.jsonl", 20000, now.Add(-72*time.Hour))
	mid := writeLog(t, dir, "bx-mid.jsonl", 20000, now.Add(-48*time.Hour))
	live := writeLog(t, dir, "bx-live.jsonl", 20000, now.Add(-time.Minute))
	ancient := writeLog(t, dir, "bx-ancient.jsonl", 100, now.AddDate(0, 0, -200))

	// Plain files over 25 KB: the oldest two idle ones are gzipped, the
	// live one is left alone. The ancient one is past 180 days.
	tidy(now, 180, 25000, 1<<30)
	for _, p := range []string{old, mid, ancient} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s should be gone", filepath.Base(p))
		}
	}
	if _, err := os.Stat(live); err != nil {
		t.Error("the live session was archived")
	}
	if _, err := os.Stat(old + ".gz"); err != nil {
		t.Error("the oldest session was not gzipped")
	}
	list, _ := ListSessions()
	if len(list) != 3 {
		t.Errorf("history should read gzipped logs too, got %d sessions", len(list))
	}

	// Gzipped files over a tiny cap: the oldest is deleted first.
	tidy(now, 180, 1<<30, 1)
	if _, err := os.Stat(old + ".gz"); err == nil {
		t.Error("the oldest archive should be deleted over the gzip cap")
	}
}
