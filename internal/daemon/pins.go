package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Facets-cloud/bx/internal/exit"
)

// A pin is one task from a session log, saved under a name so an agent can
// do it again later. It is a copy of the task's entries in <Dir>/pins, so
// the log's size and age limits never remove it. Values were never logged,
// so repeating a pin means asking the user for them again.

// PinHeader is the first line of a pin file.
type PinHeader struct {
	V       int       `json:"v"`
	Type    string    `json:"type"` // "pin"
	Name    string    `json:"name"`
	Slug    string    `json:"slug"`
	Title   string    `json:"title,omitempty"` // the task's title when it ran
	Session string    `json:"session"`
	HSess   string    `json:"harness_session,omitempty"`
	Project string    `json:"project,omitempty"`
	Pinned  time.Time `json:"pinned"`
}

// Pin is a saved task.
type Pin struct {
	PinHeader
	Entries []Entry `json:"entries"`
}

func pinsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pins"), nil
}

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

// PinSlug is the file name a pin's name maps to: "Onboard vendor" is onboard-vendor.
func PinSlug(name string) string {
	return strings.Trim(notSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// Pin inputs. A fill or type step's value is never kept: the pin names it an
// input instead, and bx run takes the value for each input from the user.

// PinInput is one value a pin needs, and the element it goes into.
type PinInput struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// otherTab is the unreplayable marker for a step on a tab other than the
// task's first acting tab: a replay runs every step on the one tab it is given.
const otherTab = "acts on a second tab"

// Steps is the pin's steps in order, as bx run replays them. The steps of an
// entry on a tab other than the task's first acting tab are marked
// unreplayable (see otherTab), so run refuses the pin before it acts.
func (p Pin) Steps() []Step {
	var out []Step
	first, started := "", false
	for _, e := range p.Entries {
		if len(e.Steps) == 0 {
			continue
		}
		if !started {
			first, started = e.Tab, true
		}
		if e.Tab == first {
			out = append(out, e.Steps...)
			continue
		}
		for _, st := range e.Steps {
			out = append(out, Step{Action: st.Action, Unreplayable: otherTab})
		}
	}
	return out
}

// Inputs is the values the pin needs, in the order its steps use them.
func (p Pin) Inputs() []PinInput {
	var out []PinInput
	for _, st := range p.Steps() {
		if st.Input != "" {
			out = append(out, PinInput{Name: st.Input, Label: st.Action + " " + st.Element})
		}
	}
	return out
}

// withInputs scrubs the typed values from a pin's steps and names each fill
// and type step's input: its element's slug (textbox-email), with -2, -3 for
// repeats, so each step asks for its own value. prev is the pin p replaces, if
// any, and an input keeps its old name so a --set by that name still works:
//  1. an input whose action and element label are the old one's exactly (a
//     goto's url or a typed key has no element, so it has no label to match);
//  2. else, only when the pin has as many inputs of its kind (see inputKind)
//     as before, the old input at the same place among that kind, so a
//     renamed label keeps its name ("input <new> kept as <old>"). A field
//     removed and another added look the same, so when the labels differ
//     the step is marked ByPlace and bx run warns about it; a later re-pin
//     that matches it by rule 1 clears the mark;
//  3. else it gets a fresh name no old input had, reported as "new input
//     <name>".
//
// Matching by place when the count changed would shift every later value one
// field off, so it is never done. It returns the notes, in input order.
func withInputs(p, prev Pin) (Pin, []string) {
	var old []Step
	for _, e := range prev.Entries {
		for _, st := range e.Steps {
			if needsInput(st) {
				old = append(old, st)
			}
		}
	}
	entries := slices.Clone(p.Entries)
	var in []*Step
	for i := range entries {
		entries[i].Steps = slices.Clone(entries[i].Steps)
		for j := range entries[i].Steps {
			if st := &entries[i].Steps[j]; needsInput(*st) {
				st.Value, st.Text, st.Files, st.ByPlace = "", "", nil, false
				in = append(in, st)
			}
		}
	}
	names := make([]string, len(in))
	taken := make([]bool, len(old))
	for i, st := range in { // 1. the same label
		for k, o := range old {
			if !taken[k] && st.Element != "" && o.Action == st.Action && o.Element == st.Element {
				names[i], taken[k] = o.Input, true
				break
			}
		}
	}
	count := map[string]int{}
	oldPlace, newPlace := make([]int, len(old)), make([]int, len(in))
	for k, o := range old {
		oldPlace[k] = count[inputKind(o)]
		count[inputKind(o)]++
	}
	seen := map[string]int{}
	for i, st := range in {
		newPlace[i] = seen[inputKind(*st)]
		seen[inputKind(*st)]++
	}
	notes := make([]string, len(in))
	for i, st := range in { // 2. the same place, when the count is unchanged
		kind := inputKind(*st)
		if names[i] != "" || count[kind] != seen[kind] {
			continue
		}
		for k, o := range old {
			if !taken[k] && inputKind(o) == kind && oldPlace[k] == newPlace[i] {
				names[i], taken[k] = o.Input, true
				st.ByPlace = o.Element != st.Element
				if b := inputBase(*st); b != o.Input {
					notes[i] = fmt.Sprintf("input %s kept as %s", b, o.Input)
				}
				break
			}
		}
	}
	// A fresh name never reuses an old one, matched or not: a --set by an old
	// name must not land on another field.
	used := map[string]bool{}
	for _, o := range old {
		used[o.Input] = true
	}
	for _, n := range names {
		used[n] = true
	}
	for i, st := range in { // 3. a fresh name
		if names[i] == "" {
			base := inputBase(*st)
			name := base
			for n := 2; used[name]; n++ {
				name = fmt.Sprintf("%s-%d", base, n)
			}
			names[i], used[name] = name, true
			if len(prev.Entries) > 0 {
				notes[i] = "new input " + name
			}
		}
		st.Input = names[i]
	}
	p.Entries = entries
	return p, slices.DeleteFunc(notes, func(s string) bool { return s == "" })
}

// inputBase is the name an input gets when nothing older names it: the slot a
// step was recorded with (url, file, key), else its element's slug, else its
// action.
func inputBase(st Step) string {
	switch {
	case st.Input != "":
		return st.Input
	case PinSlug(st.Element) != "":
		return PinSlug(st.Element)
	}
	return st.Action
}

// inputKind is what a re-pin matches an input by: its action and its element's
// role, so "textbox Email" and "textbox Work email" are the same kind of input.
func inputKind(st Step) string {
	role, _, _ := strings.Cut(st.Element, " ")
	return st.Action + " " + role
}

// needsInput is whether a step reads a value from the user on replay: a fill,
// type or upload always does; a goto does when its URL was logged as a slot.
func needsInput(st Step) bool {
	switch st.Action {
	case "fill", "type", "upload":
		return true
	case "goto", "key":
		return st.Input != ""
	}
	return false
}

// refuseUnreplayable fails before anything runs when a step from index from on
// cannot be replayed: a page-changing op with no step form, or an element that
// has no place a replay can find. It names the step by its index, as a run's
// failed does.
func refuseUnreplayable(steps []Step, from int) error {
	for i := from; i < len(steps); i++ {
		st := steps[i]
		if st.Unreplayable != "" {
			return exit.New(exit.User, "step index %d (%s) cannot be replayed; finish by hand", i, st.Unreplayable)
		}
		if strings.HasSuffix(st.Element, unreplayable) {
			return exit.New(exit.User, "step index %d: %s cannot be found again on replay; finish by hand", i, strings.TrimSuffix(st.Element, unreplayable))
		}
	}
	return nil
}

// bindValues returns steps from index from on, each input given its value: a
// fill's Value, a type's Text, an upload's file, a goto's URL. It fails before
// anything runs when a value is missing, or names an input the pin does not
// have; a value for an input used only before from is ignored, so an agent
// that resends every value can resume.
func bindValues(steps []Step, from int, values map[string]string) ([]Step, error) {
	known := map[string]bool{}
	for _, st := range steps {
		if st.Input != "" {
			known[st.Input] = true
		}
	}
	var unknown, missing []string
	for k := range values {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	out := slices.Clone(steps[from:])
	for i, st := range out {
		if st.Input == "" {
			continue
		}
		v, ok := values[st.Input]
		if !ok {
			missing = append(missing, st.Input)
			continue
		}
		switch st.Action {
		case "fill":
			out[i].Value = v
		case "type":
			out[i].Text = v
		case "upload":
			out[i].Files = []string{v}
		case "goto":
			out[i].URL = v
		case "key":
			if !typedKey(v) {
				return nil, exit.New(exit.Validation, "input %s takes one printable character, not %q", st.Input, v)
			}
			out[i].Key = v
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return nil, exit.New(exit.Validation, "this pin has no input named %s", strings.Join(unknown, ", "))
	}
	if len(missing) > 0 {
		return nil, exit.New(exit.Validation, "the pin needs a value for each input: %s (give them with --set name=value)", strings.Join(missing, ", "))
	}
	return out, nil
}

// RunResult is a pin replayed on one tab: how far it got, as a batch's
// result, and the page as it stands when a step failed.
type RunResult struct {
	Pin  string `json:"pin"`
	Page string `json:"page,omitempty"`
	// Warning names the inputs a re-pin kept by place (see Step.ByPlace).
	Warning string `json:"warning,omitempty"`
	BatchResult
}

// checkFrom refuses a run --from outside the pin, and a step from there on
// that cannot be replayed. An earlier step is not run again, so it is not
// checked.
func checkFrom(steps []Step, from int) error {
	if from < 0 || from >= len(steps) {
		return exit.New(exit.Validation, "--from takes a step index from 0 to %d; the pin has %d steps", len(steps)-1, len(steps))
	}
	return refuseUnreplayable(steps, from)
}

// runPin replays a pin's steps from index from on, on a tab, with the values
// for their inputs.
func (s *server) runPin(id, name string, values map[string]string, from int, timeout time.Duration) (RunResult, error) {
	p, err := LoadPin(name)
	if err != nil {
		return RunResult{}, err
	}
	steps := unchecked(p.Steps())
	if len(steps) == 0 {
		return RunResult{}, exit.New(exit.User, "pin %q has no steps bx can replay: pin the task again", p.Name)
	}
	if err := checkFrom(steps, from); err != nil {
		return RunResult{}, err
	}
	bound, err := bindValues(steps, from, values)
	if err != nil {
		return RunResult{}, err
	}
	res, err := s.runSteps(id, bound, timeout)
	if err != nil {
		return RunResult{}, err
	}
	if res.Failed != nil {
		*res.Failed += from // the pin's index, as the log and the CLI name it
	}
	out := RunResult{Pin: p.Name, Warning: byPlaceWarning(bound), BatchResult: res}
	if res.Failed != nil {
		snap, err := s.snapshot(id, timeout)
		if err == nil {
			snap, err = snap.Filter("", true)
		}
		if err != nil {
			out.Page = "(page snapshot failed: " + err.Error() + ")"
		} else {
			out.Page = snap.Text()
		}
	}
	return out, nil
}

// byPlaceWarning is one line naming the inputs of steps that a re-pin kept by
// place, or "" when there are none.
func byPlaceWarning(steps []Step) string {
	var list []string
	seen := map[string]bool{}
	for _, st := range steps {
		if st.ByPlace && !seen[st.Input] {
			seen[st.Input] = true
			list = append(list, fmt.Sprintf("%s (now %s)", st.Input, st.Element))
		}
	}
	switch len(list) {
	case 0:
		return ""
	case 1:
		name, now, _ := strings.Cut(list[0], " ")
		return fmt.Sprintf("warning: %s was kept by position %s; check the value or re-pin", name, now)
	}
	return fmt.Sprintf("warning: %s were kept by position; check the values or re-pin", strings.Join(list, ", "))
}

// SavePin writes a pin, replacing one with the same name.
func SavePin(p Pin) (string, []string, error) {
	p.Slug, p.V, p.Type = PinSlug(p.Name), logVersion, "pin"
	if p.Slug == "" {
		return "", nil, exit.New(exit.Validation, "a pin needs a name with letters or digits")
	}
	prev, _ := LoadPin(p.Name) // none when there is no pin of that name yet
	p, notes := withInputs(p, prev)
	dir, err := pinsDir()
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	var b bytes.Buffer
	h, _ := json.Marshal(p.PinHeader)
	b.Write(append(h, '\n'))
	for _, e := range p.Entries {
		line, _ := json.Marshal(e)
		b.Write(append(line, '\n'))
	}
	path := filepath.Join(dir, p.Slug+".jsonl")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o600); err != nil {
		return "", nil, err
	}
	return path, notes, os.Rename(tmp, path)
}

// LoadPin reads the pin with that name or slug.
func LoadPin(name string) (Pin, error) {
	dir, err := pinsDir()
	if err != nil {
		return Pin{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, PinSlug(name)+".jsonl"))
	if err != nil {
		return Pin{}, exit.New(exit.User, "no pin named %q (see bx history pinned)", name)
	}
	var p Pin
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for first := true; sc.Scan(); first = false {
		if first {
			_ = json.Unmarshal(sc.Bytes(), &p.PinHeader)
			continue
		}
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			p.Entries = append(p.Entries, e)
		}
	}
	return p, nil
}

// ListPins reads every pin's header, newest first.
func ListPins() ([]Pin, error) {
	dir, err := pinsDir()
	if err != nil {
		return nil, err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	var out []Pin
	for _, f := range files {
		if p, err := LoadPin(strings.TrimSuffix(filepath.Base(f), ".jsonl")); err == nil {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pinned.After(out[j].Pinned) })
	return out, nil
}

// Unpin deletes a pin. The session log it came from is not touched.
func Unpin(name string) error {
	dir, err := pinsDir()
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, PinSlug(name)+".jsonl")); err != nil {
		return exit.New(exit.User, "no pin named %q (see bx history pinned)", name)
	}
	return nil
}
