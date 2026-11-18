//go:build !windows

package browser

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Facets-cloud/bx/internal/exit"
)

// Running reports whether the browser has its user data dir open. Chromium
// holds a SingletonLock symlink there, pointing at "<host>-<pid>". A browser
// that was killed can leave the link behind, so the PID must also be alive.
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

// detach puts the launched process in its own session, so it outlives the
// bx process that started it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// startBrowser opens the browser app normally (after SetDebugging).
func startBrowser(b Browser) error {
	bundle := b.App
	if i := strings.Index(bundle, ".app/"); i >= 0 {
		bundle = bundle[:i+len(".app")]
	}
	if out, err := exec.Command("open", "-a", bundle).CombinedOutput(); err != nil {
		return exit.New(exit.Upstream, "start %s: %v: %s", b.Label, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// quitProcess asks the agent browser with this PID to quit (SIGTERM, which
// Chromium handles as a normal quit). It signals the PID only after
// confirming the process still runs with this agent's user data dir, so a
// reused PID is never touched.
func quitProcess(b Browser, pid int) error {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || !strings.Contains(string(out), "--user-data-dir="+b.Dir()) {
		return exit.New(exit.User, "%s is running but not as the process bx started; quit it from its window", b.Label)
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return exit.New(exit.Upstream, "quit %s: %v", b.Label, err)
	}
	return nil
}
