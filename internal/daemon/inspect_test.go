package daemon

import (
	"testing"

	"github.com/chromedp/cdproto/performance"
)

func TestParseViewport(t *testing.T) {
	cases := []struct {
		in     string
		w, h   int
		scale  float64
		mobile bool
		ok     bool
	}{
		{"390x844", 390, 844, 1, false, true},
		{"390x844@3", 390, 844, 3, false, true},
		{"390x844@3,mobile", 390, 844, 3, true, true},
		{"390x844,mobile", 390, 844, 1, true, true},
		{"390", 0, 0, 0, false, false},
		{"0x844", 0, 0, 0, false, false},
		{"390x844@0", 0, 0, 0, false, false},
		{"axb", 0, 0, 0, false, false},
	}
	for _, c := range cases {
		w, h, scale, mobile, err := parseViewport(c.in)
		if (err == nil) != c.ok || (c.ok && (w != c.w || h != c.h || scale != c.scale || mobile != c.mobile)) {
			t.Errorf("%q: got %d %d %v %v %v", c.in, w, h, scale, mobile, err)
		}
	}
}

func TestEmulationSteps(t *testing.T) {
	steps, err := emulationSteps(EmulateOptions{Network: "slow-3g", CPU: "4", Geo: "12.97,77.59", Color: "dark"})
	if err != nil || len(steps) != 4 {
		t.Fatalf("got %d steps, %v", len(steps), err)
	}
	for _, bad := range []EmulateOptions{{Network: "5g"}, {CPU: "0.5"}, {CPU: "30"}, {Geo: "91,0"}, {Geo: "12"}, {Color: "blue"}} {
		if _, err := emulationSteps(bad); err == nil {
			t.Errorf("%+v: want an error", bad)
		}
	}
	off, err := emulationSteps(EmulateOptions{Viewport: "off", Network: "off", CPU: "off", Geo: "off", Color: "off"})
	if err != nil || len(off) != 5 {
		t.Errorf("off: %d steps, %v", len(off), err)
	}
	if steps, _ := emulationSteps(EmulateOptions{}); len(steps) != 0 {
		t.Errorf("empty options made %d steps", len(steps))
	}
}

func TestRuntimeMetrics(t *testing.T) {
	got := runtimeMetrics([]*performance.Metric{
		{Name: "JSHeapUsedSize", Value: 3 << 20}, {Name: "ScriptDuration", Value: 0.4755},
		{Name: "Nodes", Value: 7085}, {Name: "Timestamp", Value: 99},
	})
	if got["js_heap_used_mb"] != 3 || got["script_ms"] != 475.5 || got["dom_nodes"] != 7085 || len(got) != 3 {
		t.Errorf("got %v", got)
	}
}
