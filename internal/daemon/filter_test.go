package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/accessibility"
)

func sampleSnap() Snapshot {
	return Snapshot{Nodes: []SnapNode{
		{Ref: "e1", Depth: 0, Role: "main"},
		{Ref: "e2", Depth: 1, Role: "heading", Name: "Cart"},
		{Ref: "e3", Depth: 1, Role: "list"},
		{Ref: "e4", Depth: 2, Role: "StaticText", Name: "Mug"},
		{Ref: "e5", Depth: 2, Role: "button", Name: "Remove"},
		{Ref: "e6", Depth: 1, Role: "button", Name: "Checkout"},
	}}
}

func refs(s Snapshot) string {
	out := ""
	for _, n := range s.Nodes {
		out += n.Ref + " "
	}
	return out
}

func TestFilterInteractive(t *testing.T) {
	s, err := sampleSnap().Filter("", true)
	if err != nil || refs(s) != "e2 e5 e6 " || s.Nodes[1].Depth != 0 {
		t.Fatalf("got %q %v", refs(s), err)
	}
}

func TestFilterWithin(t *testing.T) {
	s, err := sampleSnap().Filter("e3", false)
	if err != nil || refs(s) != "e3 e4 e5 " || s.Nodes[0].Depth != 0 || s.Nodes[1].Depth != 1 {
		t.Fatalf("got %q %v", refs(s), err)
	}
	if _, err := sampleSnap().Filter("e99", false); err == nil {
		t.Fatal("want an error for a missing ref")
	}
}

func TestBatchRejectsBadSteps(t *testing.T) {
	s := &server{}
	for _, raw := range []string{"", "[]", "{", `{"action":"click"}`} {
		if _, err := s.batch("tab", raw, time.Second); err == nil {
			t.Fatalf("%q: want a validation error", raw)
		}
	}
}

func TestDiffNodes(t *testing.T) {
	a := []SnapNode{{Ref: "e1", Role: "button", Name: "Next"}, {Ref: "e2", Role: "textbox", Name: "Name"}, {Ref: "e3", Role: "alert"}}
	b := []SnapNode{{Ref: "e1", Role: "button", Name: "Next"}, {Ref: "e2", Role: "textbox", Name: "Name", Value: "Ravi"},
		{Ref: "e9", Role: "StaticText", Name: "GSTIN must look like 27ABCDE1234F1Z5."}}
	got := strings.Join(diffNodes(a, b), "\n")
	want := "~ e2 textbox \"Name\" value=\"Ravi\"\n+ e9 StaticText \"GSTIN must look like 27ABCDE1234F1Z5.\"\n- e3"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestStableRefs(t *testing.T) {
	first := []*accessibility.Node{
		{NodeID: "1", Role: axv("main"), ChildIDs: []accessibility.NodeID{"2"}, BackendDOMNodeID: 1},
		{NodeID: "2", ParentID: "1", Role: axv("button"), Name: axv("Next"), BackendDOMNodeID: 2},
	}
	_, rs := buildSnapshot(first, refState{})
	second := []*accessibility.Node{
		{NodeID: "1", Role: axv("main"), ChildIDs: []accessibility.NodeID{"3", "2"}, BackendDOMNodeID: 1},
		{NodeID: "3", ParentID: "1", Role: axv("alert"), Name: axv("Fix the errors"), BackendDOMNodeID: 3},
		{NodeID: "2", ParentID: "1", Role: axv("button"), Name: axv("Next"), BackendDOMNodeID: 2},
	}
	got, _ := buildSnapshot(second, rs)
	if got[1].Ref != "e3" || got[2].Ref != "e2" {
		t.Fatalf("want the new alert as e3 and Next to keep e2, got %+v", got)
	}
}

func TestDiffRedrawn(t *testing.T) {
	a := []SnapNode{{Ref: "e1", Role: "heading", Name: "Approvals"}, {Ref: "e2", Role: "combobox", Name: "Add approver"}, {Ref: "e3", Role: "option", Name: "Arjun Mehta"}}
	b := []SnapNode{{Ref: "e4", Role: "heading", Name: "Approvals"}, {Ref: "e5", Role: "StaticText", Name: "Arjun Mehta"}, {Ref: "e6", Role: "combobox", Name: "Add approver"}}
	got := strings.Join(diffNodes(a, b), "\n")
	want := "+ e5 StaticText \"Arjun Mehta\"\n- e3\n= redrawn, new ref=old ref: e4=e1 e6=e2"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
