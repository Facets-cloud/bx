package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Facets-cloud/bx/internal/exit"
)

func TestParseArgs(t *testing.T) {
	a, err := parseArgs([]string{"list", "tabs", "--profile", "Client", "-o=json", "-q"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(a.pos, " ") != "list tabs" || a.flags["profile"] != "Client" || a.flags["output"] != "json" || !a.has("quiet") {
		t.Fatalf("got %+v", a)
	}
	// "--" lets a value that starts with "-" through as a positional word.
	a, err = parseArgs([]string{"fill", "T1", "#n", "--", "-5"})
	if err != nil || len(a.pos) != 4 || a.pos[3] != "-5" {
		t.Fatalf("got %+v, %v", a, err)
	}
	for _, bad := range [][]string{{"--nope"}, {"--profile"}, {"--yes=1"}} {
		if _, err := parseArgs(bad); exit.CodeOf(err) != exit.Validation {
			t.Errorf("parseArgs(%v): want validation error, got %v", bad, err)
		}
	}
}

func TestTimeout(t *testing.T) {
	a, _ := parseArgs([]string{"--timeout", "2m"})
	if v, err := a.timeout(); err != nil || v != "2m" {
		t.Fatalf("got %q, %v", v, err)
	}
	for _, bad := range []string{"soon", "-1s", "0s"} {
		a, _ := parseArgs([]string{"--timeout=" + bad})
		if _, err := a.timeout(); exit.CodeOf(err) != exit.Validation {
			t.Errorf("timeout %q: want validation error", bad)
		}
	}
}

// run runs the CLI with JSON output and returns the exit code and stderr.
func run(t *testing.T, argv ...string) (int, map[string]any) {
	t.Helper()
	a, err := parseArgs(append(argv, "-o", "json"))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := out{json: true, w: &stdout, errw: &stderr}
	e := env{a: a, o: o}
	err = e.dispatch()
	if err == nil {
		return 0, nil
	}
	code := o.fail(err)
	var body map[string]any
	if err := json.Unmarshal(stderr.Bytes(), &body); err != nil {
		t.Fatalf("stderr is not JSON: %q", stderr.String())
	}
	return code, body
}

// These fail before any browser or daemon is contacted.
func TestValidationExitCodes(t *testing.T) {
	cases := [][]string{
		{"frobnicate"},
		{"close", "tab", "T1"},                         // no --yes
		{"open", "--", "--renderer-cmd-prefix=x"},      // flag-like URL past the parser
		{"open", "example.com", "--profile", "Facets"}, // no scheme
		{"click", "T1"},                                // missing selector
		{"read", "T1", "--timeout", "never"},
		{"cdp"},
		{"list", "tabs", "extra"},
		{"fill", "T1", "e3"},                  // element without a value
		{"fill", "T1", "e3", "a", "e4"},       // odd pair
		{"dialog", "T1", "maybe"},             // not accept|dismiss
		{"eval", "T1"},                        // no script
		{"eval", "T1", "1", "--file", "x.js"}, // both
		{"upload", "T1", "e2"},                // no file
		{"key", "T1"},
		{"goto", "T1"},
		{"wait"},
		{"drag", "T1", "e1"},
	}
	for _, c := range cases {
		code, body := run(t, c...)
		if code != exit.Validation {
			t.Errorf("%v: exit %d, want %d (%v)", c, code, exit.Validation, body)
		}
		if body["code"] != float64(exit.Validation) || body["error"] == "" {
			t.Errorf("%v: JSON error body %v", c, body)
		}
	}
}

func TestDispatchPrefersTwoWordCommands(t *testing.T) {
	a, _ := parseArgs([]string{"version"})
	var stdout bytes.Buffer
	e := env{a: a, o: out{w: &stdout}}
	if err := e.dispatch(); err != nil || strings.TrimSpace(stdout.String()) != Version {
		t.Fatalf("version: %q, %v", stdout.String(), err)
	}
}

func TestDoneAndQuiet(t *testing.T) {
	var b bytes.Buffer
	out{w: &b}.done("clicked x")
	out{w: &b, quiet: true}.done("hidden")
	out{w: &b, json: true}.done("ignored")
	if got := b.String(); got != "clicked x\n{\n  \"message\": \"ignored\",\n  \"ok\": true\n}\n" {
		t.Fatalf("got %q", got)
	}
}

func TestClip(t *testing.T) {
	if clip("abcdef", 4) != "abc…" || clip("abc", 4) != "abc" {
		t.Fatal("clip")
	}
}
