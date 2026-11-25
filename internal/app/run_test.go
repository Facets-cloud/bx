package app

import (
	"reflect"
	"testing"
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
