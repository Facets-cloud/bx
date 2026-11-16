package daemon

import (
	"os/exec"
	"strings"

	"github.com/Facets-cloud/bx/internal/exit"
)

// approve asks the user, in a macOS dialog outside the browser, to allow a
// change an agent asked for. The agent can't answer it: it is not part of
// any page bx can reach. Deny is the default button, and no answer within
// a minute counts as Deny. The message goes to osascript as an argument,
// never into the script, so a page's text can't change it.
//
// A click on "Always allow" returns remember true, so the caller can stop
// asking for this site (see consentFor); "Allow" allows the one change
// without remembering it.
var approve = func(task, what string) (remember bool, err error) {
	msg := "An agent wants to " + what + "."
	if task != "" {
		msg = "The agent working on \"" + task + "\" wants to " + what + "."
	}
	out, oerr := exec.Command("osascript",
		"-e", "on run argv",
		"-e", `display dialog (item 1 of argv) with title "bx" buttons {"Deny", "Allow", "Always allow"} default button "Deny" cancel button "Deny" with icon caution giving up after 60`,
		"-e", "end run", msg).Output()
	btn, ok := clicked(string(out), oerr)
	if !ok {
		return false, exit.New(exit.Permission, "the user did not allow this: %s", what)
	}
	return btn == "Always allow", nil
}

// clicked reads the dialog's answer: the button the user clicked, and
// whether it allows the change. Deny makes osascript fail ("User
// cancelled") and a timeout reports gave up, so both leave ok false. Both
// Allow and Always allow leave ok true; the caller tells them apart by the
// button name.
func clicked(out string, err error) (button string, ok bool) {
	if err != nil || strings.Contains(out, "gave up:true") {
		return "", false
	}
	const p = "button returned:"
	i := strings.Index(out, p)
	if i < 0 {
		return "", false
	}
	rest := out[i+len(p):]
	if j := strings.IndexAny(rest, ",\n"); j >= 0 {
		rest = rest[:j]
	}
	button = strings.TrimSpace(rest)
	return button, button == "Allow" || button == "Always allow"
}
