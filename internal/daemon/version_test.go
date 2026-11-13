package daemon

import "testing"

func TestOlderVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.3.0", "v0.4.0", true},
		{"v0.4.0", "v0.4.0", false},
		{"v0.4.1", "v0.4.0", false},
		{"v0.9.0", "v0.10.0", true},
		{"", "v0.4.0", true},     // no version: an old daemon
		{"dev", "v0.4.0", false}, // a development build is left alone
		{"v0.3.0", "dev", false}, // and never replaces anything
	} {
		if got := olderVersion(c.a, c.b); got != c.want {
			t.Errorf("olderVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
