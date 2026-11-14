package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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

// SavePin writes a pin, replacing one with the same name.
func SavePin(p Pin) (string, error) {
	p.Slug, p.V, p.Type = PinSlug(p.Name), logVersion, "pin"
	if p.Slug == "" {
		return "", exit.New(exit.Validation, "a pin needs a name with letters or digits")
	}
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
