package app

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

// bx mcp is a Model Context Protocol server on stdio for MCP-only clients
// such as Claude Desktop and Cursor. Every tool forwards to the same
// background daemon the CLI uses, so all clients share one browser
// connection and the browser asks to Allow once per launch.

const mcpProtocol = "2025-06-18"

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpTool is one tool: its schema, and how to run it.
type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	run         func(args map[string]any) (mcpContent, error)
}

// mcpContent is a tool result: text, or an image for screenshots.
type mcpContent struct {
	text  string
	image []byte
}

func cmdMCP(e env) error {
	return serveMCP(os.Stdin, os.Stdout)
}

func serveMCP(in io.Reader, out io.Writer) error {
	tools := mcpTools()
	byName := map[string]mcpTool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	var mu sync.Mutex
	enc := json.NewEncoder(out)
	send := func(m rpcMsg) {
		m.JSONRPC = "2.0"
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(m)
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var wg sync.WaitGroup
	for sc.Scan() {
		var m rpcMsg
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			send(rpcMsg{Error: &rpcError{-32700, "parse error"}})
			continue
		}
		if len(m.ID) == 0 {
			continue // a notification, such as notifications/initialized
		}
		// Tool calls can wait on the page for a while; answer each in its
		// own goroutine so pings and other calls are not held up.
		wg.Add(1)
		go func() {
			defer wg.Done()
			send(handleMCP(m, tools, byName))
		}()
	}
	wg.Wait()
	return sc.Err()
}

func handleMCP(m rpcMsg, tools []mcpTool, byName map[string]mcpTool) rpcMsg {
	reply := rpcMsg{ID: m.ID}
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		v := p.ProtocolVersion
		if v == "" {
			v = mcpProtocol
		}
		reply.Result = map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "bx", "version": Version},
			"instructions":    mcpInstructions,
		}
	case "ping":
		reply.Result = map[string]any{}
	case "tools/list":
		reply.Result = map[string]any{"tools": tools}
	case "tools/call":
		reply.Result = callTool(m.Params, byName)
	default:
		reply.Error = &rpcError{-32601, "method not found: " + m.Method}
	}
	return reply
}

const mcpInstructions = `bx drives the user's own running browser, in a named profile, with their logins. ` +
	`If a call fails, run doctor. If remote debugging is off, ask the user to turn it on at the page doctor names ` +
	`(such as brave://inspect/#remote-debugging), or, if they would rather you did it, to quit the browser so you can run setup. ` +
	`Start with list_profiles or list_tabs, then snapshot a tab: each element gets a ref like e12 that ` +
	`click, fill and the other actions accept. Ask the user before submitting forms, sending messages, ` +
	`buying or deleting. The first call after the browser starts makes it ask the user to Allow.`

