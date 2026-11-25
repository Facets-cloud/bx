package app

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

// pinTask saves task n of a session (0: its last titled task, else its
// last task) as a pin named name.
func pinTask(ref, name string, n int) (daemon.Pin, string, error) {
	list, err := daemon.ListSessions()
	if err != nil {
		return daemon.Pin{}, "", err
	}
	info, es, err := daemon.LoadSession(resolveSession(ref, list, daemon.CurrentSession()))
	if err != nil {
		return daemon.Pin{}, "", err
	}
	tasks := splitTasks(es)
	if len(tasks) == 0 {
		return daemon.Pin{}, "", exit.New(exit.User, "no bx activity logged for session %q (see bx history)", ref)
	}
	t, err := pickTask(tasks, n)
	if err != nil {
		return daemon.Pin{}, "", err
	}
	p := daemon.Pin{PinHeader: daemon.PinHeader{Name: name, Title: t.title, Session: info.ID, HSess: info.HSess,
		Project: info.Project, Pinned: time.Now()}, Entries: t.entries}
	path, err := daemon.SavePin(p)
	p.Slug = daemon.PinSlug(name)
	return p, path, err
}

func pickTask(tasks []*task, n int) (*task, error) {
	if n > 0 {
		if n > len(tasks) {
			return nil, exit.New(exit.Validation, "the session has %d tasks; --task takes 1 to %d", len(tasks), len(tasks))
		}
		return tasks[n-1], nil
	}
	for i := len(tasks) - 1; i >= 0; i-- {
		if tasks[i].title != "" {
			return tasks[i], nil
		}
	}
	return tasks[len(tasks)-1], nil
}

// pinRecipe is a pin as steps an agent can follow again: the actions that
// worked, grouped by the page they ran on.
func pinRecipe(p daemon.Pin) string {
	var b strings.Builder
	fmt.Fprintf(&b, "pin %q (%s), pinned %s from session %s\n", p.Name, p.Slug, p.Pinned.Local().Format("Jan 2 2006"), p.Session)
	if p.Title != "" {
		fmt.Fprintf(&b, "the task was: %q\n", p.Title)
	}
	if len(p.Steps()) == 0 {
		b.WriteString("This pin has no replayable steps (saved before bx run): pin the task again.\n")
	} else {
		if in := p.Inputs(); len(in) > 0 {
			b.WriteString("inputs (ask the user for each value; values were never logged):\n")
			for _, x := range in {
				fmt.Fprintf(&b, "  %s  %s\n", x.Name, x.Label)
			}
		}
		fmt.Fprintf(&b, "To repeat it in one call: bx run <tab> %q --set <input>=<value> ... If it stops on a step, fix that step and re-pin under the same name.\n", p.Name)
	}
	steps, skipped, page, at := 0, 0, 0, ""
	for _, e := range p.Entries {
		if daemon.ReadOnly[e.Op] || e.Error != "" {
			skipped++
			continue
		}
		if url := daemon.ShortURL(e.URL); url != at || page == 0 {
			page, at = page+1, url
			fmt.Fprintf(&b, "%d. %s\n", page, pageLabel(e))
		}
		for _, s := range recipeSteps(e) {
			steps++
			fmt.Fprintf(&b, "   %s\n", s)
		}
	}
	fmt.Fprintf(&b, "(%s on %s; %d reads and failed attempts left out)\n", plural(steps, "step"), plural(page, "page"), skipped)
	return b.String()
}

// recipeSteps is an entry as steps: a batch's logged target ("3 steps:
// fill textbox "Email"; click button "Next"; ...") becomes one per step.
func recipeSteps(e daemon.Entry) []string {
	if e.Op != "batch" {
		return []string{strings.TrimSpace(e.Op + " " + e.Target)}
	}
	_, list, ok := strings.Cut(e.Target, ": ")
	if !ok {
		return []string{"batch " + e.Target}
	}
	return strings.Split(list, "; ")
}

