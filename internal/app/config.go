package app

import (
	"fmt"

	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

// cmdConfig shows the log settings: bx config.
func cmdConfig(e env) error {
	ss := daemon.LoadConfig().Settings()
	if e.o.json {
		e.o.printJSON(ss)
		return nil
	}
	fmt.Fprint(e.o.w, daemon.FormatSettings(ss))
	return nil
}

// cmdConfigSet changes one: bx config set <key> <value>. At a terminal
// the user may change anything; an agent's MCP call goes through
// setSetting with fromAgent set.
func cmdConfigSet(e env) error {
	if len(e.pos) != 2 {
		return exit.New(exit.Validation, "usage: bx config set <key> <value> (see bx config)")
	}
	if err := setSetting(e.pos[0], e.pos[1], false); err != nil {
		return err
	}
	e.o.done(e.pos[0] + " = " + e.pos[1])
	return nil
}

func setSetting(key, value string, fromAgent bool) error {
	c := daemon.LoadConfig()
	if err := c.Set(key, value, fromAgent); err != nil {
		return err
	}
	if err := daemon.SaveConfig(c); err != nil {
		return exit.New(exit.User, "save the settings: %v", err)
	}
	return nil
}
