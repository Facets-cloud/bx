package daemon

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/cdp"
)

// rec is a recorder whose refs resolve to the given labels.
func rec(full bool, m map[string]string) stepRecorder {
	return stepRecorder{full: full, label: func(ref string) string {
		if l, ok := m[ref]; ok {
			return l
		}
		return ref
	}}
}

// Recorded steps name elements by the label a ref resolved to, and never keep
// a typed value.
func TestRecordStepResolvesRefsAndDropsValues(t *testing.T) {
	r := rec(false, map[string]string{"e12": `button "Next"`, "e4": `textbox "Email"`})
	fill := r.recordStep(Step{Action: "fill", Element: "e4", Value: "hunter2", Input: "textbox-email"})
	if fill.Element != `textbox "Email"` || fill.Value != "" || fill.Input != "" {
		t.Fatalf("fill: got %+v, want element label, no value, no input slot", fill)
	}
	if click := r.recordStep(Step{Action: "click", Element: "e12"}); click.Element != `button "Next"` {
		t.Fatalf("click: element %q", click.Element)
	}
	if unnamed := r.recordStep(Step{Action: "click", Element: "e99"}); unnamed.Element != "e99" {
		t.Fatalf("unknown ref: element %q, want it left as given", unnamed.Element)
	}
}

// The allowlist: a value in a field the action does not own is never kept.
// Only a wait keeps Text, and no action keeps Value or upload Files.
func TestRecordStepAllowlistDropsWrongFields(t *testing.T) {
	r := rec(false, map[string]string{"e4": `textbox "Email"`})
	cases := map[string]Step{
		"key with text":    {Action: "key", Key: "Enter", Text: "secret"},
		"click with value": {Action: "click", Element: "e4", Value: "secret"},
		"hover with text":  {Action: "hover", Element: "e4", Text: "secret"},
		"type with value":  {Action: "type", Element: "e4", Text: "secret", Value: "secret"},
		"upload with text": {Action: "upload", Element: "e4", Files: []string{"/tmp/secret.pdf"}, Text: "secret"},
	}
	for name, st := range cases {
		got := r.recordStep(st)
		if got.Text != "" || got.Value != "" || len(got.Files) != 0 {
			t.Errorf("%s: kept a typed field: %+v", name, got)
		}
	}
	if w := r.recordStep(Step{Action: "wait", Text: "Welcome"}); w.Text != "Welcome" {
		t.Fatalf("wait lost its text: %+v", w)
	}
}

// A double-click is recorded as a double click, not a plain one.
func TestDoubleClickIsRecorded(t *testing.T) {
	r := rec(false, map[string]string{"e4": `button "Open"`})
	got := planSteps(Request{Op: "click", Args: map[string]string{"selector": "e4", "double": "true", "tab": "t"}}, r)
	if len(got) != 1 || !got[0].Double || got[0].Element != `button "Open"` {
		t.Fatalf("double click: got %+v", got)
	}
	single := planSteps(Request{Op: "click", Args: map[string]string{"selector": "e4", "tab": "t"}}, r)
	if len(single) != 1 || single[0].Double {
		t.Fatalf("single click: got %+v", single)
	}
}

// A URL with a query, fragment or user info is an input slot unless the user
// asked to log URLs whole; a clean URL is kept.
func TestGotoURLBecomesInputSlot(t *testing.T) {
	slot := rec(false, nil).recordStep(Step{Action: "goto", URL: "https://example.test/a?token=abc"})
	if slot.URL != "" || slot.Input != "url" {
		t.Fatalf("query URL not a slot: %+v", slot)
	}
	whole := rec(true, nil).recordStep(Step{Action: "goto", URL: "https://example.test/a?token=abc"})
	if whole.URL != "https://example.test/a?token=abc" || whole.Input != "" {
		t.Fatalf("log.values full should keep the URL: %+v", whole)
	}
	clean := rec(false, nil).recordStep(Step{Action: "goto", URL: "https://example.test/a"})
	if clean.URL != "https://example.test/a" || clean.Input != "" {
		t.Fatalf("clean URL should be kept: %+v", clean)
	}
	frag := rec(false, nil).recordStep(Step{Action: "goto", URL: "https://example.test/a#secret"})
	if frag.Input != "url" {
		t.Fatalf("fragment URL should be a slot: %+v", frag)
	}
}

