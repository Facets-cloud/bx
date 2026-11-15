package daemon

import "testing"

func TestParseMouse(t *testing.T) {
	o, err := parseMouse(map[string]string{"x": "100", "y": "40.5"})
	if err != nil || o.Action != "click" || o.X != 100 || o.Y != 40.5 {
		t.Fatalf("default click: %+v, %v", o, err)
	}
	o, err = parseMouse(map[string]string{"action": "wheel", "x": "1", "y": "2", "dy": "300"})
	if err != nil || o.DY != 300 {
		t.Fatalf("wheel: %+v, %v", o, err)
	}
	for _, a := range []map[string]string{
		{"x": "1"},                             // no y
		{"x": "a", "y": "2"},                   // not a number
		{"action": "drag", "x": "1", "y": "2"}, // unknown action
	} {
		if _, err := parseMouse(a); err == nil {
			t.Errorf("%v: want an error", a)
		}
	}
}

func TestMocks(t *testing.T) {
	var m tabMocks
	a, err := parseMock(map[string]string{"url": "*/api/orders*", "status": "500"})
	if err != nil {
		t.Fatal(err)
	}
	m.put(a)
	if k, ok := m.find("https://shop.test/api/orders?page=2"); !ok || k.Status != 500 || k.ContentType != "application/json" {
		t.Errorf("find: %+v %v", k, ok)
	}
	if _, ok := m.find("https://shop.test/api/users"); ok {
		t.Error("matched a URL the pattern doesn't cover")
	}
	// A dot in the pattern is a dot, not any character.
	b, _ := parseMock(map[string]string{"url": "https://a.test/x?y"})
	m.put(b)
	if _, ok := m.find("https://aXtest/x?y"); ok {
		t.Error("dot matched any character")
	}
	if _, ok := m.find("https://a.test/x1y"); !ok {
		t.Error("? should match one character")
	}
	a.Status = 404
	m.put(a) // same pattern: replaces
	if len(m.snapshot()) != 2 {
		t.Errorf("want 2 mocks, got %d", len(m.snapshot()))
	}
	m.remove(a.URL)
	if len(m.snapshot()) != 1 {
		t.Errorf("remove one: got %d", len(m.snapshot()))
	}
	m.remove("")
	if len(m.snapshot()) != 0 {
		t.Error("remove all left mocks")
	}
	for _, bad := range []map[string]string{{}, {"url": "*", "status": "99"}, {"url": "*", "status": "x"}} {
		if _, err := parseMock(bad); err == nil {
			t.Errorf("%v: want an error", bad)
		}
	}
}

func TestStorageChange(t *testing.T) {
	ok := []StorageChange{{"set", "cookies", "sid", "1"}, {"delete", "local", "k", ""}, {"clear", "session", "", ""}}
	for _, c := range ok {
		if err := c.check(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []StorageChange{{"set", "cookies", "", "1"}, {"wipe", "cookies", "a", ""}, {"clear", "disk", "", ""}}
	for _, c := range bad {
		if err := c.check(); err == nil {
			t.Errorf("%+v: want an error", c)
		}
	}
	got := StorageChange{"set", "cookies", "sid", "secret-token"}.describe("shop.test")
	if got != `set the cookie "sid" on shop.test` {
		t.Errorf("describe: %s", got)
	}
	if got := (StorageChange{Action: "clear", Area: "local"}).describe("shop.test"); got != "clear every localStorage item of shop.test" {
		t.Errorf("describe clear: %s", got)
	}
}

func TestPDFName(t *testing.T) {
	for in, want := range map[string]string{
		"Order #123: Amazon.in": "Order #123- Amazon.in.pdf",
		"a/b":                   "a-b.pdf",
		"  ":                    "page.pdf",
	} {
		if got := pdfName(in); got != want {
			t.Errorf("pdfName(%q) = %q, want %q", in, got, want)
		}
	}
}
