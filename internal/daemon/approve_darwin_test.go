package daemon

import (
	"errors"
	"testing"
)

// The outputs are what osascript printed for each answer in a live test.
func TestClicked(t *testing.T) {
	cases := []struct {
		out        string
		err        error
		wantButton string
		wantOK     bool
	}{
		{"button returned:Allow, gave up:false\n", nil, "Allow", true},
		{"button returned:Always allow, gave up:false\n", nil, "Always allow", true},
		{"", errors.New("execution error: User cancelled. (-128)"), "", false}, // Deny
		{"button returned:, gave up:true\n", nil, "", false},                   // no answer in time
		{"", errors.New("osascript: not found"), "", false},
	}
	for _, c := range cases {
		btn, ok := clicked(c.out, c.err)
		if ok != c.wantOK || (ok && btn != c.wantButton) {
			t.Errorf("clicked(%q, %v) = (%q, %v), want (%q, %v)", c.out, c.err, btn, ok, c.wantButton, c.wantOK)
		}
	}
}