// pinList is the pins, one per line.
func pinList(ps []daemon.Pin) string {
	if len(ps) == 0 {
		return "no pins yet: bx history pin <session> \"<name>\" saves a task to repeat later\n"
	}
	var b strings.Builder
	for _, p := range ps {
		n := 0
		for _, e := range p.Entries {
			if !daemon.ReadOnly[e.Op] && e.Error == "" {
				n += len(recipeSteps(e))
			}
		}
		fmt.Fprintf(&b, "%s  %q  %d steps  pinned %s", p.Slug, p.Name, n, p.Pinned.Local().Format("Jan 2 2006"))
		if p.Title != "" && p.Title != p.Name {
			fmt.Fprintf(&b, "  (task: %q)", p.Title)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// cmdPin is bx history pin <session> "<name>" [--task n].
func cmdPin(e env) error {
	if len(e.pos) < 3 {
		return exit.New(exit.Validation, `usage: bx history pin <session|current|previous> "<name>" [--task <n>]`)
	}
	n := 0
	if v := e.a.flags["task"]; v != "" {
		var err error
		if n, err = strconv.Atoi(v); err != nil || n < 1 {
			return exit.New(exit.Validation, "--task takes a task number from bx history <session>")
		}
	}
	p, path, err := pinTask(e.pos[1], strings.Join(e.pos[2:], " "), n)
	if err != nil {
		return err
	}
	e.o.done(fmt.Sprintf("pinned %q as %s (%s)", p.Name, p.Slug, path))
	return nil
}

// cmdPinned is bx history pinned [<name>].
func cmdPinned(e env) error {
	if len(e.pos) > 1 {
		p, err := daemon.LoadPin(strings.Join(e.pos[1:], " "))
		if err != nil {
			return err
		}
		if e.o.json {
			e.o.printJSON(p)
			return nil
		}
		fmt.Fprint(e.o.w, pinRecipe(p))
		return nil
	}
	ps, err := daemon.ListPins()
	if err != nil {
		return err
	}
	if e.o.json {
		e.o.printJSON(ps)
		return nil
	}
	fmt.Fprint(e.o.w, pinList(ps))
	return nil
}

// cmdRun is bx run <tab> <pin name> [--set <input>=<value> ...]: replays a
// saved pin on a tab. Its inputs are the values the user gave, never logged.
func cmdRun(e env) error {
	if len(e.pos) < 2 {
		return exit.New(exit.Validation, `usage: bx run <tab> <pin name> [--set <input>=<value> ...]`)
	}
	values := map[string]string{}
	for _, s := range e.a.sets {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" {
			return exit.New(exit.Validation, "--set takes <input>=<value>, got %q", s)
		}
		values[k] = v
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(values)
	var res daemon.RunResult
	if err := e.call("run", map[string]string{"tab": e.pos[0], "pin": strings.Join(e.pos[1:], " "),
		"values": string(body), "from": e.a.flags["from"], "timeout": t}, &res); err != nil {
		return err
	}
	e.o.printJSON(res)
	if res.Failed != nil {
		return exit.New(exit.User, "step index %d failed: %s", *res.Failed, res.Error)
	}
	return nil
}

// runValues is the MCP values object as the input name to value map a run
// takes. A value may be text, a number or a boolean; a nested object or list
// is refused.
func runValues(v any) (map[string]string, error) {
	out := map[string]string{}
	if v == nil {
		return out, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, exit.New(exit.Validation, "run: values must be an object of input name to value")
	}
	for k, x := range m {
		switch x.(type) {
		case map[string]any, []any:
			return nil, exit.New(exit.Validation, "run: the value for %s must be text, a number or true/false", k)
		}
		out[k] = argString(m, k)
	}
	return out, nil
}

// cmdUnpin is bx history unpin <name>.
func cmdUnpin(e env) error {
	if len(e.pos) < 2 {
		return exit.New(exit.Validation, "usage: bx history unpin <name>")
	}
	if err := daemon.Unpin(strings.Join(e.pos[1:], " ")); err != nil {
		return err
	}
	e.o.done("unpinned " + strings.Join(e.pos[1:], " "))
	return nil
}

// pinsTool is the pins MCP tool.
func pinsTool(args map[string]any) (mcpContent, error) {
	name := argString(args, "name")
	switch argString(args, "action") {
	case "show":
		p, err := daemon.LoadPin(name)
		if err != nil {
			return mcpContent{}, err
		}
		return mcpContent{text: pinRecipe(p)}, nil
	case "pin":
		if name == "" {
			return mcpContent{}, exit.New(exit.Validation, "pin needs a name")
		}
		ref := argString(args, "session")
		if ref == "" {
			ref = "current"
		}
		n, _ := strconv.Atoi(argString(args, "task"))
		p, _, err := pinTask(ref, name, n)
		if err != nil {
			return mcpContent{}, err
		}
		return mcpContent{text: pinRecipe(p)}, nil
	case "unpin":
		if err := daemon.Unpin(name); err != nil {
			return mcpContent{}, err
		}
		return mcpContent{text: "unpinned " + name}, nil
	case "run":
		if name == "" || argString(args, "tab") == "" {
			return mcpContent{}, exit.New(exit.Validation, "run needs a pin name and a tab")
		}
		values, err := runValues(args["values"])
		if err != nil {
			return mcpContent{}, err
		}
		body, _ := json.Marshal(values)
		return daemonCall(args, "run", map[string]string{"tab": argString(args, "tab"), "pin": name,
			"values": string(body), "from": argString(args, "from"), "timeout": orDefault(argString(args, "timeout"), "30s")})
	}
	ps, err := daemon.ListPins()
	if err != nil {
		return mcpContent{}, err
	}
	return mcpContent{text: pinList(ps)}, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
