package browser

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Facets-cloud/bx/internal/exit"
)

// AgentSuffix marks a browser instance that bx launches itself.
const AgentSuffix = "-agent"

// agent returns the separate instance of b that `bx launch` starts: its own
// user data dir under bx's folder, debugging on a local port, no Allow
// dialog. It shares no logins with the user's everyday profiles.
func (b Browser) agent() Browser {
	base, _ := os.UserConfigDir()
	b.Name += AgentSuffix
	b.Label += " (agent)"
	b.DataDir = filepath.Join(base, "bx", b.Name)
	b.Agent = true
	return b
}

// Running reports whether the browser has its user data dir open.
// Chromium holds a SingletonLock symlink there, pointing at "<host>-<pid>".
// A browser that was killed can leave the link behind, so the PID must
// also be alive.
func (b Browser) Running() bool {
	target, err := os.Readlink(filepath.Join(b.Dir(), "SingletonLock"))
	if err != nil {
		return false
	}
	i := strings.LastIndex(target, "-")
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 0 {
		return true // unknown format: assume it runs, the safe answer
	}
	// Signal 0 checks existence; EPERM means it exists as another user.
	err = syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// The Local State setting that the inspect page's "Allow remote
// debugging" switch stores.
var debugPref = []string{"devtools", "remote_debugging", "user-enabled"}

// DebuggingEnabled reports whether the remote-debugging switch is on.
func (b Browser) DebuggingEnabled() (bool, error) {
	state, err := b.readLocalState()
	if err != nil {
		return false, err
	}
	v, _ := lookupPath(state, debugPref).(bool)
	return v, nil
}

// SetDebugging turns the remote-debugging switch on or off by editing
// Local State. The browser rewrites that file when it quits, so it must not
// be running.
func (b Browser) SetDebugging(on bool) error {
	if b.Running() {
		return exit.New(exit.User, "quit %s first: it rewrites its settings when it quits", b.Label)
	}
	path := filepath.Join(b.Dir(), "Local State")
	orig, err := os.ReadFile(path)
	if err != nil {
		return localStateErr(b, err)
	}
	state, err := decodeState(orig)
	if err != nil {
		return err
	}
	setPath(state, debugPref, on)
	out, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp := path + ".bx-tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return exit.New(exit.User, "write Local State: %v", err)
	}
	return os.Rename(tmp, path)
}

func (b Browser) readLocalState() (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(b.Dir(), "Local State"))
	if err != nil {
		return nil, localStateErr(b, err)
	}
	return decodeState(data)
}

func localStateErr(b Browser, err error) error {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return exit.New(exit.Permission, "cannot read %s's settings: give your terminal app Full Disk Access", b.Label)
	case errors.Is(err, fs.ErrNotExist):
		return exit.New(exit.User, "%s has no settings yet: start it once, then quit it", b.Label)
	}
	return exit.New(exit.Upstream, "read %s's settings: %v", b.Label, err)
}

// decodeState keeps numbers as written, so large integers survive a rewrite.
func decodeState(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, exit.New(exit.Upstream, "parse Local State: %v", err)
	}
	return m, nil
}

func lookupPath(m map[string]any, path []string) any {
	var cur any = m
	for _, k := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[k]
	}
	return cur
}

func setPath(m map[string]any, path []string, v any) {
	for _, k := range path[:len(path)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[path[len(path)-1]] = v
}

// Start opens the browser app normally (after SetDebugging).
func (b Browser) Start() error {
	bundle := b.App
	if i := strings.Index(bundle, ".app/"); i >= 0 {
		bundle = bundle[:i+len(".app")]
	}
	if out, err := exec.Command("open", "-a", bundle).CombinedOutput(); err != nil {
		return exit.New(exit.Upstream, "start %s: %v: %s", b.Label, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// LaunchAgent starts the agent instance, detached, with debugging on an
// OS-chosen local port, and waits until its DevToolsActivePort appears.
func (b Browser) LaunchAgent() error {
	if !b.Agent {
		return exit.New(exit.Validation, "%s is not an agent browser", b.Name)
	}
	if b.Running() {
		return nil
	}
	if err := os.MkdirAll(b.Dir(), 0o700); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(b.Dir(), "DevToolsActivePort"))
	cmd := exec.Command(b.App, "--user-data-dir="+b.Dir(), "--remote-debugging-port=0",
		"--no-first-run", "--no-default-browser-check")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return exit.New(exit.Upstream, "launch %s: %v", b.Label, err)
	}
	_ = os.WriteFile(b.pidFile(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	_ = cmd.Process.Release()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(filepath.Join(b.Dir(), "DevToolsActivePort")); err == nil {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return exit.New(exit.Upstream, "%s started but opened no debugging port within 15s", b.Label)
}

func (b Browser) pidFile() string { return filepath.Join(b.Dir(), "bx-agent.pid") }

// QuitAgent asks the agent browser to quit (SIGTERM, which Chromium
// handles as a normal quit). It signals the recorded PID only if that
// process still runs with this agent's user data dir, so a reused PID is
// never touched. It reports whether it sent the signal.
func (b Browser) QuitAgent() (bool, error) {
	if !b.Agent || !b.Running() {
		return false, nil
	}
	raw, err := os.ReadFile(b.pidFile())
	if err != nil {
		return false, exit.New(exit.User, "%s was not started by this bx; quit it from its window", b.Label)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		return false, exit.New(exit.User, "bad PID file %s", b.pidFile())
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || !strings.Contains(string(out), "--user-data-dir="+b.Dir()) {
		return false, exit.New(exit.User, "%s is running but not as the process bx started; quit it from its window", b.Label)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return false, exit.New(exit.Upstream, "quit %s: %v", b.Label, err)
	}
	// Wait for it to exit, so a bx launch right after starts a new one
	// instead of finding this one still shutting down.
	for i := 0; i < 100 && b.Running(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if b.Running() {
		return true, exit.New(exit.Upstream, "%s is still shutting down after 10s", b.Label)
	}
	return true, nil
}
