//go:build darwin

package daemon

import (
	"errors"
	"testing"

	"github.com/Facets-cloud/bx/internal/consent"
)

func TestConsentFor(t *testing.T) {
	origApprove := approve
	defer func() { approve = origApprove }()

	dir := t.TempDir()
	store := consent.New(dir)

	// Override consentStore for testing
	consentOnce.Do(func() {}) // mark initialized
	consentStore = store

	calledApprove := 0
	var lastTask, lastWhat, lastAlso string
	approveResult := false
	var approveErr error

	approve = func(task, what, also string) (bool, error) {
		calledApprove++
		lastTask, lastWhat, lastAlso = task, what, also
		return approveResult, approveErr
	}

	// 1. Deny -> error, auto=false, not remembered
	approveResult = false
	approveErr = errors.New("the user did not allow this")
	auto, err := consentFor("test task", "set cookie", "edge", "Personal", "github.com")
	if err == nil {
		t.Fatal("expected error on deny, got nil")
	}
	if auto {
		t.Fatal("expected auto=false on deny")
	}
	if store.Remembered("edge", "Personal", "github.com") {
		t.Fatal("denied site was remembered")
	}
	if calledApprove != 1 {
		t.Fatalf("expected approve to be called once, got %d", calledApprove)
	}

	// 2. Allow (one time) -> auto=false, err=nil, not remembered
	approveResult = false
	approveErr = nil
	auto, err = consentFor("test task", "set cookie", "edge", "Personal", "github.com")
	if err != nil {
		t.Fatalf("unexpected error on allow: %v", err)
	}
	if auto {
		t.Fatal("expected auto=false on allow without remember")
	}
	if store.Remembered("edge", "Personal", "github.com") {
		t.Fatal("allow-only site was remembered")
	}
	if calledApprove != 2 {
		t.Fatalf("expected approve to be called twice, got %d", calledApprove)
	}

	// 3. Always allow -> auto=false, err=nil, remembered
	approveResult = true
	approveErr = nil
	auto, err = consentFor("test task", "set cookie", "edge", "Personal", "github.com")
	if err != nil {
		t.Fatalf("unexpected error on always allow: %v", err)
	}
	if auto {
		t.Fatal("expected auto=false on first always allow")
	}
	if !store.Remembered("edge", "Personal", "github.com") {
		t.Fatal("always allow site was not remembered")
	}
	if calledApprove != 3 {
		t.Fatalf("expected approve to be called 3 times, got %d", calledApprove)
	}

	// 4. Second call for remembered browser+profile+site -> auto=true, approve NOT called
	auto, err = consentFor("test task", "set cookie", "edge", "Personal", "github.com")
	if err != nil {
		t.Fatalf("unexpected error on second consentFor: %v", err)
	}
	if !auto {
		t.Fatal("expected auto=true on remembered grant")
	}
	if calledApprove != 3 {
		t.Fatalf("approve was called again for remembered site: called %d times", calledApprove)
	}

	// 5. Empty site -> always calls approve, never remembered even if remember=true returned
	approveResult = true
	approveErr = nil
	auto, err = consentFor("test task", "set cookie", "edge", "Personal", "")
	if err != nil {
		t.Fatalf("unexpected error on empty site: %v", err)
	}
	if auto {
		t.Fatal("expected auto=false on empty site")
	}
	if store.Remembered("edge", "Personal", "") {
		t.Fatal("empty site was remembered")
	}
	if calledApprove != 4 {
		t.Fatalf("approve was not called for empty site")
	}
	_ = lastTask
	_ = lastWhat
	_ = lastAlso
}
