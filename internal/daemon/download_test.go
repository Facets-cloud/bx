package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreeName(t *testing.T) {
	dir := t.TempDir()
	if got := freeName(dir, "Invoice-1.pdf"); got != filepath.Join(dir, "Invoice-1.pdf") {
		t.Errorf("free name: got %s", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "Invoice-1.pdf"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := freeName(dir, "Invoice-1.pdf"); got != filepath.Join(dir, "Invoice-1 (1).pdf") {
		t.Errorf("taken name: got %s", got)
	}
	// A site or agent names the file, never the folder.
	for _, n := range []string{"../../etc/passwd", "/tmp/x.pdf", "a/b.pdf"} {
		if got := filepath.Dir(freeName(dir, n)); got != dir {
			t.Errorf("%q escaped the folder: %s", n, got)
		}
	}
	if got := freeName(dir, ""); got != filepath.Join(dir, "download") {
		t.Errorf("empty name: got %s", got)
	}
}

func TestDownloadDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inv", "2026-11")
	got, err := downloadDir(dir)
	if err != nil || got != dir {
		t.Fatalf("downloadDir: %s, %v", got, err)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Errorf("folder not created: %v", err)
	}
}
