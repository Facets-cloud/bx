//go:build windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// Windows has no flock and no Setsid. The lock uses kernel32's LockFileEx,
// loaded lazily, so bx needs no extra module; the daemon is spawned detached
// with process-creation flags from the standard syscall package.
var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock = 0x00000002
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

// lockFile takes an exclusive, whole-file lock, released when the file is
// closed or the process exits, so two bx commands starting at once never
// start two daemons.
func lockFile(f *os.File) error {
	var ol syscall.Overlapped
	r, _, e := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock, 0, 0xffffffff, 0xffffffff, uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		return e
	}
	return nil
}

// unlockFile releases the lock. A failed unlock is not fatal: the lock is
// released when the file is closed or the process exits anyway.
func unlockFile(f *os.File) error {
	var ol syscall.Overlapped
	_, _, _ = procUnlockFileEx.Call(f.Fd(), 0, 0xffffffff, 0xffffffff, uintptr(unsafe.Pointer(&ol)))
	return nil
}

// detachDaemon starts the spawned daemon in its own group, detached from any
// console, so it outlives the bx command that started it.
func detachDaemon(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}
