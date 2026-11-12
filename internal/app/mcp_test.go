package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mcpSession sends lines to the server and returns its replies by id.
func mcpSession(t *testing.T, lines ...string) map[string]map[string]any {
	t.Helper()
	var out strings.Builder
	if err := serveMCP(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	replies := map[string]map[string]any{}
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad reply %q", sc.Text())
		}
		id, _ := json.Marshal(m["id"])
		replies[string(id)] = m
	}
	return replies
}

func TestMCPProtocol(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BX_BROWSER", "brave")
	dir := filepath.Join(home, "Library/Application Support/BraveSoftware/Brave-Browser")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "Local State"), []byte(`{"profile":{"info_cache":{"Default":{"name":"Work"}}}}`), 0o600)

	r := mcpSession(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_profiles","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"bogus"}`,
		`{"jsonrpc":"2.0","id":"p","method":"ping"}`,
	)
	if len(r) != 6 {
		t.Fatalf("want 6 replies (the notification gets none), got %d: %v", len(r), r)
	}
	init := r["1"]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-06-18" || init["serverInfo"].(map[string]any)["name"] != "bx" {
		t.Errorf("initialize: %v", init)
	}
	tools := r["2"]["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		m := tl.(map[string]any)
		names[m["name"].(string)] = true
		if m["inputSchema"].(map[string]any)["type"] != "object" {
			t.Errorf("%s: schema is not an object", m["name"])
		}
	}
	for _, want := range []string{"list_profiles", "open", "snapshot", "click", "fill", "screenshot", "dialog"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	text := r["3"]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"name":"Work"`) {
		t.Errorf("list_profiles: %s", text)
	}
	if r["4"]["result"].(map[string]any)["isError"] != true {
		t.Errorf("unknown tool must be a tool error: %v", r["4"])
	}
	if r["5"]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Errorf("unknown method: %v", r["5"])
	}
	if _, ok := r[`"p"`]["result"]; !ok {
		t.Errorf("ping: %v", r[`"p"`])
	}
}

func TestMCPFillValidation(t *testing.T) {
	r := mcpSession(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fill","arguments":{"tab":"T","fields":[]}}}`)
	if r["1"]["result"].(map[string]any)["isError"] != true {
		t.Fatalf("empty fields must fail: %v", r["1"])
	}
}

func TestAddMCPServerKeepsOtherSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	os.WriteFile(path, []byte(`{"theme":"dark","n":12345678901234567890,"mcpServers":{"other":{"command":"x"}}}`), 0o600)
	if err := addMCPServer(path, "/opt/bin/bx"); err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	b, _ := os.ReadFile(path)
	json.Unmarshal(b, &cfg)
	servers := cfg["mcpServers"].(map[string]any)
	if servers["other"] == nil || cfg["theme"] != "dark" || !strings.Contains(string(b), "12345678901234567890") {
		t.Errorf("lost settings: %s", b)
	}
	bx := servers["bx"].(map[string]any)
	if bx["command"] != "/opt/bin/bx" || bx["args"].([]any)[0] != "mcp" {
		t.Errorf("bx entry: %v", bx)
	}
	// Broken JSON is left untouched.
	os.WriteFile(path, []byte(`{nope`), 0o600)
	if err := addMCPServer(path, "/opt/bin/bx"); err == nil {
		t.Error("invalid JSON must be refused")
	}
	if b, _ := os.ReadFile(path); string(b) != `{nope` {
		t.Error("invalid file was changed")
	}
	// A missing file is created.
	fresh := filepath.Join(t.TempDir(), "sub", "mcp.json")
	if err := addMCPServer(fresh, "/opt/bin/bx"); err != nil {
		t.Fatal(err)
	}
}