func callTool(params json.RawMessage, byName map[string]mcpTool) map[string]any {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolError(exit.New(exit.Validation, "bad tools/call params"))
	}
	t, ok := byName[p.Name]
	if !ok {
		return toolError(exit.New(exit.Validation, "unknown tool %q", p.Name))
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}
	c, err := t.run(p.Arguments)
	if err != nil {
		return toolError(err)
	}
	if c.image != nil {
		return map[string]any{"content": []any{map[string]any{
			"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(c.image)}}}
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": c.text}}}
}

func toolError(err error) map[string]any {
	msg := fmt.Sprintf("%v (exit code %d)", err, exit.CodeOf(err))
	return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": msg}}}
}

// argString reads an argument as text; numbers and booleans are formatted.
func argString(args map[string]any, k string) string {
	switch v := args[k].(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// opTool builds a tool that forwards to a daemon op. fields maps tool
// argument names to the op's argument names.
func opTool(name, desc, op string, props map[string]any, required []string, fields map[string]string) mcpTool {
	return mcpTool{Name: name, Description: desc, InputSchema: schema(props, required),
		run: func(args map[string]any) (mcpContent, error) {
			req := map[string]string{"timeout": argString(args, "timeout")}
			for from, to := range fields {
				req[to] = argString(args, from)
			}
			if req["timeout"] == "" {
				req["timeout"] = "30s"
			}
			return daemonCall(args, op, req)
		}}
}

func daemonCall(args map[string]any, op string, req map[string]string) (mcpContent, error) {
	b, err := chooseBrowser(argString(args, "browser"))
	if err != nil {
		return mcpContent{}, err
	}
	var res json.RawMessage
	if err := daemon.Call(b.Name, daemon.Request{Op: op, Args: req}, &res, true); err != nil {
		return mcpContent{}, err
	}
	if len(res) == 0 || string(res) == "null" {
		return mcpContent{text: `{"ok":true}`}, nil
	}
	return mcpContent{text: string(res)}, nil
}

// schema builds a JSON Schema object; every tool also takes browser and
// timeout.
func schema(props map[string]any, required []string) map[string]any {
	all := map[string]any{
		"browser": str("Browser to drive, such as brave or chrome. Default: the user's default."),
		"timeout": str("How long to wait on the page, such as 10s. Default 30s."),
	}
	for k, v := range props {
		all[k] = v
	}
	s := map[string]any{"type": "object", "properties": all}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func boolean(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

const elDesc = "A ref from the tab's latest snapshot (such as e12) or a CSS selector."

var (
	tabArg = str("Tab ID from list_tabs or open.")
	elArg  = str(elDesc)
)

func mcpTools() []mcpTool {
	return append(append(setupTools(), readTools()...), actTools()...)
}

// readTools look at the browser and change nothing.
func readTools() []mcpTool {
	tab, el := tabArg, elArg
	return []mcpTool{
		{Name: "list_profiles", Description: "List the browser's profiles (display name and folder).",
			InputSchema: schema(nil, nil), run: listProfilesTool},
		opTool("list_tabs", "List open tabs, optionally only those of one profile.", "tabs",
			map[string]any{"profile": str("Profile name or folder, exact.")}, nil, map[string]string{"profile": "profile"}),
		opTool("open", "Open a URL in a new tab of the named profile; returns the tab.", "open",
			map[string]any{"url": str("http(s), file or about URL."), "profile": str("Profile name or folder, exact.")},
			[]string{"url", "profile"}, map[string]string{"url": "url", "profile": "profile"}),
		opTool("snapshot", "The tab's elements as an accessibility tree; each gets a ref like e12 for the actions. Prefer this to a screenshot.", "snapshot",
			map[string]any{"tab": tab}, []string{"tab"}, map[string]string{"tab": "tab"}),
		opTool("read", "Text or HTML of the page or one element.", "read",
			map[string]any{"tab": tab, "selector": str(elDesc + " Default: body."), "format": str("text (default) or html.")},
			[]string{"tab"}, map[string]string{"tab": "tab", "selector": "selector", "format": "format"}),
		{Name: "screenshot", Description: "PNG of the visible page, the full page, or one element.",
			InputSchema: schema(map[string]any{"tab": tab, "selector": el, "full": boolean("Capture the whole page.")}, []string{"tab"}),
			run:         screenshotTool},
		opTool("eval", "Run JavaScript in the page and return its JSON result. Takes an expression or a function such as () => document.title.", "eval",
			map[string]any{"tab": tab, "script": str("JavaScript expression or function.")}, []string{"tab", "script"},
			map[string]string{"tab": "tab", "script": "script"}),
		opTool("wait", "Wait until text appears, or an element is visible (or, with gone, is not).", "wait",
			map[string]any{"tab": tab, "text": str("Text to wait for."), "selector": el, "gone": boolean("Wait for it to disappear.")},
			[]string{"tab"}, map[string]string{"tab": "tab", "text": "text", "selector": "selector", "gone": "gone"}),
	}
}

// setupTools let an MCP-only user check and prepare the browser without
// the CLI. Each runs the CLI command of the same name.
func setupTools() []mcpTool {
	return []mcpTool{
		cliTool("doctor", "Check the browser: installed, profiles readable, remote debugging on (and the page to turn it on), connection, bx version. Run this first when something fails.",
			cmdDoctor, nil),
		cliTool("list_browsers", "Each supported browser: installed, running, remote debugging on, connected, and which is the default.",
			cmdListBrowsers, nil),
		cliTool("setup", "Turn the browser's remote-debugging switch on (or off with off=true). The user must quit the browser first; bx then starts it. Ask the user before running it.",
			cmdSetup, map[string]any{"off": boolean("Turn it off instead.")}),
		cliTool("launch", "Start a separate agent browser with its own profile folder: no setup and no Allow prompt, but its own logins. Then pass browser=<name>-agent. Ask the user first.",
			cmdLaunch, nil),
	}
}

// cliTool runs a CLI command with JSON output and returns that output.
func cliTool(name, desc string, command func(env) error, props map[string]any) mcpTool {
	return mcpTool{Name: name, Description: desc, InputSchema: schema(props, nil),
		run: func(args map[string]any) (mcpContent, error) {
			b, err := chooseBrowser(argString(args, "browser"))
			if err != nil {
				return mcpContent{}, err
			}
			var buf strings.Builder
			a := args2(args)
			e := env{a: a, o: out{json: true, w: &buf, errw: io.Discard}, b: b, mcp: true}
			runErr := command(e)
			// doctor reports failed checks as an error, but its output is the
			// answer the client needs.
			if buf.Len() > 0 {
				return mcpContent{text: buf.String()}, nil
			}
			if runErr != nil {
				return mcpContent{}, runErr
			}
			return mcpContent{text: `{"ok":true}`}, nil
		}}
}

// args2 turns tool arguments into CLI flags (booleans set when true).
func args2(in map[string]any) args {
	a := args{flags: map[string]string{}}
	for k, v := range in {
		if b, ok := v.(bool); ok {
			if b {
				a.flags[k] = "true"
			}
			continue
		}
		a.flags[k] = argString(in, k)
	}
	return a
}

// actTools change the page or the browser.
func actTools() []mcpTool {
	tab, el := tabArg, elArg
	return []mcpTool{
		opTool("goto", "Navigate the tab to a URL, or back, forward or reload.", "goto",
			map[string]any{"tab": tab, "url": str("A URL, or back, forward or reload.")}, []string{"tab", "url"},
			map[string]string{"tab": "tab", "url": "url"}),
		opTool("click", "Click an element. Reports navigation, new tabs and dialogs.", "click",
			map[string]any{"tab": tab, "element": el, "double": boolean("Double-click.")}, []string{"tab", "element"},
			map[string]string{"tab": "tab", "element": "selector", "double": "double"}),
		opTool("hover", "Move the mouse over an element.", "hover",
			map[string]any{"tab": tab, "element": el}, []string{"tab", "element"}, map[string]string{"tab": "tab", "element": "selector"}),
		opTool("drag", "Drag one element onto another.", "drag",
			map[string]any{"tab": tab, "from": el, "to": el}, []string{"tab", "from", "to"},
			map[string]string{"tab": "tab", "from": "from", "to": "to"}),
		{Name: "fill", Description: "Set form fields: text is typed in, a select takes an option's value or text, a checkbox or radio takes true or false.",
			InputSchema: schema(map[string]any{"tab": tab, "fields": map[string]any{
				"type": "array", "description": "Fields to set, in order.",
				"items": map[string]any{"type": "object", "required": []string{"element", "value"},
					"properties": map[string]any{"element": el, "value": str("The value.")}}}}, []string{"tab", "fields"}),
			run: fillTool},
		opTool("type", "Type text into whatever has focus, then optionally press a key such as Enter.", "type",
			map[string]any{"tab": tab, "text": str("Text to type."), "submit": str("Key to press after, such as Enter.")},
			[]string{"tab", "text"}, map[string]string{"tab": "tab", "text": "text", "submit": "submit"}),
		opTool("key", "Press a key or combination: Enter, Escape, Tab, ArrowDown, Meta+a.", "key",
			map[string]any{"tab": tab, "key": str("Key or combination.")}, []string{"tab", "key"},
			map[string]string{"tab": "tab", "key": "keys"}),
		{Name: "upload", Description: "Set the files of a file input.",
			InputSchema: schema(map[string]any{"tab": tab, "element": el, "files": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"}, "description": "Absolute file paths."}},
				[]string{"tab", "element", "files"}),
			run: uploadTool},
		opTool("dialog", "Accept or dismiss the alert, confirm or prompt open on the tab.", "dialog",
			map[string]any{"tab": tab, "action": map[string]any{"type": "string", "enum": []string{"accept", "dismiss"}},
				"text": str("Reply for a prompt.")}, []string{"tab", "action"},
			map[string]string{"tab": "tab", "action": "action", "text": "text"}),
		opTool("close_tab", "Close a tab. Close only tabs you opened.", "close",
			map[string]any{"tab": tab}, []string{"tab"}, map[string]string{"tab": "tab"}),
	}
}

func listProfilesTool(args map[string]any) (mcpContent, error) {
	b, err := chooseBrowser(argString(args, "browser"))
	if err != nil {
		return mcpContent{}, err
	}
	ps, err := b.Profiles()
	if err != nil {
		return mcpContent{}, err
	}
	out, _ := json.Marshal(map[string]any{"browser": b.Name, "profiles": ps})
	return mcpContent{text: string(out)}, nil
}

func screenshotTool(args map[string]any) (mcpContent, error) {
	b, err := chooseBrowser(argString(args, "browser"))
	if err != nil {
		return mcpContent{}, err
	}
	req := map[string]string{"tab": argString(args, "tab"), "selector": argString(args, "selector"),
		"full": argString(args, "full"), "timeout": argString(args, "timeout")}
	if req["timeout"] == "" {
		req["timeout"] = "30s"
	}
	var img daemon.Image
	if err := daemon.Call(b.Name, daemon.Request{Op: "screenshot", Args: req}, &img, true); err != nil {
		return mcpContent{}, err
	}
	return mcpContent{image: img.PNG}, nil
}

func fillTool(args map[string]any) (mcpContent, error) {
	raw, _ := json.Marshal(args["fields"])
	var fields []struct {
		Element string `json:"element"`
		Value   any    `json:"value"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) == 0 {
		return mcpContent{}, exit.New(exit.Validation, "fields must be a list of {element, value}")
	}
	pairs := make([][2]string, len(fields))
	for i, f := range fields {
		pairs[i] = [2]string{f.Element, argString(map[string]any{"v": f.Value}, "v")}
	}
	b, _ := json.Marshal(pairs)
	req := map[string]string{"tab": argString(args, "tab"), "pairs": string(b), "timeout": argString(args, "timeout")}
	if req["timeout"] == "" {
		req["timeout"] = "30s"
	}
	return daemonCall(args, "fill", req)
}

func uploadTool(args map[string]any) (mcpContent, error) {
	raw, _ := json.Marshal(args["files"])
	req := map[string]string{"tab": argString(args, "tab"), "selector": argString(args, "element"),
		"files": string(raw), "timeout": argString(args, "timeout")}
	if req["timeout"] == "" {
		req["timeout"] = "30s"
	}
	return daemonCall(args, "upload", req)
}