// Upload records the file as an input slot: the path is never stored.
func TestUploadRecordsInputSlot(t *testing.T) {
	r := rec(false, map[string]string{"e5": `input "Upload"`})
	got := planSteps(Request{Op: "upload", Args: map[string]string{
		"selector": "e5", "files": `["/Users/me/secret.pdf"]`, "tab": "t"}}, r)
	if len(got) != 1 || got[0].Action != "upload" || got[0].Input != "file" || len(got[0].Files) != 0 {
		t.Fatalf("upload: got %+v", got)
	}
	if strings.Contains(strings.Join(got[0].Files, ""), "secret") {
		t.Fatal("upload stored the file path")
	}
}

// A single action's plan is built from the request, so a value that was typed
// never reaches a step.
func TestPlannedStepsHaveNoValues(t *testing.T) {
	r := rec(false, map[string]string{"e4": `textbox "Email"`})
	fill := planSteps(Request{Op: "fill", Args: map[string]string{"pairs": `[["e4","hunter2"]]`, "tab": "t"}}, r)
	if len(fill) != 1 || fill[0].Element != `textbox "Email"` || fill[0].Value != "" {
		t.Fatalf("fill: got %+v", fill)
	}
	typed := planSteps(Request{Op: "type", Args: map[string]string{"selector": "e4", "text": "secret", "tab": "t"}}, r)
	if len(typed) != 1 || typed[0].Text != "" || typed[0].Element != `textbox "Email"` {
		t.Fatalf("type: got %+v", typed)
	}
}

// A page-changing op with no step form is recorded as unreplayable, so a pin
// refuses it instead of skipping it. A read-only op or a non-tab op is not.
func TestUnreplayableOpsAreMarked(t *testing.T) {
	r := rec(false, nil)
	eval := planSteps(Request{Op: "eval", Args: map[string]string{"tab": "t", "script": "x"}}, r)
	if len(eval) != 1 || eval[0].Unreplayable != "eval" {
		t.Fatalf("eval should be unreplayable: %+v", eval)
	}
	if got := planSteps(Request{Op: "snapshot", Args: map[string]string{"tab": "t"}}, r); got != nil {
		t.Fatalf("a read-only op plans steps: %+v", got)
	}
	if got := planSteps(Request{Op: "eval", Args: map[string]string{}}, r); got != nil {
		t.Fatalf("an op with no tab plans steps: %+v", got)
	}
	multi := planSteps(Request{Op: "upload", Args: map[string]string{"tab": "t", "files": `["a","b"]`}}, r)
	if len(multi) != 1 || multi[0].Unreplayable == "" {
		t.Fatalf("a multi-file upload should be unreplayable: %+v", multi)
	}
}

