package daemon

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/cdp"
)

// labels is a label function over a fixed map of refs, as labeller returns.
func labels(m map[string]string) func(string) string {
	return func(ref string) string {
		if l, ok := m[ref]; ok {
			return l
		}
		return ref
	}
}

// Recorded steps name elements by the label a ref resolved to, and never keep
// a typed value.
func TestRecordStepResolvesRefsAndDropsValues(t *testing.T) {
	label := labels(map[string]string{"e12": `button "Next"`, "e4": `textbox "Email"`})

	fill := recordStep(Step{Action: "fill", Element: "e4", Value: "hunter2", Input: "textbox-email"}, label)
	if fill.Element != `textbox "Email"` || fill.Value != "" || fill.Input != "" {
		t.Fatalf("fill: got %+v, want element label, no value, no input slot", fill)
	}
	click := recordStep(Step{Action: "click", Element: "e12"}, label)
	if click.Element != `button "Next"` {
		t.Fatalf("click: element %q, want button \"Next\"", click.Element)
	}
	typed := recordStep(Step{Action: "type", Element: "e4", Text: "secret", Submit: "Enter"}, label)
	if typed.Text != "" || typed.Submit != "Enter" || typed.Element != `textbox "Email"` {
		t.Fatalf("type: got %+v, want no text and the submit key kept", typed)
	}
	wait := recordStep(Step{Action: "wait", Text: "Welcome"}, label)
	if wait.Text != "Welcome" {
		t.Fatalf("wait: text %q, want it kept", wait.Text)
	}
	goTo := recordStep(Step{Action: "goto", URL: "https://example.test/a?token=abc"}, label)
	if strings.Contains(goTo.URL, "token=") {
		t.Fatalf("goto: URL %q keeps its query", goTo.URL)
	}
	if unnamed := recordStep(Step{Action: "click", Element: "e99"}, label); unnamed.Element != "e99" {
		t.Fatalf("unknown ref: element %q, want it left as given", unnamed.Element)
	}
}

// A single action's plan is built from the request, so a value that was typed
// never reaches a step.
func TestPlannedStepsHaveNoValues(t *testing.T) {
	label := labels(map[string]string{"e4": `textbox "Email"`})
	fill := planSteps(Request{Op: "fill", Args: map[string]string{"pairs": `[["e4","hunter2"]]`}}, label)
	if len(fill) != 1 || fill[0].Element != `textbox "Email"` || fill[0].Value != "" {
		t.Fatalf("fill: got %+v", fill)
	}
	typed := planSteps(Request{Op: "type", Args: map[string]string{"selector": "e4", "text": "secret"}}, label)
	if len(typed) != 1 || typed[0].Text != "" || typed[0].Element != `textbox "Email"` {
		t.Fatalf("type: got %+v", typed)
	}
	if got := planSteps(Request{Op: "snapshot"}, label); got != nil {
		t.Fatalf("a read-only op plans steps: %+v", got)
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

// The #k a log or pin saves resolves to the same element: placeLabel numbers
// the element from the list the resolver uses, and pickNth picks from it.
func TestDuplicateNameRoundTrip(t *testing.T) {
	nodes := []SnapNode{
		{Ref: "e1", Role: "textbox", Name: "Email"},
		{Ref: "e2", Role: "textbox", Name: "Email"},
		{Ref: "e3", Role: "button", Name: "Next"},
	}
	refs := refState{refs: map[string]cdp.BackendNodeID{"e1": 1, "e2": 2, "e3": 3}, in: map[string]docKey{}}
	keys := []nodeKey{{backend: 1}, {backend: 2}}
	at := refAts(nodes, refs)
	if !at["e1"].shared || !at["e2"].shared || at["e3"].shared {
		t.Fatalf("shared flags wrong: %+v", at)
	}
	for _, ref := range []string{"e1", "e2"} {
		label, err := placeLabel(at[ref], keys)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		m := namePattern.FindStringSubmatch(label)
		if m == nil || m[3] == "" {
			t.Fatalf("label %q does not parse with a #k", label)
		}
		nth, _ := strconv.Atoi(m[3])
		got, ok := pickNth(keys, nth)
		want := nodeKey{refs.in[ref], refs.refs[ref]}
		if !ok || got != want {
			t.Fatalf("%s: label %q resolves to %+v, want %+v", ref, label, got, want)
		}
	}
	if _, ok := pickNth(keys, 3); ok {
		t.Fatal("pickNth past the last element should fail")
	}
	if l, _ := placeLabel(at["e3"], nil); l != `button "Next"` {
		t.Fatalf("unshared name labelled %q", l)
	}
}

// A shared name whose element the resolver does not list has no place a
// replay can find: the label is an error, and the step is never saved as the
// first match.
func TestSharedNameWithoutPlaceIsUnreplayable(t *testing.T) {
	nodes := []SnapNode{
		{Ref: "e1", Role: "textbox", Name: "Email"},
		{Ref: "e2", Role: "textbox", Name: "Email"},
	}
	refs := refState{refs: map[string]cdp.BackendNodeID{"e1": 1, "e2": 2}, in: map[string]docKey{}}
	at := refAts(nodes, refs)
	if _, err := placeLabel(at["e2"], []nodeKey{{backend: 1}}); err == nil {
		t.Fatal("a shared element missing from the resolver's list must not get a label")
	}
}

// A step whose label is unreplayable is marked, so a pin never saves it as a
// plain name that would resolve to the first match.
func TestUnreplayableLabelIsMarked(t *testing.T) {
	label := labels(map[string]string{"e2": `textbox "Email"` + unreplayable})
	st := recordStep(Step{Action: "fill", Element: "e2", Value: "x"}, label)
	if !strings.HasSuffix(st.Element, unreplayable) {
		t.Fatalf("element %q is not marked unreplayable", st.Element)
	}
}

// A failed step in a report keeps its input slot, so the agent knows which
// value to fix; it keeps no typed value.
func TestFailedStepReportHasNoValue(t *testing.T) {
	label := labels(map[string]string{"e4": `textbox "Email"`})
	f := failureStep(Step{Action: "fill", Element: "e4", Value: "hunter2", Input: "textbox-email"}, label)
	if f.Value != "" || f.Input != "textbox-email" || f.Element != `textbox "Email"` {
		t.Fatalf("failure step %+v", f)
	}
}

// A wait the agent ran is recorded as an expect step: its text or element is
// kept, and the element is named the way a replay can find it again.
func TestLoggedWaitIsAnExpectStep(t *testing.T) {
	label := labels(map[string]string{"e7": `status "Saved"`})
	got := planSteps(Request{Op: "wait", Args: map[string]string{"text": "Saved", "gone": "false"}}, label)
	if len(got) != 1 || got[0].Action != "wait" || got[0].Text != "Saved" || got[0].Gone {
		t.Fatalf("wait by text: got %+v", got)
	}
	gone := planSteps(Request{Op: "wait", Args: map[string]string{"selector": "e7", "gone": "true"}}, label)
	if len(gone) != 1 || gone[0].Element != `status "Saved"` || !gone[0].Gone {
		t.Fatalf("wait gone by element: got %+v", gone)
	}
}
