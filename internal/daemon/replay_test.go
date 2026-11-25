package daemon

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/cdp"
)

// Recorded steps name elements by the label a ref resolved to, and never keep
// a typed value.
func TestRecordStepResolvesRefsAndDropsValues(t *testing.T) {
	names := map[string]string{"e12": `button "Next"`, "e4": `textbox "Email"`}

	fill := recordStep(Step{Action: "fill", Element: "e4", Value: "hunter2"}, names)
	if fill.Element != `textbox "Email"` || fill.Value != "" || fill.Input != "" {
		t.Fatalf("fill: got %+v, want element label and no value", fill)
	}
	click := recordStep(Step{Action: "click", Element: "e12"}, names)
	if click.Element != `button "Next"` {
		t.Fatalf("click: element %q, want button \"Next\"", click.Element)
	}
	typed := recordStep(Step{Action: "type", Element: "e4", Text: "secret", Submit: "Enter"}, names)
	if typed.Text != "" || typed.Submit != "Enter" || typed.Element != `textbox "Email"` {
		t.Fatalf("type: got %+v, want no text and the submit key kept", typed)
	}
	wait := recordStep(Step{Action: "wait", Text: "Welcome"}, names)
	if wait.Text != "Welcome" {
		t.Fatalf("wait: text %q, want it kept", wait.Text)
	}
	goTo := recordStep(Step{Action: "goto", URL: "https://example.test/a?token=abc"}, names)
	if strings.Contains(goTo.URL, "token=") {
		t.Fatalf("goto: URL %q keeps its query", goTo.URL)
	}
	if unnamed := recordStep(Step{Action: "click", Element: "e99"}, names); unnamed.Element != "e99" {
		t.Fatalf("unknown ref: element %q, want it left as given", unnamed.Element)
	}
}

// A single action's log step is built from the request, so a value that was
// typed never reaches an entry.
func TestLoggedStepsHaveNoValues(t *testing.T) {
	names := map[string]string{"e4": `textbox "Email"`}
	fill := loggedSteps(Request{Op: "fill", Args: map[string]string{"pairs": `[["e4","hunter2"]]`}, names: names}, nil)
	if len(fill) != 1 || fill[0].Element != `textbox "Email"` || fill[0].Value != "" {
		t.Fatalf("fill: got %+v", fill)
	}
	typed := loggedSteps(Request{Op: "type", Args: map[string]string{"selector": "e4", "text": "secret"}, names: names}, nil)
	if len(typed) != 1 || typed[0].Text != "" || typed[0].Element != `textbox "Email"` {
		t.Fatalf("type: got %+v", typed)
	}
	batch := loggedSteps(Request{Op: "batch"}, BatchResult{Recorded: []Step{{Action: "fill", Element: `textbox "Email"`}}})
	if len(batch) != 1 || batch[0].Value != "" {
		t.Fatalf("batch: got %+v", batch)
	}
}

// A saved pin keeps no typed value on disk; each fill or type step names its
// input, and repeats of an element get their own.
func TestSavePinScrubsValues(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := Pin{PinHeader: PinHeader{Name: "Onboard vendor"}, Entries: []Entry{{Op: "batch", Steps: []Step{
		{Action: "fill", Element: `textbox "Email"`, Value: "hunter2"},
		{Action: "type", Element: `textbox "Email"`, Text: "secret"},
		{Action: "click", Element: `button "Next"`},
	}}}}
	path, err := SavePin(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"hunter2", "secret"} {
		if strings.Contains(string(raw), v) {
			t.Fatalf("pin file keeps the typed value %q", v)
		}
	}
	loaded, err := LoadPin("Onboard vendor")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, in := range loaded.Inputs() {
		names = append(names, in.Name)
	}
	if strings.Join(names, ",") != "textbox-email,textbox-email-2" {
		t.Fatalf("inputs %v, want textbox-email and textbox-email-2", names)
	}
}

