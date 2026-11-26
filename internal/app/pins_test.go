package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/bx/internal/daemon"
)

// A batch that stopped after some steps is PARTIAL in the log and in the pin
// recipe, which says bx run replays the steps that ran; a batch that failed at
// its first step is FAILED.
func TestPartialBatchIsMarked(t *testing.T) {
	part := daemon.Entry{Op: "batch", Target: "2 steps: click button \"A\"; fill textbox \"B\"",
		Error: "no such element", Steps: []daemon.Step{{Action: "click", Element: `button "A"`}}}
	if got := actionLine(part); !strings.Contains(got, "PARTIAL: no such element") {
		t.Fatalf("partial line: %q", got)
	}
	none := part
	none.Steps = nil
	if got := actionLine(none); !strings.Contains(got, "FAILED: no such element") || strings.Contains(got, "PARTIAL") {
		t.Fatalf("failed line: %q", got)
	}
	p := daemon.Pin{PinHeader: daemon.PinHeader{Name: "p"}, Entries: []daemon.Entry{part}}
	if got := pinRecipe(p); !strings.Contains(got, "PARTIAL: the task stopped short") || !strings.Contains(got, "replays the steps of it that ran") {
		t.Fatalf("recipe should name the partial batch and that its steps that ran replay:\n%s", got)
	}
	if steps := p.Steps(); len(steps) != 1 || steps[0].Element != `button "A"` {
		t.Fatalf("a partial batch's steps that ran should replay: %+v", steps)
	}
}

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
	if _, _, err := daemon.SavePin(p); err != nil {
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
