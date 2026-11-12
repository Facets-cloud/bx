package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Facets-cloud/bx/internal/browser"
	"github.com/Facets-cloud/bx/internal/exit"
)

// cmdSetup turns on the browser's remote-debugging switch, the one-time
// step that otherwise means a visit to its inspect page.
func cmdSetup(e env) error {
	b := e.b
	if b.Agent {
		e.o.done(b.Label + " needs no setup: bx launch turns debugging on")
		return nil
	}
	on, err := b.DebuggingEnabled()
	if err != nil {
		return err
	}
	switch {
	case on && b.Running():
		e.o.done("remote debugging is already on in " + b.Label)
		return nil
	case on:
		e.o.done("remote debugging is on; start " + b.Label + " and run a bx command")
		return nil
	case b.Running():
		return exit.New(exit.User, "quit %s, then run bx setup again (or turn it on yourself at %s)", b.Label, b.Inspect)
	}
	if err := b.EnableDebugging(); err != nil {
		return err
	}
	if err := b.Start(); err != nil {
		return err
	}
	if !waitForFile(filepath.Join(b.Dir(), "DevToolsActivePort"), 30*time.Second) {
		return exit.New(exit.Upstream, "%s started but did not turn debugging on; turn it on at %s", b.Label, b.Inspect)
	}
	e.o.done(fmt.Sprintf("remote debugging is on in %s; the first bx command asks you to Allow once", b.Label))
	return nil
}

// cmdLaunch starts the separate agent instance of the browser.
func cmdLaunch(e env) error {
	b := e.b
	if !b.Agent {
		var err error
		if b, err = browser.Lookup(b.Name + browser.AgentSuffix); err != nil {
			return err
		}
	}
	was := b.Running()
	if err := b.LaunchAgent(); err != nil {
		return err
	}
	switch {
	case e.o.quiet:
	case e.o.json:
		e.o.printJSON(map[string]any{"browser": b.Name, "data_dir": b.Dir(), "already_running": was})
	default:
		fmt.Fprintf(e.o.w, "%s is running (its own profile folder: %s)\n", b.Label, b.Dir())
		fmt.Fprintf(e.o.w, "Drive it with --browser %s, or export BX_BROWSER=%s.\n", b.Name, b.Name)
		fmt.Fprintln(e.o.w, "It never asks to Allow, so any program on this Mac can drive it. Log in there only to what agents need.")
	}
	return nil
}

func waitForFile(path string, d time.Duration) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}
