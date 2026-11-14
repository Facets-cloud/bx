package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/bx/internal/daemon"
)

func TestPinRoundTripAndRecipe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t0 := time.Now()
	es := []daemon.Entry{
		{T: t0, Op: "start", Target: "Onboard Lumen"},
		{T: t0.Add(time.Second), Op: "open", Target: "http://localhost:8770/", Profile: "Work", URL: "http://localhost:8770/"},
		{T: t0.Add(2 * time.Second), Op: "snapshot", Profile: "Work", URL: "http://localhost:8770/"},
		{T: t0.Add(3 * time.Second), Op: "fill", Target: `textbox "Company", textbox "GSTIN"`, Profile: "Work", URL: "http://localhost:8770/"},
		{T: t0.Add(4 * time.Second), Op: "click", Target: `button "Nope"`, Error: "no element", Profile: "Work", URL: "http://localhost:8770/"},
		{T: t0.Add(5 * time.Second), Op: "click", Target: `button "Next"`, Profile: "Work", URL: "http://localhost:8770/", To: "http://localhost:8770/#/2"},
		{T: t0.Add(6 * time.Second), Op: "click", Target: `button "Submit"`, Profile: "Work", URL: "http://localhost:8770/#/2"},
		{T: t0.Add(7 * time.Second), Op: "done", Target: "Submitted"},
	}
	tasks := splitTasks(es)
	task, err := pickTask(tasks, 0)
	if err != nil || task.title != "Onboard Lumen" {
		t.Fatalf("want the titled task, got %+v %v", task, err)
	}
	p := daemon.Pin{PinHeader: daemon.PinHeader{Name: "Onboard vendor", Title: task.title, Session: "bx-1", Pinned: t0}, Entries: task.entries}
	if _, err := daemon.SavePin(p); err != nil {
		t.Fatal(err)
	}
	got, err := daemon.LoadPin("onboard vendor")
	if err != nil || len(got.Entries) != 6 {
		t.Fatalf("load: %d entries, %v", len(got.Entries), err)
	}
	r := pinRecipe(got)
	for _, want := range []string{
		`pin "Onboard vendor" (onboard-vendor)`,
		"1. Work  localhost:8770/",
		`   fill textbox "Company", textbox "GSTIN"`,
		"2. Work  localhost:8770/#/2",
		`   click button "Submit"`,
		"(4 steps on 2 pages; 2 reads and failed attempts left out)",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("recipe lacks %q:\n%s", want, r)
		}
	}
	if strings.Contains(r, "Nope") || strings.Contains(r, "snapshot") {
		t.Errorf("recipe kept a failed or read-only step:\n%s", r)
	}
	if ps, _ := daemon.ListPins(); len(ps) != 1 {
		t.Errorf("want 1 pin, got %d", len(ps))
	}
	if err := daemon.Unpin("Onboard vendor"); err != nil {
		t.Fatal(err)
	}
	if ps, _ := daemon.ListPins(); len(ps) != 0 {
		t.Error("unpin left the pin")
	}
}
