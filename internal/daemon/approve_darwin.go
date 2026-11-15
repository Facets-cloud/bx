package daemon

import (
	"os/exec"
	"strings"

	"github.com/Facets-cloud/bx/internal/exit"
)

// approve asks the user, in a macOS dialog outside the browser, to allow
// a change an agent asked for. The agent can't answer it: it is not part
// of any page bx can reach. Deny is the default button, and no answer
// within a minute counts as Deny. The message goes to osascript as an
// argument, never into the script, so a page's text can't change it.
var approve = func(task, what string) error {
	msg := "An agent wants to " + what + "."
	if task != "" {
		msg = "The agent working on \"" + task + "\" wants to " + what + "."
	}
	out, err := exec.Command("osascript",
		"-e", "on run argv",
		"-e", `display dialog (item 1 of argv) with title "bx" buttons {"Deny", "Allow"} default button "Deny" cancel button "Deny" with icon caution giving up after 60`,
		"-e", "end run", msg).Output()
	if !allowed(string(out), err) {
		return exit.New(exit.Permission, "the user did not allow this: %s", what)
	}
	return nil
}

// allowed reads the dialog's answer: only a click on Allow counts. Deny
// makes osascript fail ("User cancelled"), and a timeout reports gave up.
func allowed(out string, err error) bool {
	return err == nil && strings.Contains(out, "button returned:Allow") && !strings.Contains(out, "gave up:true")
}
