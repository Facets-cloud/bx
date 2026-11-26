package consent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScopedToBrowserProfileSite(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Remember("edge", "Personal", "github.com"); err != nil {
		t.Fatal(err)
	}
	if !New(dir).Remembered("edge", "Personal", "github.com") {
		t.Fatal("grant did not survive a restart")
	}
	// Same site, different profile = not covered.
	if New(dir).Remembered("edge", "Throwaway", "github.com") {
		t.Fatal("grant leaked across profiles")
	}
	// Same site, different browser = not covered.
	if New(dir).Remembered("brave", "Personal", "github.com") {
		t.Fatal("grant leaked across browsers")
	}
}

func TestEmptyHostNeverRemembered(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Remember("edge", "Personal", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "consent.json")); !os.IsNotExist(err) {
		t.Fatal("empty host wrote a grant")
	}
	if s.Remembered("edge", "Personal", "") {
		t.Fatal("empty host reported remembered")
	}
}

func TestForgetBySite(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	_ = s.Remember("edge", "Personal", "a.com")
	_ = s.Remember("brave", "Work", "a.com")
	_ = s.Remember("edge", "Personal", "b.com")
	n, err := s.Forget("a.com")
	if err != nil || n != 2 {
		t.Fatalf("Forget a.com = %d, %v; want 2", n, err)
	}
	if New(dir).Remembered("edge", "Personal", "a.com") {
		t.Fatal("a.com still remembered")
	}
	if !New(dir).Remembered("edge", "Personal", "b.com") {
		t.Fatal("Forget a.com wrongly dropped b.com")
	}
}

func TestRememberReplaces(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	_ = s.Remember("edge", "Personal", "s.com")
	_ = s.Remember("edge", "Personal", "s.com")
	if n := len(New(dir).List()); n != 1 {
		t.Fatalf("re-grant made %d records, want 1", n)
	}
}
