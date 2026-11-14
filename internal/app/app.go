// Package app implements the bx command line.
package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/Facets-cloud/bx/internal/browser"
	"github.com/Facets-cloud/bx/internal/daemon"
	"github.com/Facets-cloud/bx/internal/exit"
)

// Version is the binary version, set by main from -ldflags.
var Version = "dev"

const usage = `bx — drive your running Chromium browser, one profile at a time

Setup:
  bx setup [--off]                  turn remote debugging on (or off) in the browser;
                                    quit the browser first
  bx launch                         start a separate agent browser: no setup, no Allow
                                    prompts, its own logins (then use --browser <name>-agent)
  bx mcp                            run as an MCP server on stdio (for Claude Desktop, Cursor)
  bx mcp install                    add bx to Claude Desktop's and Cursor's MCP config
  bx list browsers                  each supported browser: installed, running, debugging on
  bx use <browser>                  make it the default (see --browser below)
  bx doctor                         check the browser, permissions, daemon and skill
  bx skill install|update           install the agent skill for Claude Code and Codex
  bx update [--check]               upgrade bx and its skill from the latest release
                                    (installed skills also refresh on their own
                                    after an upgrade)
  bx version

Read:
  bx list profiles
  bx list tabs [--profile <name>]
  bx show tab <id>
  bx snapshot <tab> [--out <file>]  accessibility tree; each element gets a ref like e12
             [--interactive]        only what can be clicked or typed in, plus headings
             [--within <ref>]       only the part of the page under that ref
  bx read <tab> [--selector <el>] [--format text|html]
  bx screenshot <tab> [--selector <el>] [--full] [--out <file.png>|-]
  bx eval <tab> '<js>' | --file <script.js>
  bx wait <tab> --text <text> | --selector <el> [--gone]

Debug (recorded from the first time bx touches the tab; console messages
logged before that are included):
  bx console <tab> [--errors] [--since <id>] [--limit <n>]
                                    console messages, uncaught errors, failed loads
  bx network <tab> [--errors] [--since <id>] [--limit <n>]
                                    requests with status and timing (default: last 50)
  bx network <tab> --request <id>   one request's headers and bodies
  bx storage <tab>                  cookies, localStorage and sessionStorage
  bx performance <tab> [--reload]   Core Web Vitals, slow resources, long frames
  bx emulate <tab> [--viewport 390x844[@3][,mobile]] [--network offline|slow-3g|fast-3g]
                   [--cpu <slowdown>] [--geo <lat>,<lon>] [--color dark|light] [--reset]
                                    each also takes off; lasts until bx stop

Act (each waits for the page to settle and reports navigation, new tabs, dialogs):
  bx open <url> [--profile <name>]  default: the profile you last used; prints the
                                    tab and the page's interactive elements
  bx goto <tab> <url|back|forward|reload>
  bx click <tab> <el> [--double]
  bx hover <tab> <el>
  bx drag <tab> <from-el> <to-el>
  bx fill <tab> <el> <value> [<el> <value> ...]   text, select, checkbox (true|false)
  bx type <tab> <text> [--into <el>] [--submit <key>]  key by key, into <el> or what has focus
  bx key <tab> <key>                              Enter, Escape, Control+A, Meta+Shift+R
  bx batch <tab> '<json steps>' | --file <f>      several actions in one call, such as
                                                  [{"action":"fill","element":"e3","value":"hi"},
                                                   {"action":"key","key":"Enter"}]; stops at the
                                                  first failure
  bx upload <tab> <el> <file> [<file> ...]
  bx dialog <tab> accept|dismiss [--text <reply>]
  bx front <tab>
  bx close tab <id> --yes
  bx cdp <Method> [<json-params>] [--tab <id>]

An <el> is a snapshot ref (e12) or a CSS selector.

Record (a video of what bx does, pointer included; follows bx from tab to tab):
  bx record start [--out <dir>] [--blur <css>]
                                    start capturing the tab bx works in; --blur
                                    blurs elements matching the CSS selectors
  bx record stop [--out <file.mp4>] stop; makes the video with ffmpeg if installed

Session (one agent's task: the on-page pointer shows its title between calls):
  bx start "<title>" [--mode fast|watch] [--typing paste|keys]
                                    begin a task; $BX_SESSION names it (default: one CLI session)
  bx done ["<summary>"]             the agent finished its browser work: the on-page
                                    pointer shows Done and the summary, then goes away
  bx history [<session>|current|previous] [--limit <n>]
                                    what bx did: recent sessions, or one session
                                    from start to end (tasks, pages, actions)
  bx history pin <session> "<name>" [--task <n>]
                                    save a task (default: the session's last titled one)
                                    to do again later; pins are never deleted
  bx history pinned [<name>]        list pins, or show one as steps to follow again
  bx history unpin <name>
  bx config                         the log settings: log.values, log.days, log.max,
                                    log.archive_max, with their defaults
  bx config set <key> <value>       change one, such as bx config set log.days 365
  bx status                         show the background connection, if any
  bx stop                           close it (the browser asks to Allow again next time);
                                    for an agent browser, also quits it

Global flags:
  -o table|json     output format (table on a terminal, JSON otherwise)
  -q                print nothing on success for actions
  --yes             confirm a destructive action
  --browser <name>  browser to drive (%s, or <name>-agent
                    for the one bx launch starts); default: $BX_BROWSER, else
                    the one set by bx use, else the one with remote
                    debugging on, else brave
  --profile <name>  profile display name or folder name, exact match
  --timeout <dur>   how long to wait on the page (default 30s)

Exit codes: 0 success, 1 user error, 2 validation, 3 browser/upstream,
4 permission (Full Disk Access missing or Allow refused).
`

