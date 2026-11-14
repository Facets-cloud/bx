package app

import (
	"fmt"
	"os"

	"github.com/Facets-cloud/bx/internal/browser"
	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

// config is what bx remembers between runs.
// chooseBrowser picks the browser to drive: --browser, then $BX_BROWSER,
// then the default saved by bx use, then the first with debugging on.
func chooseBrowser(flag string) (browser.Browser, error) {
	for _, name := range []string{flag, os.Getenv("BX_BROWSER"), daemon.LoadConfig().Browser} {
		if name != "" {
			return browser.Lookup(name)
		}
	}
	return browser.Detect(), nil
}

func cmdUse(e env) error {
	if len(e.pos) == 0 {
		name := daemon.LoadConfig().Browser
		if name == "" {
			name = "(none: bx picks the browser with remote debugging on)"
		}
		if e.o.json {
			e.o.printJSON(map[string]string{"default": daemon.LoadConfig().Browser})
		} else {
			fmt.Fprintln(e.o.w, "default browser:", name)
		}
		return nil
	}
	if err := need(e.pos, 1, "bx use <browser>"); err != nil {
		return err
	}
	b, err := browser.Lookup(e.pos[0])
	if err != nil {
		return err
	}
	c := daemon.LoadConfig()
	c.Browser = b.Name
	if err := daemon.SaveConfig(c); err != nil {
		return exit.New(exit.User, "save the default: %v", err)
	}
	e.o.done("bx now drives " + b.Label + " by default")
	return nil
}

// BrowserState is one row of bx list browsers.
type BrowserState struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Debugging bool   `json:"debugging"` // the remote-debugging switch is on
	Connected bool   `json:"connected"` // a bx daemon holds a connection
	Default   bool   `json:"default"`
}

func cmdListBrowsers(e env) error {
	if err := need(e.pos, 0, "bx list browsers"); err != nil {
		return err
	}
	var rows []BrowserState
	for _, name := range browser.Names() {
		b, _ := browser.Lookup(name)
		_, statErr := os.Stat(b.App)
		on, _ := b.DebuggingEnabled()
		rows = append(rows, BrowserState{
			Name: name, Installed: statErr == nil, Running: b.Running(),
			Debugging: on, Connected: daemon.Running(name), Default: name == e.b.Name,
		})
	}
	if e.o.json {
		e.o.printJSON(rows)
		return nil
	}
	yes := func(v bool) string {
		if v {
			return "yes"
		}
		return "-"
	}
	var table [][]string
	for _, r := range rows {
		mark := ""
		if r.Default {
			mark = "*"
		}
		table = append(table, []string{mark, r.Name, yes(r.Installed), yes(r.Running), yes(r.Debugging), yes(r.Connected)})
	}
	e.o.table([]string{"", "BROWSER", "INSTALLED", "RUNNING", "DEBUGGING", "CONNECTED"}, table)
	return nil
}
