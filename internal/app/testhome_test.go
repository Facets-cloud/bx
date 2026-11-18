package app

import "testing"

// testHome points HOME and the Windows home/config roots at a fresh temp dir,
// so a test's reads and writes never touch the real user's files on any OS.
func testHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)         // macOS/Linux
	t.Setenv("USERPROFILE", dir)  // Windows UserHomeDir
	t.Setenv("APPDATA", dir)      // Windows UserConfigDir
	t.Setenv("LOCALAPPDATA", dir) // Windows local state
	return dir
}
