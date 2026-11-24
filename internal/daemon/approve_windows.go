//go:build windows

package daemon

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/Facets-cloud/bx/internal/exit"
)

// approve asks the user, in a Windows dialog outside the browser, to allow a
// change an agent asked for. The agent can't answer it: it is not part of
// any page bx can reach. No is the default button, and no answer within a
// minute counts as No. The message goes to the dialog through an environment
// variable, never the script text, so a page's text can't change it.
var approve = func(task, what string) error {
	msg := "An agent wants to " + what + "."
	if task != "" {
		msg = "The agent working on \"" + task + "\" wants to " + what + "."
	}
	const script = `Add-Type -AssemblyName System.Windows.Forms | Out-Null;` +
		`$r=[System.Windows.Forms.MessageBox]::Show($env:BX_APPROVE_MSG,'bx',` +
		`[System.Windows.Forms.MessageBoxButtons]::YesNo,` +
		`[System.Windows.Forms.MessageBoxIcon]::Warning,` +
		`[System.Windows.Forms.MessageBoxDefaultButton]::Button2);` +
		`if($r -eq [System.Windows.Forms.DialogResult]::Yes){exit 0}else{exit 1}`
	// A minute to answer, then the context ends powershell and it counts as No.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA", "-Command", script)
	cmd.Env = append(os.Environ(), "BX_APPROVE_MSG="+msg)
	if err := cmd.Run(); err != nil {
		return exit.New(exit.Permission, "the user did not allow this: %s", what)
	}
	return nil
}
