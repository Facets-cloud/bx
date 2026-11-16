//go:build !darwin && !windows

package daemon

import "github.com/Facets-cloud/bx/internal/exit"

// approve has no dialog to show on this system yet, so it refuses every
// change: a change to the user's logins never goes ahead unapproved. A
// port adds its own dialog here (zenity or kdialog on Linux, a message
// box on Windows).
var approve = func(task, what string) error {
	return exit.New(exit.Permission, "bx can't ask the user to allow this on this system yet, so it didn't: %s", what)
}
