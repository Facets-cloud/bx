package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/bx/internal/daemon"
)

func TestSessionTreeGroupsTasksAndPages(t *testing.T) {
	t0 := time.Date(2026, 11, 14, 12, 0, 0, 0, time.Local)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	es := []daemon.Entry{
		{T: at(0), Op: "open", Target: "https://a.test", Profile: "Work", URL: "https://a.test/"},
		{T: at(1), Op: "start", Target: "Fill the form"},
		{T: at(2), Op: "click", Target: `button "Next"`, Profile: "Work", URL: "https://a.test/", To: "https://a.test/2"},
		{T: at(3), Op: "fill", Target: "e4", Profile: "Work", URL: "https://a.test/2", Error: "no element"},
		{T: at(4), Op: "done", Target: "Filled, not sent"},
	}
	info := daemon.SessionInfo{ID: "bx-0a1b2c3d4e5f", HSess: "0b1c-uuid", Harness: "claude-code", Log: "/x.jsonl", Start: at(0), End: at(4)}
	out := sessionTree(info, es, "", 0)
	for _, want := range []string{
		"resume: claude --resume 0b1c-uuid",
		`├─ 1 (untitled)`,
		`└─ 2 "Fill the form"`,
		`2 actions, 1 failed  done: "Filled, not sent"`,
		`click button "Next"  ok 0s  -> a.test/2`,
		"Work  a.test/2",
		"fill e4  FAILED: no element",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tree lacks %q:\n%s", want, out)
		}
	}
}
