package daemon

import (
	"errors"
	"testing"
)

// The outputs are what osascript printed for each answer in a live test.
func TestAllowed(t *testing.T) {
	cases := []struct {
		out  string
		err  error
		want bool
	}{
		{"button returned:Allow, gave up:false\n", nil, true},
		{"", errors.New("execution error: User cancelled. (-128)"), false}, // Deny
		{"button returned:, gave up:true\n", nil, false},                   // no answer in time
		{"", errors.New("osascript: not found"), false},
	}
	for _, c := range cases {
		if got := allowed(c.out, c.err); got != c.want {
			t.Errorf("allowed(%q, %v) = %v, want %v", c.out, c.err, got, c.want)
		}
	}
}
