package daemon

import (
	"strings"
	"testing"
	"time"
)

func TestDescribeLeavesOutTypedValues(t *testing.T) {
	cases := []struct {
		op   string
		a    map[string]string
		want string
	}{
		{"fill", map[string]string{"pairs": `[["textbox \"Password\"","hunter2"],["e4","x"]]`}, `textbox "Password", e4`},
		{"type", map[string]string{"selector": "e9", "text": "secret note"}, "e9 text (11 chars)"},
		{"eval", map[string]string{"script": "document.cookie"}, "script (15 chars)"},
		{"batch", map[string]string{"steps": `[{"action":"fill","element":"e3","value":"pw"},{"action":"key","key":"Enter"},{"action":"goto","url":"https://a.test/"}]`},
			"3 steps: fill e3; key Enter; goto https://a.test/"},
	}
	for _, c := range cases {
		got := describe(c.op, c.a, false)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.op, got, c.want)
		}
		for _, v := range []string{"hunter2", "secret note", "document.cookie", `"pw"`} {
			if strings.Contains(got, v) {
				t.Errorf("%s: logged the typed value %q", c.op, v)
			}
		}
	}
	if got := describe("fill", cases[0].a, true); !strings.Contains(got, "hunter2") {
		t.Errorf("full logging left the value out: %q", got)
	}
}

func TestHistoryGroupsSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now()
	writeEntry(Entry{T: now.Add(-time.Hour), Session: "mcp-a", Title: "Old task", Browser: "brave", URL: "https://www.example.com/x", Op: "click", MS: 10})
	writeEntry(Entry{T: now.Add(-time.Minute), Session: "mcp-b", Browser: "brave", URL: "https://docs.test/a", Op: "goto", MS: 5})
	writeEntry(Entry{T: now, Session: "mcp-b", Browser: "brave", Op: "fill", Error: "no element", MS: 5})
	list, err := ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	_, es, _ := LoadSession("mcp-b")
	if len(list) != 2 || list[0].ID != "mcp-b" || list[1].Title != "Old task" {
		t.Fatalf("sessions not newest first: %+v", list)
	}
	if list[0].Actions != 2 || list[0].Errors != 1 || list[1].Sites[0] != "example.com" {
		t.Errorf("bad summary: %+v %+v", list[0], list[1])
	}
	if len(es) != 2 {
		t.Errorf("want 2 entries for mcp-b, got %d", len(es))
	}
}

func TestSessionIDFollowsTheHarness(t *testing.T) {
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "79ff63bd-e3f2")
	a, b := NewSessionID(), NewSessionID()
	if a != b || !strings.HasPrefix(a, "bx-") || strings.Contains(a, "79ff") {
		t.Errorf("want one stable bx id per harness session, got %q and %q", a, b)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	if c, d := NewSessionID(), NewSessionID(); c == d || !strings.HasPrefix(c, "bx-") {
		t.Errorf("want unique bx ids outside a harness, got %q and %q", c, d)
	}
}

func TestCleanURLKeepsOnlyThePage(t *testing.T) {
	for in, want := range map[string]string{
		"https://app.test/reset?token=abc123#step2":    "https://app.test/reset",
		"https://user:pw@mail.test/inbox?ik=1&view=pt": "https://mail.test/inbox",
		"https://example.com/":                         "https://example.com/",
		"":                                             "",
	} {
		if got := cleanURL(in); got != want {
			t.Errorf("cleanURL(%q) = %q, want %q", in, got, want)
		}
	}
	if got := describe("goto", map[string]string{"url": "https://a.test/cb?code=xyz"}, false); got != "https://a.test/cb" {
		t.Errorf("goto target kept the query: %q", got)
	}
	if got := describe("batch", map[string]string{"steps": `[{"action":"goto","url":"https://a.test/?sid=9"}]`}, false); strings.Contains(got, "sid") {
		t.Errorf("batch goto kept the query: %q", got)
	}
}