// A pin with an unreplayable step is refused before anything runs, naming the
// step by index.
func TestRefuseUnreplayable(t *testing.T) {
	steps := []Step{{Action: "click", Element: `button "Next"`}, {Action: "eval", Unreplayable: "eval"}}
	err := refuseUnreplayable(steps)
	if err == nil || !strings.Contains(err.Error(), "step index 1 (eval)") {
		t.Fatalf("refuse: err %v", err)
	}
	if err := refuseUnreplayable([]Step{{Action: "click", Element: `button "Next"`}}); err != nil {
		t.Fatalf("a replayable pin was refused: %v", err)
	}
	if err := refuseUnreplayable([]Step{{Action: "click", Element: `button "Next"` + unreplayable}}); err == nil {
		t.Fatal("an element with no findable place should be refused")
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
// is missing or unknown; upload and goto inputs take their value too.
func TestBindValues(t *testing.T) {
	steps := []Step{
		{Action: "fill", Element: `textbox "Email"`, Input: "textbox-email"},
		{Action: "type", Element: `textbox "Email"`, Input: "textbox-email-2"},
		{Action: "upload", Element: "input", Input: "file"},
		{Action: "goto", Input: "url"},
		{Action: "click", Element: `button "Next"`},
	}
	bound, err := bindValues(steps, map[string]string{"textbox-email": "a@b.test", "textbox-email-2": "pw",
		"file": "/tmp/x.pdf", "url": "https://example.test/a?t=1"})
	if err != nil {
		t.Fatal(err)
	}
	if bound[0].Value != "a@b.test" || bound[1].Text != "pw" || bound[2].Files[0] != "/tmp/x.pdf" ||
		bound[3].URL != "https://example.test/a?t=1" || bound[4].Value != "" {
		t.Fatalf("bound %+v", bound)
	}
	if steps[0].Value != "" || steps[3].URL != "" {
		t.Fatal("bindValues changed its input")
	}
	if _, err := bindValues(steps, map[string]string{"textbox-email": "a@b.test"}); err == nil ||
		!strings.Contains(err.Error(), "textbox-email-2") {
		t.Fatalf("missing value: err %v, want it to name textbox-email-2", err)
	}
	if _, err := bindValues(steps, map[string]string{"textbox-email": "x", "textbox-email-2": "y", "file": "f", "url": "u", "typo": "z"}); err == nil ||
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

// A shared name the resolver lists no elements for (nil keys, or one that is
// not among them) has no place a replay can find: it is an error, never a
// guess at the first match.
func TestSharedNameWithoutPlaceIsUnreplayable(t *testing.T) {
	nodes := []SnapNode{
		{Ref: "e1", Role: "textbox", Name: "Email"},
		{Ref: "e2", Role: "textbox", Name: "Email"},
	}
	refs := refState{refs: map[string]cdp.BackendNodeID{"e1": 1, "e2": 2}, in: map[string]docKey{}}
	at := refAts(nodes, refs)
	if _, err := placeLabel(at["e2"], nil); err == nil {
		t.Fatal("nil keys: a shared element must not get a label")
	}
	if _, err := placeLabel(at["e2"], []nodeKey{{backend: 1}}); err == nil {
		t.Fatal("an element missing from the resolver's list must not get a label")
	}
}

// A step whose label is unreplayable carries the marker, so a pin never saves
// it as a plain name that would resolve to the first match.
func TestUnreplayableLabelIsMarked(t *testing.T) {
	r := rec(false, map[string]string{"e2": `textbox "Email"` + unreplayable})
	st := r.recordStep(Step{Action: "fill", Element: "e2", Value: "x"})
	if !strings.HasSuffix(st.Element, unreplayable) {
		t.Fatalf("element %q is not marked unreplayable", st.Element)
	}
}

// A failed step in a report keeps its input slot, so the agent knows which
// value to fix; it keeps no typed value.
func TestFailedStepReportHasNoValue(t *testing.T) {
	r := rec(false, map[string]string{"e4": `textbox "Email"`})
	f := r.failureStep(Step{Action: "fill", Element: "e4", Value: "hunter2", Input: "textbox-email"})
	if f.Value != "" || f.Input != "textbox-email" || f.Element != `textbox "Email"` {
		t.Fatalf("failure step %+v", f)
	}
}

// A wait the agent ran is recorded as an expect step: its text or element is
// kept, and the element is named the way a replay can find it again.
func TestLoggedWaitIsAnExpectStep(t *testing.T) {
	r := rec(false, map[string]string{"e7": `status "Saved"`})
	got := planSteps(Request{Op: "wait", Args: map[string]string{"text": "Saved", "gone": "false", "tab": "t"}}, r)
	if len(got) != 1 || got[0].Action != "wait" || got[0].Text != "Saved" || got[0].Gone {
		t.Fatalf("wait by text: got %+v", got)
	}
	gone := planSteps(Request{Op: "wait", Args: map[string]string{"selector": "e7", "gone": "true", "tab": "t"}}, r)
	if len(gone) != 1 || gone[0].Element != `status "Saved"` || !gone[0].Gone {
		t.Fatalf("wait gone by element: got %+v", gone)
	}
}

// Closing a tab is bookkeeping a replay does not repeat, so it plans nothing.
func TestCloseIsNotPlanned(t *testing.T) {
	if got := planSteps(Request{Op: "close", Args: map[string]string{"tab": "t"}}, rec(false, nil)); got != nil {
		t.Fatalf("close plans steps: %+v", got)
	}
}