// env is what every command gets: parsed args, output, and the browser.
type env struct {
	a   args
	o   out
	b   browser.Browser
	pos []string // positional words after the command words
	mcp bool     // run on behalf of an MCP client (bx mcp)
}

// Run runs the CLI and returns the exit code.
func Run(argv []string) int {
	a, err := parseArgs(argv)
	o, oerr := newOut(a)
	if err == nil {
		err = oerr
	}
	if err != nil {
		return o.fail(err)
	}
	if len(a.pos) == 0 || a.has("help") {
		fmt.Printf(usage, joinNames())
		return 0
	}
	daemon.Version = Version
	b, err := chooseBrowser(a.flags["browser"])
	if err != nil {
		return o.fail(err)
	}
	switch a.pos[0] {
	case "version", "skill", "update", "__daemon":
		// These manage versions themselves, or must stay fast and silent.
	default:
		maybeRefreshSkill(os.Stderr)
		maybeNoticeUpdate(os.Stderr)
	}
	e := env{a: a, o: o, b: b}
	if err := e.dispatch(); err != nil {
		return o.fail(err)
	}
	return 0
}

func joinNames() string { return strings.Join(browser.Names(), ", ") }

// commands maps "verb" or "verb noun" to a handler.
var commands = map[string]func(e env) error{
	"version":       func(e env) error { fmt.Fprintln(e.o.w, Version); return nil },
	"__daemon":      func(e env) error { os.Exit(daemon.Serve(e.b.Name)); return nil },
	"doctor":        cmdDoctor,
	"skill install": cmdSkillInstall,
	"skill update":  cmdSkillInstall,
	"update":        cmdUpdate,
	"setup":         cmdSetup,
	"list browsers": cmdListBrowsers,
	"use":           cmdUse,
	"mcp":           cmdMCP,
	"mcp install":   cmdMCPInstall,
	"launch":        cmdLaunch,
	"list profiles": cmdListProfiles,
	"list tabs":     cmdListTabs,
	"show tab":      cmdShowTab,
	"read":          cmdRead,
	"screenshot":    cmdScreenshot,
	"open":          cmdOpen,
	"click":         cmdClick,
	"fill":          cmdFill,
	"close tab":     cmdCloseTab,
	"cdp":           cmdCDP,
	"snapshot":      cmdSnapshot,
	"eval":          cmdEval,
	"batch":         cmdBatch,
	"wait":          cmdWait,
	"console":       cmdConsole,
	"network":       cmdNetwork,
	"storage":       cmdStorage,
	"emulate":       cmdEmulate,
	"performance":   cmdPerformance,
	"goto":          cmdGoto,
	"hover":         cmdHover,
	"drag":          cmdDrag,
	"type":          cmdType,
	"key":           cmdKey,
	"upload":        cmdUpload,
	"dialog":        cmdDialog,
	"front":         cmdFront,
	"status":        cmdStatus,
	"done":          cmdDone,
	"history":       cmdHistory,
	"config":        cmdConfig,
	"config set":    cmdConfigSet,
	"start":         cmdStart,
	"record start":  cmdRecordStart,
	"record stop":   cmdRecordStop,
	"stop":          cmdStop,
}

// dispatch matches the longest command ("list tabs" before "list").
func (e env) dispatch() error {
	p := e.a.pos
	if len(p) >= 2 {
		if h, ok := commands[p[0]+" "+p[1]]; ok {
			e.pos = p[2:]
			return h(e)
		}
	}
	if h, ok := commands[p[0]]; ok {
		e.pos = p[1:]
		return h(e)
	}
	return exit.New(exit.Validation, "unknown command %q (see bx --help)", joinWords(p))
}

func joinWords(p []string) string {
	if len(p) > 2 {
		p = p[:2]
	}
	s := p[0]
	if len(p) == 2 {
		s += " " + p[1]
	}
	return s
}

// call sends one op to the daemon, starting it if needed.
func (e env) call(op string, args map[string]string, res any) error {
	return daemon.Call(e.b.Name, daemon.Request{Op: op, Args: args}, res, true)
}
