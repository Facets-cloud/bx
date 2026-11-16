package consent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionGrantNotDurable(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	if s.Remembered("github.com", CapStorage) {
		t.Fatal("nothing granted yet, but Remembered returned true")
	}
	if err := s.Remember("github.com", CapStorage, ScopeSession); err != nil {
		t.Fatalf("Remember session: %v", err)
	}
	if !s.Remembered("github.com", CapStorage) {
		t.Fatal("session grant not remembered")
	}
	// A session grant must never touch disk.
	if _, err := os.Stat(filepath.Join(dir, "consent.json")); !os.IsNotExist(err) {
		t.Fatalf("session grant wrote consent.json; want no file (err=%v)", err)
	}
	// A fresh store (new daemon) must not see the old session grant.
	if New(dir).Remembered("github.com", CapStorage) {
		t.Fatal("session grant survived into a new store")
	}
}

func TestAlwaysGrantIsDurable(t *testing.T) {
	dir := t.TempDir()
	if err := New(dir).Remember("example.com", CapStorage, ScopeAlways); err != nil {
		t.Fatalf("Remember always: %v", err)
	}
	// A new store (daemon restart) must still see the always grant.
	if !New(dir).Remembered("example.com", CapStorage) {
		t.Fatal("always grant did not survive a restart")
	}
	// Scoping is per site: a different site is still unknown.
	if New(dir).Remembered("evil.com", CapStorage) {
		t.Fatal("grant for example.com leaked to evil.com")
	}
}

func TestForget(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	_ = s.Remember("a.com", CapStorage, ScopeAlways)
	_ = s.Remember("b.com", CapStorage, ScopeSession)

	if err := s.Forget("a.com", ""); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if s.Remembered("a.com", CapStorage) {
		t.Fatal("a.com still remembered after Forget")
	}
	if New(dir).Remembered("a.com", CapStorage) {
		t.Fatal("a.com still durable after Forget")
	}
	if !s.Remembered("b.com", CapStorage) {
		t.Fatal("Forget a.com wrongly dropped b.com")
	}
}

func TestRememberReplaces(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	_ = s.Remember("site.com", CapStorage, ScopeAlways)
	_ = s.Remember("site.com", CapStorage, ScopeAlways)

	n := 0
	for _, g := range New(dir).List() {
		if g.Site == "site.com" && g.Capability == CapStorage {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("re-granting the same site made %d durable records, want 1", n)
	}
}
