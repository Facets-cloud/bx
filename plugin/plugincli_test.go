package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The bx-cli plugin (plugin-cli/) carries copies, because a plugin can only
// use files inside its own folder.
func TestPluginCLICopiesMatch(t *testing.T) {
	for copy, orig := range map[string]string{
		"../plugin-cli/skills/bx/SKILL.md": "skills/bx/SKILL.md",
		"../plugin-cli/hooks/install.sh":   "../site/install.sh",
	} {
		a, err := os.ReadFile(copy)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(orig)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs from %s: cp %s %s", copy, orig, orig, copy)
		}
	}
}

// bx-cli has no MCP server, and its JSON files parse.
func TestPluginCLIManifests(t *testing.T) {
	if _, err := os.Stat("../plugin-cli/.mcp.json"); err == nil {
		t.Fatal("plugin-cli must not ship .mcp.json: Claude Code would start the MCP server")
	}
	for _, f := range []string{"../plugin-cli/.claude-plugin/plugin.json", "../plugin-cli/hooks/hooks.json", "../.claude-plugin/marketplace.json"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	for _, f := range []string{"../plugin-cli/bin/bx", "../plugin-cli/hooks/setup.sh", "../plugin-cli/hooks/install.sh"} {
		st, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode()&0o111 == 0 {
			t.Errorf("%s is not executable", f)
		}
	}
}

// fakeRelease serves bx_darwin_<arch>.tar.gz holding a stub bx, and a
// checksums.txt that matches it unless tamper is set.
func fakeRelease(t *testing.T, tamper bool) string {
	t.Helper()
	stub := "#!/bin/sh\necho stub-bx \"$@\"\n"
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "bx", Mode: 0o755, Size: int64(len(stub))})
	tw.Write([]byte(stub))
	tw.Close()
	gz.Close()
	arch := map[string]string{"arm64": "arm64", "amd64": "amd64"}[runtime.GOARCH]
	asset := "bx_darwin_" + arch + ".tar.gz"
	sum := sha256.Sum256(tgz.Bytes())
	if tamper {
		sum[0] ^= 1
	}
	sums := fmt.Sprintf("%s  %s\n%s  bx.mcpb\n", hex.EncodeToString(sum[:]), asset, strings.Repeat("0", 64))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest/download/" + asset:
			w.Write(tgz.Bytes())
		case "/latest/download/checksums.txt":
			w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func installEnv(t *testing.T, releases string) (dir string, env []string) {
	dir = t.TempDir()
	return dir, append(os.Environ(), "BX_RELEASES_URL="+releases, "BX_INSTALL_DIR="+dir, "BX_VERSION=")
}

func TestInstallScript(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("install.sh is macOS only")
	}
	dir, env := installEnv(t, fakeRelease(t, false))
	cmd := exec.Command("sh", "../site/install.sh")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	out, err := exec.Command(filepath.Join(dir, "bx"), "version").Output()
	if err != nil || string(out) != "stub-bx version\n" {
		t.Fatalf("installed bx: %q, %v", out, err)
	}

	dir, env = installEnv(t, fakeRelease(t, true))
	cmd = exec.Command("sh", "../site/install.sh")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "does not match") {
		t.Fatalf("a tampered download must fail: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "bx")); err == nil {
		t.Fatal("a tampered download must not be installed")
	}
}

// The launcher installs bx on first use, then runs it with the same args.
func TestLauncher(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("install.sh is macOS only")
	}
	dir, env := installEnv(t, fakeRelease(t, false))
	for i := 0; i < 2; i++ { // first run installs, second runs the installed one
		cmd := exec.Command("../plugin-cli/bin/bx", "list", "tabs")
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil || string(out) != "stub-bx list tabs\n" {
			t.Fatalf("run %d: %q, %v", i, out, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "bx")); err != nil {
		t.Fatal("the launcher should have installed bx")
	}

	// The session-start hook installs quietly and never fails.
	dir, env = installEnv(t, fakeRelease(t, false))
	cmd := exec.Command("sh", "../plugin-cli/hooks/setup.sh")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("setup.sh: %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bx")); err != nil {
		t.Fatal("setup.sh should have installed bx")
	}
}
