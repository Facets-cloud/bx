package app

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Facets-cloud/bx/internal/browser"
	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

func cmdListProfiles(e env) error {
	if err := need(e.pos, 0, "bx list profiles"); err != nil {
		return err
	}
	ps, err := e.b.Profiles()
	if err != nil {
		return err
	}
	if e.o.json {
		e.o.printJSON(ps)
		return nil
	}
	var rows [][]string
	for _, p := range ps {
		rows = append(rows, []string{p.Name, p.Dir})
	}
	e.o.table([]string{"NAME", "FOLDER"}, rows)
	return nil
}

// printTab prints one tab: an object in JSON, a one-row table otherwise.
func printTab(o out, t daemon.Tab) {
	if o.json {
		o.printJSON(t)
		return
	}
	printTabs(o, []daemon.Tab{t})
}

func printTabs(o out, tabs []daemon.Tab) {
	if o.json {
		o.printJSON(tabs)
		return
	}
	var rows [][]string
	for _, t := range tabs {
		p := t.Profile
		if p == "" {
			p = "?"
		}
		rows = append(rows, []string{t.ID, p, clip(t.Title, 40), clip(t.URL, 60)})
	}
	o.table([]string{"ID", "PROFILE", "TITLE", "URL"}, rows)
}

func cmdListTabs(e env) error {
	if err := need(e.pos, 0, "bx list tabs [--profile <name>]"); err != nil {
		return err
	}
	var tabs []daemon.Tab
	if err := e.call("tabs", map[string]string{"profile": e.a.flags["profile"]}, &tabs); err != nil {
		return err
	}
	printTabs(e.o, tabs)
	return nil
}

func cmdShowTab(e env) error {
	if err := need(e.pos, 1, "bx show tab <id>"); err != nil {
		return err
	}
	var t daemon.Tab
	if err := e.call("show", map[string]string{"tab": e.pos[0]}, &t); err != nil {
		return err
	}
	printTab(e.o, t)
	return nil
}

func cmdOpen(e env) error {
	if err := need(e.pos, 1, "bx open <url> --profile <name>"); err != nil {
		return err
	}
	if err := browser.CheckURL(e.pos[0]); err != nil {
		return err
	}
	if e.a.flags["profile"] == "" {
		return exit.New(exit.Validation, "--profile is required (see bx list profiles)")
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	var tab daemon.Tab
	args := map[string]string{"url": e.pos[0], "profile": e.a.flags["profile"], "timeout": t}
	if err := e.call("open", args, &tab); err != nil {
		return err
	}
	if !e.o.quiet {
		printTab(e.o, tab)
	}
	return nil
}

func cmdRead(e env) error {
	if err := need(e.pos, 1, "bx read <tab> [--selector <css>] [--format text|html]"); err != nil {
		return err
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	var c daemon.Content
	args := map[string]string{"tab": e.pos[0], "selector": e.a.flags["selector"], "format": e.a.flags["format"], "timeout": t}
	if err := e.call("read", args, &c); err != nil {
		return err
	}
	if e.o.json {
		e.o.printJSON(c)
	} else {
		fmt.Fprintln(e.o.w, c.Content)
	}
	return nil
}

func cmdCloseTab(e env) error {
	if err := need(e.pos, 1, "bx close tab <id> --yes"); err != nil {
		return err
	}
	if !e.a.has("yes") {
		return exit.New(exit.Validation, "closing a tab needs --yes")
	}
	if err := e.call("close", map[string]string{"tab": e.pos[0]}, nil); err != nil {
		return err
	}
	e.o.done("closed " + e.pos[0])
	return nil
}

func cmdScreenshot(e env) error {
	if err := need(e.pos, 1, "bx screenshot <tab> [--selector <css>] [--full] [--out <file.png>|-]"); err != nil {
		return err
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	var img daemon.Image
	args := map[string]string{"tab": e.pos[0], "selector": e.a.flags["selector"], "timeout": t}
	if e.a.has("full") {
		args["full"] = "true"
	}
	if err := e.call("screenshot", args, &img); err != nil {
		return err
	}
	path := e.a.flags["out"]
	if path == "-" {
		_, err := os.Stdout.Write(img.PNG)
		return err
	}
	if path == "" {
		path = fmt.Sprintf("bx-%s.png", time.Now().Format("20060102-150405"))
	}
	if err := os.WriteFile(path, img.PNG, 0o644); err != nil {
		return exit.New(exit.User, "write %s: %v", path, err)
	}
	switch {
	case e.o.quiet:
	case e.o.json:
		e.o.printJSON(map[string]any{"path": path, "bytes": len(img.PNG)})
	default:
		fmt.Fprintln(e.o.w, path)
	}
	return nil
}

func cmdCDP(e env) error {
	if len(e.pos) < 1 || len(e.pos) > 2 {
		return exit.New(exit.Validation, "usage: bx cdp <Method> [<json-params>] [--tab <id>]")
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	args := map[string]string{"method": e.pos[0], "tab": e.a.flags["tab"], "timeout": t}
	if len(e.pos) == 2 {
		args["params"] = e.pos[1]
	}
	var res json.RawMessage
	if err := e.call("cdp", args, &res); err != nil {
		return err
	}
	if len(res) == 0 {
		res = json.RawMessage("{}")
	}
	e.o.printJSON(res)
	return nil
}

func cmdStatus(e env) error {
	var st daemon.Status
	err := daemon.Call(e.b.Name, daemon.Request{Op: "status"}, &st, false)
	if exit.CodeOf(err) == exit.User {
		// Not running is a normal answer to "status", not a failure.
		if e.o.json {
			e.o.printJSON(map[string]any{"browser": e.b.Name, "running": false})
		} else {
			fmt.Fprintf(e.o.w, "no bx connection to %s\n", e.b.Label)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if e.o.json {
		e.o.printJSON(map[string]any{"browser": st.Browser, "running": true, "pid": st.PID, "since": st.Since, "known_profiles": st.KnownProfiles})
		return nil
	}
	fmt.Fprintf(e.o.w, "connected to %s (pid %d) since %s; profiles mapped: %d\n",
		e.b.Label, st.PID, st.Since.Format(time.Kitchen), len(st.KnownProfiles))
	return nil
}

func cmdStop(e env) error {
	err := daemon.Call(e.b.Name, daemon.Request{Op: "stop"}, nil, false)
	if exit.CodeOf(err) == exit.User {
		e.o.done("not running")
		return nil
	}
	if err != nil {
		return err
	}
	e.o.done("stopped")
	return nil
}
