//go:build !darwin

package daemon

import "github.com/Facets-cloud/bx/internal/exit"

// approve has no dialog to show on this system yet, so it refuses every
// change: a change to the user's logins never goes ahead unapproved, and
// there is nothing to remember. A port adds its own dialog here (zenity or
// kdialog on Linux, a message box on Windows) and returns remember true
// when the user picks its "always allow" option.
var approve = func(task, what string) (remember bool, err error) {
	return false, exit.New(exit.Permission, "bx can't ask the user to allow this on this system yet, so it didn't: %s", what)
}
