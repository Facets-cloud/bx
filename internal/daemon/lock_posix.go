//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// lockFile takes an exclusive, advisory, whole-file lock, released when the
// file is closed or the process exits, so two bx commands starting at once
// never start two daemons.
func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// detachDaemon puts the spawned daemon in its own session, so it outlives the
// bx command that started it.
func detachDaemon(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
