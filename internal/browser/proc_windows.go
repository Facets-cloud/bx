//go:build windows

package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Facets-cloud/bx/internal/exit"
)

// Windows has no POSIX SingletonLock symlink and no syscall.Kill, so the
// process control here uses the built-in Windows tools (tasklist, taskkill)
// and PowerShell, the same way the macOS code uses open, osascript and ps.
// No cgo and no extra module: process creation flags come from the standard
// syscall package.
const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// Running reports whether the browser is up. For the agent instance (its own
// data dir, started by bx launch) it checks the PID bx recorded; for the
// user's everyday browser it checks whether the executable runs at all, since
// several windows share one process and bx never started it.
func (b Browser) Running() bool {
	if b.Agent {
		raw, err := os.ReadFile(b.pidFile())
		if err != nil {
			return false
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || pid <= 0 {
			return false
		}
		return pidAlive(pid)
	}
	return exeRunning(filepath.Base(b.App))
}

// detach starts the process in its own group, detached from any console, so
// it outlives the bx process that started it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}

// startBrowser opens the browser app normally (after SetDebugging).
func startBrowser(b Browser) error {
	cmd := exec.Command(b.App)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return exit.New(exit.Upstream, "start %s: %v", b.Label, err)
	}
	_ = cmd.Process.Release()
	return nil
}

// quitProcess quits the agent browser with this PID. It first confirms the
// process still runs with this agent's user data dir, so a reused PID is
// never touched, then ends it and its child processes (renderers, GPU) with
// taskkill. Chromium's agent instance is bx's own throwaway profile, so a
// forced quit loses nothing the user cares about.
func quitProcess(b Browser, pid int) error {
	if !strings.Contains(procCmdline(pid), "--user-data-dir="+b.Dir()) {
		return exit.New(exit.User, "%s is running but not as the process bx started; quit it from its window", b.Label)
	}
	if out, err := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").CombinedOutput(); err != nil {
		return exit.New(exit.Upstream, "quit %s: %v: %s", b.Label, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// exeRunning reports whether any process has the given executable name.
func exeRunning(name string) bool {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+name, "/NH", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(name))
}

// pidAlive reports whether a process with this PID is running.
func pidAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "\""+strconv.Itoa(pid)+"\"")
}

// procCmdline returns a process's command line, or "" if it can't be read.
// The PID goes into a -Filter string, never the script body, and it is only
// ever an integer from bx's own PID file, so a page's text can't reach it.
func procCmdline(pid int) string {
	script := "(Get-CimInstance Win32_Process -Filter ('ProcessId=" + strconv.Itoa(pid) + "')).CommandLine"
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return ""
	}
	return string(out)
}
