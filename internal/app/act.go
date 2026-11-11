package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

// actCall sends an action op with the timeout and prints its ActResult.
func actCall(e env, op string, args map[string]string, msg string) error {
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	args["timeout"] = t
	var r daemon.ActResult
	if err := e.call(op, args, &r); err != nil {
		return err
	}
	printAct(e.o, r, msg)
	return nil
}

// printAct says what the action did and what changed after it.
func printAct(o out, r daemon.ActResult, msg string) {
	switch {
	case o.quiet:
	case o.json:
		o.printJSON(r)
	default:
		fmt.Fprintln(o.w, msg)
		if r.Dialog != nil {
			fmt.Fprintf(o.w, "a %s dialog opened: %q (answer it with bx dialog <tab> accept|dismiss)\n", r.Dialog.Type, r.Dialog.Message)
			return
		}
		if r.Navigated {
			fmt.Fprintf(o.w, "navigated to %s (%s)\n", r.URL, r.Title)
		}
		for _, t := range r.NewTabs {
			fmt.Fprintln(o.w, "opened tab", t)
		}
	}
}

func cmdClick(e env) error {
	if err := need(e.pos, 2, "bx click <tab> <element> [--double]"); err != nil {
		return err
	}
	args := map[string]string{"tab": e.pos[0], "selector": e.pos[1]}
	if e.a.has("double") {
		args["double"] = "true"
	}
	return actCall(e, "click", args, "clicked "+e.pos[1])
}

func cmdHover(e env) error {
	if err := need(e.pos, 2, "bx hover <tab> <element>"); err != nil {
		return err
	}
	return actCall(e, "hover", map[string]string{"tab": e.pos[0], "selector": e.pos[1]}, "hovering over "+e.pos[1])
}

func cmdDrag(e env) error {
	if err := need(e.pos, 3, "bx drag <tab> <from> <to>"); err != nil {
		return err
	}
	return actCall(e, "drag", map[string]string{"tab": e.pos[0], "from": e.pos[1], "to": e.pos[2]}, "dragged "+e.pos[1]+" to "+e.pos[2])
}

func cmdFill(e env) error {
	usage := "bx fill <tab> <element> <value> [<element> <value> ...]"
	if len(e.pos) < 3 || len(e.pos)%2 != 1 {
		return exit.New(exit.Validation, "usage: %s", usage)
	}
	var pairs [][2]string
	for i := 1; i < len(e.pos); i += 2 {
		pairs = append(pairs, [2]string{e.pos[i], e.pos[i+1]})
	}
	b, _ := json.Marshal(pairs)
	return actCall(e, "fill", map[string]string{"tab": e.pos[0], "pairs": string(b)}, fmt.Sprintf("filled %d field(s)", len(pairs)))
}

func cmdType(e env) error {
	if err := need(e.pos, 2, "bx type <tab> <text> [--submit <key>]"); err != nil {
		return err
	}
	return actCall(e, "type", map[string]string{"tab": e.pos[0], "text": e.pos[1], "submit": e.a.flags["submit"]}, "typed")
}

func cmdKey(e env) error {
	if err := need(e.pos, 2, "bx key <tab> <key> (such as Enter, Escape, Control+A)"); err != nil {
		return err
	}
	return actCall(e, "key", map[string]string{"tab": e.pos[0], "keys": e.pos[1]}, "pressed "+e.pos[1])
}

func cmdUpload(e env) error {
	if len(e.pos) < 3 {
		return exit.New(exit.Validation, "usage: bx upload <tab> <element> <file> [<file> ...]")
	}
	b, _ := json.Marshal(e.pos[2:])
	return actCall(e, "upload", map[string]string{"tab": e.pos[0], "selector": e.pos[1], "files": string(b)}, "uploaded")
}

func cmdGoto(e env) error {
	if err := need(e.pos, 2, "bx goto <tab> <url|back|forward|reload>"); err != nil {
		return err
	}
	return actCall(e, "goto", map[string]string{"tab": e.pos[0], "url": e.pos[1]}, "went to "+e.pos[1])
}

func cmdDialog(e env) error {
	if err := need(e.pos, 2, "bx dialog <tab> accept|dismiss [--text <reply>]"); err != nil {
		return err
	}
	if e.pos[1] != "accept" && e.pos[1] != "dismiss" {
		return exit.New(exit.Validation, "the dialog answer must be accept or dismiss")
	}
	var d daemon.Dialog
	if err := e.call("dialog", map[string]string{"tab": e.pos[0], "action": e.pos[1], "text": e.a.flags["text"]}, &d); err != nil {
		return err
	}
	switch {
	case e.o.quiet:
	case e.o.json:
		e.o.printJSON(map[string]any{"answered": e.pos[1], "dialog": d})
	default:
		fmt.Fprintf(e.o.w, "%sed the %s dialog %q\n", strings.TrimSuffix(e.pos[1], "e"), d.Type, d.Message)
	}
	return nil
}

func cmdFront(e env) error {
	if err := need(e.pos, 1, "bx front <tab>"); err != nil {
		return err
	}
	if err := e.call("front", map[string]string{"tab": e.pos[0]}, nil); err != nil {
		return err
	}
	e.o.done("brought " + e.pos[0] + " to the front")
	return nil
}

func cmdSnapshot(e env) error {
	if err := need(e.pos, 1, "bx snapshot <tab> [--out <file>]"); err != nil {
		return err
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	var s daemon.Snapshot
	if err := e.call("snapshot", map[string]string{"tab": e.pos[0], "timeout": t}, &s); err != nil {
		return err
	}
	text := s.Text()
	if path := e.a.flags["out"]; path != "" {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return exit.New(exit.User, "write %s: %v", path, err)
		}
		switch {
		case e.o.quiet:
		case e.o.json:
			e.o.printJSON(map[string]any{"path": path, "nodes": len(s.Nodes)})
		default:
			fmt.Fprintf(e.o.w, "%s (%d elements)\n", path, len(s.Nodes))
		}
		return nil
	}
	if e.o.json {
		e.o.printJSON(s)
	} else {
		fmt.Fprint(e.o.w, text)
	}
	return nil
}

func cmdEval(e env) error {
	src, err := evalScript(e)
	if err != nil {
		return err
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	var res json.RawMessage
	if err := e.call("eval", map[string]string{"tab": e.pos[0], "script": src, "timeout": t}, &res); err != nil {
		return err
	}
	e.o.printJSON(res)
	return nil
}

// evalScript takes the script from the argument or from --file.
func evalScript(e env) (string, error) {
	usage := "usage: bx eval <tab> '<js expression or function>' | bx eval <tab> --file <script.js>"
	file := e.a.flags["file"]
	switch {
	case file == "" && len(e.pos) == 2:
		return e.pos[1], nil
	case file != "" && len(e.pos) == 1:
		b, err := os.ReadFile(file)
		if err != nil {
			return "", exit.New(exit.User, "read %s: %v", file, err)
		}
		return string(b), nil
	}
	return "", exit.New(exit.Validation, "%s", usage)
}

func cmdWait(e env) error {
	if err := need(e.pos, 1, "bx wait <tab> --text <text> | --selector <element> [--gone]"); err != nil {
		return err
	}
	t, err := e.a.timeout()
	if err != nil {
		return err
	}
	args := map[string]string{"tab": e.pos[0], "text": e.a.flags["text"], "selector": e.a.flags["selector"], "timeout": t}
	if e.a.has("gone") {
		args["gone"] = "true"
	}
	if err := e.call("wait", args, nil); err != nil {
		return err
	}
	e.o.done("condition met")
	return nil
}
