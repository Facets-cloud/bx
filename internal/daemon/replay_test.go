package daemon

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
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
	err := refuseUnreplayable(steps, 0)
	if err == nil || !strings.Contains(err.Error(), "step index 1 (eval)") {
		t.Fatalf("refuse: err %v", err)
	}
	if err := refuseUnreplayable([]Step{{Action: "click", Element: `button "Next"`}}, 0); err != nil {
		t.Fatalf("a replayable pin was refused: %v", err)
	}
	if err := refuseUnreplayable([]Step{{Action: "click", Element: `button "Next"` + unreplayable}}, 0); err == nil {
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
	path, _, err := SavePin(p)
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
	bound, err := bindValues(steps, 0, map[string]string{"textbox-email": "a@b.test", "textbox-email-2": "pw",
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
	if _, err := bindValues(steps, 0, map[string]string{"textbox-email": "a@b.test"}); err == nil ||
		!strings.Contains(err.Error(), "textbox-email-2") {
		t.Fatalf("missing value: err %v, want it to name textbox-email-2", err)
	}
	if _, err := bindValues(steps, 0, map[string]string{"textbox-email": "x", "textbox-email-2": "y", "file": "f", "url": "u", "typo": "z"}); err == nil ||
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

// A pin whose task acted on a second tab cannot replay: a run puts every step
// on the one tab it is given. Steps on the task's first acting tab still replay.
func TestPinAcrossTabsRefused(t *testing.T) {
	p := Pin{Entries: []Entry{
		{Tab: "A", Steps: []Step{{Action: "click", Element: `button "Next"`}}},
		{Tab: "B", Steps: []Step{{Action: "click", Element: `button "Save"`}}},
	}}
	steps := p.Steps()
	if len(steps) != 2 || steps[1].Unreplayable != otherTab || steps[0].Unreplayable != "" {
		t.Fatalf("steps %+v: want the second tab's step marked unreplayable", steps)
	}
	err := refuseUnreplayable(steps, 0)
	if err == nil || !strings.Contains(err.Error(), "step index 1 (acts on a second tab)") {
		t.Fatalf("refuse: err %v", err)
	}
	one := Pin{Entries: []Entry{{Tab: "A", Steps: []Step{{Action: "click", Element: `button "Next"`}}},
		{Tab: "A", Steps: []Step{{Action: "click", Element: `button "Save"`}}}}}
	if err := refuseUnreplayable(one.Steps(), 0); err != nil {
		t.Fatalf("a one-tab pin was refused: %v", err)
	}
}

// A shared name is labelled by its place among the page's elements with that
// name. A page query that never answers gives the unreplayable label within
// the wait, so labelling cannot hold a request.
func TestShareLabelDeadline(t *testing.T) {
	at := refAt{role: "textbox", name: "Email", shared: true}
	found := func(context.Context) []nodeKey { return []nodeKey{{}} }
	if got := shareLabel(context.Background(), time.Second, at, found); got != `textbox "Email" #1` {
		t.Fatalf("placed: got %q", got)
	}
	hang := func(ctx context.Context) []nodeKey { <-ctx.Done(); return nil }
	began := time.Now()
	got := shareLabel(context.Background(), 50*time.Millisecond, at, hang)
	if got != `textbox "Email"`+unreplayable {
		t.Fatalf("timed out: got %q, want the unreplayable label", got)
	}
	if d := time.Since(began); d > 2*time.Second {
		t.Fatalf("labelling took %s past its wait", d)
	}
}

// A typed character is an input slot, never kept; a named key is kept as
// given. The log names only how many characters were typed.
func TestTypedKeyIsInputSlot(t *testing.T) {
	r := rec(false, nil)
	typed := r.recordStep(Step{Action: "key", Key: "s"})
	if typed.Key != "" || typed.Input != "key" {
		t.Fatalf("typed key: got %+v, want an input slot with no key", typed)
	}
	if named := r.recordStep(Step{Action: "key", Key: "Enter"}); named.Key != "Enter" || named.Input != "" {
		t.Fatalf("named key: got %+v", named)
	}
	steps := []Step{typed}
	if _, err := bindValues(steps, 0, map[string]string{"key": "ab"}); err == nil {
		t.Fatal("a two-character value for a key input was accepted")
	}
	bound, err := bindValues(steps, 0, map[string]string{"key": "x"})
	if err != nil || bound[0].Key != "x" {
		t.Fatalf("bind: %+v %v", bound, err)
	}
	if got := describe("key", map[string]string{"keys": "s"}, false, nil); got != "key (1 chars)" {
		t.Fatalf("log of a typed key: %q", got)
	}
}

// A batch that stops at a failed step or an opened dialog returns no error,
// so its log entry takes the failure from the result: a FAILED entry, which
// pins leave out. A batch that finished has none.
func TestResultFailure(t *testing.T) {
	failed := 1
	if got := resultFailure(BatchResult{Done: 1, Failed: &failed, Error: "no such element"}); got != "no such element" {
		t.Fatalf("failed batch: %q", got)
	}
	if got := resultFailure(RunResult{BatchResult: BatchResult{Error: "stopped: the page opened a dialog"}}); got == "" {
		t.Fatal("a run stopped by a dialog logged as success")
	}
	if got := resultFailure(BatchResult{Done: 2}); got != "" {
		t.Fatalf("finished batch: %q", got)
	}
}

// --from N replays steps N on, not the ones before: an unreplayable step before
// N is not refused, one from N on is, and an index outside the pin is refused.
func TestResumeFrom(t *testing.T) {
	steps := []Step{{Action: "click", Element: `button "A"`}, {Action: "eval", Unreplayable: "eval"}, {Action: "click", Element: `button "B"`}}
	if err := checkFrom(steps, 2); err != nil {
		t.Fatalf("resume at 2: %v", err)
	}
	if got, err := bindValues(steps, 2, nil); err != nil || len(got) != 1 || got[0].Element != `button "B"` {
		t.Fatalf("bind from 2: %+v %v", got, err)
	}
	if err := checkFrom(steps, 0); err == nil || !strings.Contains(err.Error(), "step index 1 (eval)") {
		t.Fatalf("resume at 0 should refuse the unreplayable step 1: %v", err)
	}
	for _, from := range []int{-1, 3} {
		if err := checkFrom(steps, from); err == nil {
			t.Fatalf("from %d accepted for a 3-step pin", from)
		}
	}
}

// A resumed run ignores a value for an input used only before from, so an agent
// that resends every value can resume; a name the pin does not have at all is
// still refused, and an input from from on still needs its value.
func TestFromIgnoresEarlierInputs(t *testing.T) {
	steps := []Step{
		{Action: "fill", Element: `textbox "Email"`, Input: "textbox-email"},
		{Action: "click", Element: `button "Next"`},
		{Action: "fill", Element: `textbox "Name"`, Input: "textbox-name"},
	}
	got, err := bindValues(steps, 1, map[string]string{"textbox-email": "e@x.io", "textbox-name": "Eve"})
	if err != nil || len(got) != 2 || got[1].Value != "Eve" {
		t.Fatalf("from 1 with every value: %+v %v", got, err)
	}
	if _, err := bindValues(steps, 1, map[string]string{"textbox-name": "Eve", "typo": "x"}); err == nil || !strings.Contains(err.Error(), "no input named typo") {
		t.Fatalf("unknown name: %v", err)
	}
	if _, err := bindValues(steps, 1, map[string]string{"textbox-email": "e@x.io"}); err == nil || !strings.Contains(err.Error(), "textbox-name") {
		t.Fatalf("missing later value: %v", err)
	}
}

// Re-pinning after a label changed keeps the input's name, so a --set by the
// old name still binds; the rename is reported.
func TestRePinKeepsInputName(t *testing.T) {
	prev, _ := withInputs(Pin{Entries: []Entry{{Steps: []Step{{Action: "fill", Element: `textbox "Email"`, Value: "a"}}}}}, Pin{})
	p := Pin{Entries: []Entry{{Steps: []Step{
		{Action: "fill", Element: `textbox "Work email"`, Value: "b"},
		{Action: "click", Element: `button "Next"`},
	}}}}
	got, renames := withInputs(p, prev)
	steps := got.Steps()
	if steps[0].Input != "textbox-email" {
		t.Fatalf("input %q, want the old name textbox-email", steps[0].Input)
	}
	if len(renames) != 1 || renames[0] != "input textbox-work-email kept as textbox-email" {
		t.Fatalf("renames %q", renames)
	}
	bound, err := bindValues(steps, 0, map[string]string{"textbox-email": "new"})
	if err != nil || bound[0].Value != "new" {
		t.Fatalf("--set by the old name: %+v %v", bound, err)
	}
}

// A field removed and another added on re-pin keeps the old name by place, so
// the input is marked ByPlace and a run warns; a later re-pin that matches it
// by its exact label clears the mark. An elementless input (url) is never
// marked: it has no label to confirm.
func TestRePinByPlaceWarns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pin := func(labels ...string) []Step {
		steps := []Step{{Action: "goto", Input: "url"}}
		for _, l := range labels {
			steps = append(steps, Step{Action: "fill", Element: `textbox "` + l + `"`, Value: "v"})
		}
		if _, _, err := SavePin(Pin{PinHeader: PinHeader{Name: "form"}, Entries: []Entry{{Steps: steps}}}); err != nil {
			t.Fatal(err)
		}
		p, err := LoadPin("form")
		if err != nil {
			t.Fatal(err)
		}
		return unchecked(p.Steps()) // as runPin reads them
	}
	if w := byPlaceWarning(pin("Email", "Phone")); w != "" {
		t.Fatalf("a first pin warns: %q", w)
	}
	steps := pin("Email", "Company") // Phone removed, Company added
	if steps[0].ByPlace || steps[1].ByPlace || !steps[2].ByPlace || steps[2].Input != "textbox-phone" {
		t.Fatalf("marks %+v", steps)
	}
	want := `warning: textbox-phone was kept by position (now textbox "Company"); check the value or re-pin`
	if w := byPlaceWarning(steps); w != want {
		t.Fatalf("warning %q", w)
	}
	if w := byPlaceWarning(steps[2:]); w != want { // run --from 2 still warns
		t.Fatalf("from 2: %q", w)
	}
	steps = pin("Email", "Company") // saved again, Company matches by label
	if w := byPlaceWarning(steps); w != "" || steps[2].Input != "textbox-phone" {
		t.Fatalf("exact re-pin kept the mark: %q %+v", w, steps)
	}
	two := []Step{
		{Action: "fill", Element: `textbox "A"`, Input: "textbox-x", ByPlace: true},
		{Action: "fill", Element: `textbox "B"`, Input: "textbox-y", ByPlace: true},
	}
	if w := byPlaceWarning(two); w != `warning: textbox-x (now textbox "A"), textbox-y (now textbox "B") were kept by position; check the values or re-pin` {
		t.Fatalf("two: %q", w)
	}
}

// A field added before another on re-pin gets a fresh name, reported as new;
// the existing field keeps its name by its label, and no value shifts.
func TestRePinAddedFieldDoesNotShift(t *testing.T) {
	prev, notes := withInputs(Pin{Entries: []Entry{{Steps: []Step{
		{Action: "fill", Element: `textbox "Email"`},
		{Action: "fill", Element: `textbox "Name"`},
	}}}}, Pin{})
	if len(notes) != 0 {
		t.Fatalf("a first pin reported %q", notes)
	}
	p := Pin{Entries: []Entry{{Steps: []Step{
		{Action: "fill", Element: `textbox "Company"`},
		{Action: "fill", Element: `textbox "Email"`},
		{Action: "fill", Element: `textbox "Full name"`},
	}}}}
	got, notes := withInputs(p, prev)
	var names []string
	for _, in := range got.Inputs() {
		names = append(names, in.Name)
	}
	if strings.Join(names, ",") != "textbox-company,textbox-email,textbox-full-name" {
		t.Fatalf("inputs %v", names)
	}
	if strings.Join(notes, "; ") != "new input textbox-company; new input textbox-full-name" {
		t.Fatalf("notes %q", notes)
	}
	// Inputs with no element (a goto's url) do not match by label: a goto added
	// before the old one changes their count, so both get fresh names, and
	// neither reuses the old name url, so a --set url=... fails, not shifts.
	urls, _ := withInputs(Pin{Entries: []Entry{{Steps: []Step{{Action: "goto", Input: "url"}}}}}, Pin{})
	_, notes = withInputs(Pin{Entries: []Entry{{Steps: []Step{{Action: "goto", Input: "url"}, {Action: "goto", Input: "url"}}}}}, urls)
	if strings.Join(notes, "; ") != "new input url-2; new input url-3" {
		t.Fatalf("url notes %q", notes)
	}
	bound, err := bindValues(got.Steps(), 0, map[string]string{"textbox-company": "Lumen", "textbox-email": "e@x.io", "textbox-full-name": "Eve"})
	if err != nil || bound[0].Value != "Lumen" || bound[1].Value != "e@x.io" || bound[2].Value != "Eve" {
		t.Fatalf("bound %+v %v", bound, err)
	}
}

// A replayed step runs only on the page it recorded. A step that recorded no
// page is not checked, and a page that cannot be read fails a checked step.
func TestPageCheck(t *testing.T) {
	if got := urlPath("http://127.0.0.1:8000/orders/new?draft=1"); got != "/orders/new" {
		t.Fatalf("path: %q", got)
	}
	if got := urlPath("about:blank"); got != "about:blank" {
		t.Fatalf("about:blank: %q", got)
	}
	if got := urlPath(""); got != "" {
		t.Fatalf("empty href: %q", got)
	}
	if msg := pageMismatch("", "/y"); msg != "" {
		t.Fatalf("unrecorded page checked: %q", msg)
	}
	if msg := pageMismatch("/x", "/x"); msg != "" {
		t.Fatalf("same page refused: %q", msg)
	}
	if msg := pageMismatch("/x", "/y"); msg != "expected page /x, on /y" {
		t.Fatalf("wrong page: %q", msg)
	}
	if msg := pageMismatch("/x", ""); msg == "" {
		t.Fatal("an unreadable page passed a checked step")
	}
}

// After a goto whose URL is an input, steps are not checked until the next goto
// with a fixed URL; from there they are checked again.
func TestInputURLSkipsPageCheck(t *testing.T) {
	steps := unchecked([]Step{
		{Action: "click", Path: "/home"},
		{Action: "goto", Input: "url", Path: "/home"},
		{Action: "fill", Path: "/orders/abc", Input: "textbox-name"},
		{Action: "click", Path: "/orders/abc/done"},
		{Action: "goto", URL: "http://127.0.0.1/list", Path: "/orders/abc/done"},
		{Action: "click", Path: "/list"},
	})
	var paths []string
	for _, st := range steps {
		paths = append(paths, st.Path)
	}
	if got := strings.Join(paths, ","); got != "/home,,,,,/list" {
		t.Fatalf("checked paths %q, want /home,,,,,/list", got)
	}
}

// A path that differs only in a record ID (digits, a UUID or a 24-hex ObjectId) is the same page;
// any other difference, or another segment count, is not.
func TestSamePage(t *testing.T) {
	u1, u2 := "3f2b8c1e-9a4d-4e2f-8b1a-0c9d8e7f6a5b", "A1B2C3D4-E5F6-4711-8899-AABBCCDDEEFF"
	cases := []struct {
		a, b string
		want bool
	}{
		{"/orders/123/edit", "/orders/456/edit", true},
		{"/orders/123/edit", "/orders/123/view", false},
		{"/vendors/" + u1, "/vendors/" + u2, true},
		{"/vendors/" + u1 + "/edit", "/vendors/" + u2 + "/view", false},
		{"/orders/123/edit", "/orders/new/edit", false},
		{"/orders/123", "/orders/123/edit", false},
		{"/orders/1/edit", "/orders/1/edit/", false},
		{"/x/507f1f77bcf86cd799439011/edit", "/x/65A1B2C3D4E5F60718293A4B/edit", true},
		{"/x/507f1f77bcf86cd799439011/edit", "/x/42/edit", true},
		{"/x/507f1f77bcf86cd79943901/edit", "/x/42/edit", false},   // 23 hex
		{"/x/507f1f77bcf86cd7994390111/edit", "/x/42/edit", false}, // 25 hex
		{"/x/507f1f77bcf86cd79943901g/edit", "/x/42/edit", false},  // not hex
		{"/vendors/lumen-analytics", "/vendors/acme-corp", false},
		{"/customers/cus_123", "/customers/cus_456", false},
		{"about:blank", "about:blank", true},
		{"/", "/", true},
	}
	for _, c := range cases {
		if got := samePage(c.a, c.b); got != c.want {
			t.Errorf("samePage(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if msg := pageMismatch("/orders/1/edit", "/orders/2/edit"); msg != "" {
		t.Fatalf("another record refused: %q", msg)
	}
}

// A goto is never checked against the page it leaves: a pin recorded from
// about:blank replays on a tab that shows anything else. Other steps keep
// their page.
func TestGotoSkipsPageCheck(t *testing.T) {
	steps := []Step{{Action: "goto", URL: "http://127.0.0.1/a", Path: "about:blank"}, {Action: "click", Path: "/a"}}
	got := unchecked(steps)
	if pageMismatch(got[0].Path, "/somewhere/else") != "" {
		t.Fatalf("goto checked: %+v", got[0])
	}
	if got[1].Path != "/a" || steps[0].Path != "about:blank" {
		t.Fatalf("got %+v, input %+v", got, steps)
	}
}

// A task that starts with bx open pins the open as its first navigation, a
// goto whose URL follows the goto scrub rules.
func TestOpenIsRecordedAsGoto(t *testing.T) {
	open := openSteps(Request{Op: "open", Args: map[string]string{"url": "http://127.0.0.1:8000/orders/1/edit"}}, false)
	if len(open) != 1 || open[0].Action != "goto" || open[0].URL != "http://127.0.0.1:8000/orders/1/edit" || open[0].Input != "" {
		t.Fatalf("open: %+v", open)
	}
	if slot := openSteps(Request{Op: "open", Args: map[string]string{"url": "http://127.0.0.1/a?token=x"}}, false); slot[0].URL != "" || slot[0].Input != "url" {
		t.Fatalf("open with a query should be a url slot: %+v", slot)
	}
	p := Pin{Entries: []Entry{
		{Tab: "T", Op: "open", Steps: open},
		{Tab: "T", Op: "click", Steps: []Step{{Action: "click", Element: `button "Save"`}}},
	}}
	steps := p.Steps()
	if len(steps) != 2 || steps[0].Action != "goto" || refuseUnreplayable(steps, 0) != nil {
		t.Fatalf("pin steps %+v", steps)
	}
}

// The page path comes from the browser's target list, so a dialog does not
// block it, and a request with no planned steps reads no page at all.
func TestPagePathSource(t *testing.T) {
	infos := []*target.Info{{TargetID: "A", URL: "http://127.0.0.1:8000/orders/new?draft=1"}, {TargetID: "B", URL: "about:blank"}}
	if got := urlPath(pageURL(infos, "A")); got != "/orders/new" {
		t.Fatalf("tab A path: %q", got)
	}
	if got := urlPath(pageURL(infos, "C")); got != "" {
		t.Fatalf("unlisted tab path: %q", got)
	}
	read := false
	stampPath(nil, func() string { read = true; return "/x" })
	if read {
		t.Fatal("the page was read with no planned steps")
	}
	steps := []Step{{Action: "click"}, {Action: "key"}}
	stampPath(steps, func() string { return "/orders/new" })
	for _, st := range steps {
		if st.Path != "/orders/new" {
			t.Fatalf("step not stamped: %+v", st)
		}
	}
}
