package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Facets-cloud/bx/internal/daemon"
)

// --set repeats, and its value keeps any '=' after the first.
func TestParseArgsRepeatsSet(t *testing.T) {
	a, err := parseArgs([]string{"tab1", "Onboard", "vendor", "--set", "email=a=b", "--set", "pw=x"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"email=a=b", "pw=x"}; !reflect.DeepEqual(a.sets, want) {
		t.Fatalf("sets %v, want %v", a.sets, want)
	}
	if want := []string{"tab1", "Onboard", "vendor"}; !reflect.DeepEqual(a.pos, want) {
		t.Fatalf("pos %v, want %v", a.pos, want)
	}
}

// pinned lists the inputs a pin needs, and tells the agent to run it; a pin
// without steps says to pin again.
func TestPinRecipeListsInputs(t *testing.T) {
	p := daemon.Pin{PinHeader: daemon.PinHeader{Name: "Onboard vendor", Slug: "onboard-vendor"}, Entries: []daemon.Entry{{
		Op: "batch", Steps: []daemon.Step{{Action: "fill", Element: `textbox "Email"`, Input: "textbox-email"}},
	}}}
	out := pinRecipe(p)
	if !strings.Contains(out, "textbox-email  fill textbox \"Email\"") || !strings.Contains(out, "bx run <tab> \"Onboard vendor\"") {
		t.Fatalf("recipe does not list the input and the run command:\n%s", out)
	}
	old := daemon.Pin{PinHeader: daemon.PinHeader{Name: "Old"}, Entries: []daemon.Entry{{Op: "batch", Target: "1 steps: click x"}}}
	if !strings.Contains(pinRecipe(old), "pin the task again") {
		t.Fatal("a pin with no steps should say to pin again")
	}
}

// MCP run values: text, numbers and booleans are accepted as the input's text;
// a nested object or list is refused.
func TestRunValuesAcceptsScalars(t *testing.T) {
	got, err := runValues(map[string]any{"count": 3.0, "ok": true, "email": "a@b.test"})
	if err != nil {
		t.Fatal(err)
	}
	if got["count"] != "3" || got["ok"] != "true" || got["email"] != "a@b.test" {
		t.Fatalf("values %v", got)
	}
	if _, err := runValues(map[string]any{"x": map[string]any{"a": 1}}); err == nil {
		t.Fatal("a nested object should be refused")
	}
	if _, err := runValues("not an object"); err == nil {
		t.Fatal("a non-object should be refused")
	}
}