// bindValues fills each input and refuses before anything runs when a value
// is missing or unknown.
func TestBindValues(t *testing.T) {
	steps := []Step{
		{Action: "fill", Element: `textbox "Email"`, Input: "textbox-email"},
		{Action: "type", Element: `textbox "Email"`, Input: "textbox-email-2"},
		{Action: "click", Element: `button "Next"`},
	}
	bound, err := bindValues(steps, map[string]string{"textbox-email": "a@b.test", "textbox-email-2": "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if bound[0].Value != "a@b.test" || bound[1].Text != "pw" || bound[2].Value != "" {
		t.Fatalf("bound %+v", bound)
	}
	if steps[0].Value != "" {
		t.Fatal("bindValues changed its input")
	}
	if _, err := bindValues(steps, map[string]string{"textbox-email": "a@b.test"}); err == nil ||
		!strings.Contains(err.Error(), "textbox-email-2") {
		t.Fatalf("missing value: err %v, want it to name textbox-email-2", err)
	}
	if _, err := bindValues(steps, map[string]string{"textbox-email": "x", "textbox-email-2": "y", "typo": "z"}); err == nil ||
		!strings.Contains(err.Error(), "no input named typo") {
		t.Fatalf("unknown input: err %v", err)
	}
}

// The #k a log or pin saves resolves to the same element: labelRefs numbers
// the element, the resolver parses the label and picks from the same list.
func TestDuplicateNameRoundTrip(t *testing.T) {
	nodes := []SnapNode{
		{Ref: "e1", Role: "textbox", Name: "Email"},
		{Ref: "e2", Role: "textbox", Name: "Email"},
		{Ref: "e3", Role: "button", Name: "Next"},
	}
	refs := refState{refs: map[string]cdp.BackendNodeID{"e1": 1, "e2": 2, "e3": 3}, in: map[string]docKey{}}
	keysFor := func(role, name string) []nodeKey {
		if role == "textbox" && name == "Email" {
			return []nodeKey{{backend: 1}, {backend: 2}}
		}
		return nil
	}
	labels := labelRefs(nodes, refs, keysFor)
	if labels["e1"] != `textbox "Email" #1` || labels["e2"] != `textbox "Email" #2` || labels["e3"] != `button "Next"` {
		t.Fatalf("labels %v", labels)
	}
	for _, ref := range []string{"e1", "e2"} {
		m := namePattern.FindStringSubmatch(labels[ref])
		if m == nil || m[3] == "" {
			t.Fatalf("label %q does not parse with a #k", labels[ref])
		}
		nth, _ := strconv.Atoi(m[3])
		got, ok := pickNth(keysFor(m[1], m[2]), nth)
		want := nodeKey{refs.in[ref], refs.refs[ref]}
		if !ok || got != want {
			t.Fatalf("%s: label %q resolves to %+v, want %+v", ref, labels[ref], got, want)
		}
	}
	if _, ok := pickNth(keysFor("textbox", "Email"), 3); ok {
		t.Fatal("pickNth past the last element should fail")
	}
}

// The failure report carries the failed step with its typed value removed
// and the input slot it reads, so a report never echoes what was typed.
func TestFailedStepReportHasNoValue(t *testing.T) {
	names := map[string]string{"e4": `textbox "Email"`}
	st := Step{Action: "fill", Element: "e4", Value: "hunter2", Input: "textbox-email"}
	rec := failureStep(st, names)
	if rec.Value != "" || rec.Element != `textbox "Email"` || rec.Input != "textbox-email" {
		t.Fatalf("failed step report: got %+v", rec)
	}
}

// A wait the agent ran is recorded as an expect step: its text or element is
// kept, and the element is named the way a replay can find it again.
func TestLoggedWaitIsAnExpectStep(t *testing.T) {
	names := map[string]string{"e7": `status "Saved"`}
	got := loggedSteps(Request{Op: "wait", Args: map[string]string{"text": "Saved", "gone": "false"}, names: names}, nil)
	if len(got) != 1 || got[0].Action != "wait" || got[0].Text != "Saved" || got[0].Gone {
		t.Fatalf("wait by text: got %+v", got)
	}
	gone := loggedSteps(Request{Op: "wait", Args: map[string]string{"selector": "e7", "gone": "true"}, names: names}, nil)
	if len(gone) != 1 || gone[0].Element != `status "Saved"` || !gone[0].Gone {
		t.Fatalf("wait gone by element: got %+v", gone)
	}
}
