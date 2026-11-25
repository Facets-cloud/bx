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

// Steps is the pin's steps in order, as bx run replays them.
func (p Pin) Steps() []Step {
	var out []Step
	for _, e := range p.Entries {
		out = append(out, e.Steps...)
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
// repeats, so each step asks for its own value.
func withInputs(p Pin) Pin {
	used := map[string]bool{}
	entries := make([]Entry, len(p.Entries))
	for i, e := range p.Entries {
		entries[i] = e
		if len(e.Steps) == 0 {
			continue
		}
		steps := slices.Clone(e.Steps)
		for j := range steps {
			st := &steps[j]
			if st.Action != "fill" && st.Action != "type" {
				continue
			}
			st.Value, st.Text = "", ""
			base := PinSlug(st.Element)
			if base == "" {
				base = st.Action
			}
			name := base
			for n := 2; used[name]; n++ {
				name = fmt.Sprintf("%s-%d", base, n)
			}
			used[name] = true
			st.Input = name
		}
		entries[i].Steps = steps
	}
	p.Entries = entries
	return p
}

// bindValues returns steps with each fill and type step given its input's
// value. It fails before anything runs when a value is missing or unknown.
func bindValues(steps []Step, values map[string]string) ([]Step, error) {
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
	out := slices.Clone(steps)
	for i, st := range out {
		if st.Input == "" {
			continue
		}
		v, ok := values[st.Input]
		if !ok {
			missing = append(missing, st.Input)
			continue
		}
		if st.Action == "fill" {
			out[i].Value = v
		} else {
			out[i].Text = v
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
	BatchResult
}

// runPin replays a pin's steps on a tab, with the values for its inputs.
func (s *server) runPin(id, name string, values map[string]string, timeout time.Duration) (RunResult, error) {
	p, err := LoadPin(name)
	if err != nil {
		return RunResult{}, err
	}
	steps := p.Steps()
	if len(steps) == 0 {
		return RunResult{}, exit.New(exit.User, "pin %q has no steps bx can replay: pin the task again", p.Name)
	}
	bound, err := bindValues(steps, values)
	if err != nil {
		return RunResult{}, err
	}
	res, err := s.runSteps(id, bound, timeout)
	if err != nil {
		return RunResult{}, err
	}
	out := RunResult{Pin: p.Name, BatchResult: res}
	if res.Failed != nil {
		if snap, err := s.snapshot(id, timeout); err == nil {
			out.Page = snap.Text()
		}
	}
	return out, nil
}

// SavePin writes a pin, replacing one with the same name.
func SavePin(p Pin) (string, error) {
	p.Slug, p.V, p.Type = PinSlug(p.Name), logVersion, "pin"
	if p.Slug == "" {
		return "", exit.New(exit.Validation, "a pin needs a name with letters or digits")
	}
	p = withInputs(p)
	dir, err := pinsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
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
		return "", err
	}
	return path, os.Rename(tmp, path)
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
