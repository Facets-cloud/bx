# bx

bx lets coding agents (Claude Code, Codex) drive the Chromium browser you
already have open, in the profile you name, over the DevTools Protocol. It
is one Go binary with no runtime dependencies and no telemetry.

**Platforms:** macOS only for now, on Apple Silicon and Intel. Linux and
Windows aren't supported yet. bx's lookup of browsers, profiles and
processes is Mac-specific. The browsers themselves offer the
same remote debugging on every OS, so ports are welcome.

<p align="center">
  <a href="docs/demo.mp4"><img src="docs/demo.gif" width="900" alt="Left, a terminal runs bx: it opens TodoMVC in the Personal profile, takes a snapshot that numbers the elements, fills the new-todo box by its ref, types two more todos, clicks a checkbox and the Active filter by their refs, and reads the remaining list. Right, the same browser tab follows each command."></a>
</p>
<p align="center"><sub>bx driving a real Brave tab: snapshot, refs, fill, type, click, read. <a href="docs/demo.mp4">Watch it in full quality</a>.</sub></p>

```sh
bx list profiles
id=$(bx open https://example.com --profile Work -o json | jq -r .id)
bx snapshot "$id" --interactive        # what can be clicked or typed in: e12 button "Save"
bx fill "$id" e4 'hello' e7 Green e9 true
bx click "$id" 'button "Save"'         # reports navigation, new tabs, dialogs, changes
bx batch "$id" --file steps.json       # many steps in one call
bx eval "$id" '() => document.title'
bx screenshot "$id" --out page.png
```

Run `bx --help` for every command. Every command takes `-o json`, never
prompts, and exits 0 (ok), 1 (user error), 2 (bad flags), 3 (browser
failed) or 4 (permission).

## How it works

The browser asks you to Allow each new DevTools connection. So the first
bx command starts a background process that holds one connection, and
every later command, from any terminal or agent session, goes through it
over a Unix socket in `~/Library/Application Support/bx/`. You click Allow
once per browser run. After an upgrade (`bx update`, or an npx MCP server
fetching a new release), the first command replaces the older background
process, so the browser asks to Allow once more. The process exits when the browser quits or on
`bx stop`. It never exits just for being idle.

The daemon also stays attached to every tab it touches. That keeps the
tab's focus emulated, so a background tab takes typing like the front one;
it lets an action that opens an alert or confirm stop and report it
(`bx dialog` answers it); and it keeps snapshot refs valid until the tab
navigates. After each action bx waits the way a person would: for a
navigation the action started to finish loading, then for the page to go
100ms without changing.

The DevTools Protocol can open a tab only in the profile the browser
treats as current (`Target.createTarget` fails for the others). bx uses it
there. For any other profile bx asks the browser to open the page with
`--profile-directory`, then finds the new tab. Tabs don't say which profile they belong to, so the first time bx
needs a profile's tabs it opens a `data:` page carrying a random nonce in
that profile, reads which context the page landed in, and closes it.

## Watch mode and fast mode

bx works in one of two modes. Both use the same browser, profiles and
logins, open new tabs in front, scroll what they act on into view, and
send real mouse and keyboard events, the kind a page can't tell from
yours. In both, a pointer with a "bx" tag shows where bx is acting. The
tag says what it is doing ("Click "Next"", "Paste 74 characters", "Read
the page", "Wait for "Saved"") and, in a batch, which step it is on
("7/32"). A click shows a ripple.

**Fast mode** is the default. The pointer jumps to each element and each
step runs as soon as the page settles.

**Watch mode** is for when you want to follow along: the pointer glides
along a curve and the real mouse travels with it, clicks land near the
middle rather than on it, a native dropdown is shown unfolding to the
choice, and batch steps pause briefly. A 32-step form took 8s in fast mode
and 50s in watch mode.

**Typing.** bx pastes text in one go by default. The page gets the same
events as a paste, which plain fields, rich text editors and
autocompletes all accept. Some sites react only to individual key
presses; for those bx can type key by key, at human pace in watch mode.

### Choosing: just ask your agent

Say it in the request: "let me watch", "go slowly so I can follow",
"this site ignores pasted text, type it". The agent passes `mode` and
`typing` to bx's `start` tool for that task, and can call `start` again
to change them halfway through. Nothing to configure.

### Sessions

Each agent's work is a session. The agent opens it with `start`, giving a
title such as "Onboard Lumen Analytics"; between its calls the pointer
shows "Working on: Onboard Lumen Analytics". It closes the session with
`done` and a one-line summary: the pointer shows "Done" and the summary,
then goes away, and `done` returns how long the task took, how many
actions it ran and the tabs it opened. Those tabs stay open unless the
agent closes them. A session can act on any tab in any profile. An agent
that skips `start` gets an untitled session on its first command; if one
never calls `done`, its pointer goes away 20 seconds after its last
command (2 minutes for a titled session), or when its MCP connection
ends. CLI agents use `bx start "<title>"` and `bx done "<summary>"`.

### History: what did bx do?

bx keeps a local log with one file per session, under
`~/Library/Application Support/bx/sessions/`. Inside Claude Code or Codex
the session is the app's own session, so one conversation is one file,
whether the agent used the MCP tools or the CLI. Each line has the time,
browser, profile, tab, the page it acted on and the page it led to, the
action and its target (`click button "Next"`), whether it worked, and how
long it took. Only you can read the files (mode 0600).

It leaves out what was typed and what a link carries. A fill shows the
field (`fill textbox "Password"`), never the value; typed text and `eval`
scripts show only their length. URLs are logged without their query
string, where sign-in codes, reset tokens and session ids travel, so the
log keeps `app.example.com/reset`, never what follows `?`. A `#` part
with `key=value` pairs (`#access_token=…`) is dropped too; one without
(`#/settings`, `#inbox`) is how some apps name the page, so it stays.
`bx config set log.values full` logs values and whole URLs, for
debugging, and `log.values off` turns logging off. These live only in
bx's config file, so an agent can't switch logging off for its own
commands.

Ask your agent "what did bx do in my last session?" and it calls the
`history` tool. At a terminal, `bx history` lists sessions, and `bx
history previous` (or `current`, or a session id) draws one from start to
end: its tasks, the pages each task worked on, and the actions on each.

```
bx-4c1e9a07d2f3  claude-code  bx  Nov 14 13:00:29-13:01:02  "Read example.com"  4 actions, 2 failed
resume: claude --resume 1b6e2c90-5a4d-…
log: ~/Library/Application Support/bx/sessions/bx-4c1e9a07d2f3.jsonl
├─ "Read example.com"  Nov 14 13:00:29-13:00:32  3 actions, 1 failed  done: "Followed the link"
│  ├─ Personal  example.com/
│  │  ├─ 13:00:29  open https://example.com  ok 413ms
│  │  └─ 13:00:29  click a  ok 364ms  -> iana.org/help/example-domains
│  └─ Personal  iana.org/help/example-domains
│     └─ 13:00:30  fill #nope  FAILED: no visible element matches "#nope"
└─ (untitled)  Nov 14 13:00:32-13:01:02  1 action
   └─ Personal  iana.org/help/example-domains
      └─ 13:00:32  goto back  ok 236ms  -> example.com/
```

`done` returns the session's log file too.

Elements are logged the way you'd name them, `button "Next"` or
`textbox "Email"`, not as snapshot refs like `e12`, which mean nothing
once the page is gone.

#### Pinned tasks: do it again

When a task is worth repeating (onboarding a vendor, filing a weekly
report), pin it. Tell your agent "pin that as Onboard vendor", or run:

```sh
bx history pin previous "Onboard vendor"   # the session's last titled task
bx history pin bx-4c1e9a07d2f3 "Weekly report" --task 2
bx history pinned                          # list pins
bx history pinned "Onboard vendor"         # its steps, page by page
bx history unpin "Onboard vendor"
```

Later, say "do the Onboard vendor pin for Lumen Analytics". The agent
reads the pin's steps with the `pins` tool, asks you for anything to type
or choose (values were never logged), then goes through the pages in
order, snapshotting each and running its steps. It follows the steps the
way a person follows notes, so a moved button doesn't break it. Pins are
copies kept in `~/Library/Application Support/bx/pins/`, so the log's
limits never remove them.

Session ids start with `bx-`. Inside Claude Code or Codex, bx derives the
id from the app's own session, so the MCP server and every CLI command in
one conversation share it, and records the app's session id beside it:
`bx history <id>` takes either, and the tree shows how to resume the
conversation. Elsewhere, each MCP connection gets a new random id.

How long logs are kept, with the defaults. Logs are kept until they get
big, not until they get old: one action is about 285 bytes, so 2 GB holds
about 7.5 million actions. An agent doing 2,000 actions a day writes about
210 MB a year, so the 2 GB of plain files lasts around ten years, and
gzipped sessions fit about 190 million more in 5 GB. An age limit would
only throw history away, so there is none by default; the size limits
keep a runaway agent from filling the disk.


| Setting | Default | What it does |
| --- | --- | --- |
| `log.max` | `2GB` | Plain session files may take this much; past it, the oldest are gzipped, a tenth of the size or less. |
| `log.archive_max` | `5GB` | Gzipped sessions may take this much; past it, the oldest are deleted. |
| `log.days` | `0` | Sessions older than this many days are deleted; `0` keeps them. For anyone who wants history to expire. |
| `log.values` | `on` | `on` leaves typed values and URL query strings out, `full` logs them, `off` logs nothing. |

All the limits apply at once, and whichever a session meets first acts on
it. `bx config` shows the settings and `bx config set log.days 365`
changes one. Agents can read them through the `settings` tool and raise
retention, but only you can lower logging: the tool answers with the
command for you to run. bx checks the limits when its
background process starts and every hour while it runs.

`history` reads archived sessions as they are, and a small index of
per-file summaries keeps it fast: with 800,000 logged actions, listing
sessions or opening one takes about 10 ms.

Each session file is JSON lines: a header line
(`{"v":1,"type":"session","session":…,"harness":…,"harness_session":…,"project":…,"started":…}`),
then one line per action (`t`, `session`, `title`, `browser`, `profile`,
`tab`, `url`, `to`, `op`, `target`, `error`, `ms`). To send the logs to
Loki, Splunk, Datadog or a SIEM, point a log shipper such as Fluent Bit,
Vector or the OpenTelemetry Collector at the sessions folder.

### Defaults, for power users

To change what bx does when the agent doesn't say, set environment
variables where the agent starts bx. A request's `start` settings still
win.

| Variable | Values | Default |
| --- | --- | --- |
| `BX_MODE` | `fast`, `watch` | `fast` |
| `BX_TYPING` | `paste`, `keys` | `paste` |
| `BX_BROWSER` | `brave`, `chrome`, `brave-agent`, ... | see `bx use` |

Claude Code: `claude mcp add -s user bx -e BX_MODE=watch -- npx -y @facets-cloud/bx mcp`
(run `claude mcp remove bx -s user` first to change an existing install).
Codex, in `~/.codex/config.toml`:

```toml
[mcp_servers.bx]
command = "npx"
args = ["-y", "@facets-cloud/bx", "mcp"]
env = { BX_MODE = "watch" }
```

Claude Desktop, Cursor and other MCP apps take an `env` object in the
server entry. The CLI reads your shell: `export BX_MODE=watch`. Restart
the agent app after changing its config.

## Compared with Playwright MCP and Chrome DevTools MCP

On a 6-step vendor onboarding form, the same agent needed a median of 24
tool calls with bx against 62 with Playwright MCP, because `batch` fills a
whole step in one call. bx's MCP server uses about 29 MB of memory (about
95 MB started through npx), against 251 MB and 290 MB. It's a small
benchmark we ran ourselves, on pages we wrote, after tuning bx; the
method, every run, where bx lost and the caveats are in
[docs/benchmark.md](docs/benchmark.md).

## Fewer calls, faster runs

Most of an agent's time goes to its own turns: reading the page and
deciding what to do. bx is built to need fewer of them.

- `snapshot --interactive` lists only what can be clicked or typed in,
  plus headings. On a Wikipedia article that is 29 KB instead of 155 KB.
  `--within e12` shows one part of the page.
- An element can be a ref (`e12`), a role and name the way a snapshot
  shows it (`button "Next"`, `textbox "Email" #2` for the second), or a
  CSS selector. A role and name waits up to 10s for the element to
  appear. An agent can therefore plan screens it hasn't seen yet.
- `batch` runs steps in one call (`click`, `fill`, `type`, `key`, `hover`,
  `goto`, `wait`, `upload`, `scroll`). It stops at the first failure and
  says which step failed. A six-step signup wizard ran as one 32-step
  batch.
- Every action reports `changes`: what appeared (`+ e61 button "Next"`),
  disappeared (`- e40`) or changed (`~ e27 combobox value="India"`). After a
  big change or a new page it returns the new screen instead. Refs stay
  the same while an element stays on the page, so a new snapshot is rarely
  needed.
- Elements inside shadow roots (web components, many post composers) work
  like any other.

## Install (macOS)

### As a plugin (Claude Code or Codex)

The plugin brings the MCP server and the skill, and updates through the
plugin manager:

```sh
claude plugin marketplace add Facets-cloud/bx && claude plugin install bx@bx
codex plugin marketplace add Facets-cloud/bx && codex plugin add bx@bx
```

Or, inside Claude Code: `/plugin install bx --marketplace Facets-cloud/bx`.
Restart the agent, then ask it to run bx's `doctor` tool. If you added bx
earlier with `claude mcp add`, remove that copy (`claude mcp remove -s
user bx`) so the agent doesn't see two. With the plugin installed, `bx
skill install` leaves Claude Code alone.

### With the binary

In any Claude Code or Codex session, paste this:

> Install bx from https://github.com/Facets-cloud/bx

Your agent follows the steps below, installs the bx skill for every agent
harness it finds, and tells you the two things only you can do in the
browser and in System Settings.

### Steps for the agent

1. Download the latest release for this Mac (`uname -m` gives `arm64` or
   `x86_64`; the asset is `bx_darwin_arm64.tar.gz` or
   `bx_darwin_amd64.tar.gz`), and check it against the release's
   `checksums.txt` with `shasum -a 256 -c --ignore-missing checksums.txt`.
   Both files are at
   `https://github.com/Facets-cloud/bx/releases/latest/download/<file>`.
2. Unpack it and put `bx` on the PATH, in `~/.local/bin` if that is on the
   PATH, else `/usr/local/bin`. Remove macOS's quarantine flag:
   `xattr -d com.apple.quarantine <path>/bx 2>/dev/null || true`.
3. Run `bx skill install`. It installs the skill for Claude Code and Codex.
   If the user asked for bx as an MCP server, run `bx mcp install` instead
   (or as well), and tell them to restart Claude Desktop or Cursor.
4. On macOS 27 and later, ask the user to give the terminal app **Full
   Disk Access** (System Settings > Privacy & Security). macOS blocks
   other apps from browser data folders, where the browser writes its
   debugging port and profile names. Without it, bx exits with code 4.
5. Run `bx setup`. It turns on the browser's remote-debugging switch, a
   one-time setting; the browser must be quit first, and bx starts it again.
6. Run `bx doctor -o json` and tell the user what still fails, and that the
   first bx command makes the browser ask to Allow, once per browser run.

### Updates

`bx update` installs the latest release (after checking its SHA-256
against the release's `checksums.txt`) and reinstalls the skill;
`bx update --check` only reports. After any upgrade, the first bx command
refreshes the skill wherever it is installed, so agents never run with an
old skill. At a terminal, bx mentions a newer release at most once a day;
`bx doctor` reports it too.

<details>
<summary>Manual install</summary>

```sh
ARCH=arm64   # Apple Silicon; amd64 for Intel
BASE=https://github.com/Facets-cloud/bx/releases/latest/download
curl -fsSLO "$BASE/bx_darwin_${ARCH}.tar.gz"
curl -fsSLO "$BASE/checksums.txt"
shasum -a 256 -c --ignore-missing checksums.txt
tar -xzf "bx_darwin_${ARCH}.tar.gz" bx
mkdir -p ~/.local/bin && mv bx ~/.local/bin/
xattr -d com.apple.quarantine ~/.local/bin/bx 2>/dev/null || true
bx skill install
bx setup     # quit the browser first; turns on remote debugging once
bx doctor
```

Then give your terminal app Full Disk Access, quit the browser, and run
`bx setup`.

</details>

### Use it as an MCP server

bx is also a Model Context Protocol server, and MCP can be the only way you
use it. `bx mcp` serves 34 tools on stdio: setup (`doctor`,
`list_browsers`, `setup`, `launch`, `settings`), reading (`list_profiles`,
`list_tabs`, `snapshot`, `read`, `screenshot`, `eval`, `wait`, `history`, `pins`), acting
(`open`, `goto`, `click`, `hover`, `drag`, `fill`, `type`, `key`,
`upload`, `dialog`, `close_tab`, `batch`, `start`, `done`) and debugging (`console`, `network`,
`storage`, `performance`, `emulate`, `cdp`). They forward to the same background
connection as the CLI, so every client shares one Allow per browser launch.

MCP-only install, with npm (no download step; npx fetches the latest):

```sh
claude mcp add -s user bx -- npx -y @facets-cloud/bx mcp
codex mcp add bx -- npx -y @facets-cloud/bx mcp
```

For Claude Desktop, Cursor and other apps, add this server to their MCP
config, or run `npx -y @facets-cloud/bx mcp install` to do it for you:

```json
{ "mcpServers": { "bx": { "command": "npx", "args": ["-y", "@facets-cloud/bx", "mcp"] } } }
```

Always use the full name `@facets-cloud/bx`: the unscoped npm package `bx`
is an unrelated WebdriverIO tool.

Without npm: put the `bx` binary somewhere permanent (steps 1 and 2 above,
or the manual install below), then run `bx mcp install`.

After installing, restart the app and:
- ask it to run the `doctor` tool;
- if remote debugging is off, quit the browser and ask it to run `setup`.

Or paste this into any agent: *Install bx from
https://github.com/Facets-cloud/bx as an MCP server.*

To pin a browser, give the server an environment variable, for example
`"env": {"BX_BROWSER": "chrome"}`, or pass `browser` in a tool call. The
same `env` takes `BX_MODE` and `BX_TYPING` (see
[Watch mode and fast mode](#watch-mode-and-fast-mode)).

Any other client: a stdio server with command `/full/path/to/bx` and args
`["mcp"]` (desktop apps don't see your shell's PATH).

### No prompts at all: an agent browser

`bx launch` starts a separate instance of the browser for agents, with its
own profile folder under `~/Library/Application Support/bx/`. It needs no
setup and never shows the Allow dialog. Its logins are separate from your
everyday profiles: sign in there to what agents need. Drive it with
`--browser brave-agent` (or `export BX_BROWSER=brave-agent`); `bx stop
--browser brave-agent` quits it. Without the Allow dialog, any program on
your Mac can drive it, so keep its logins to what agents need.

Supported: Brave and Chrome (tested), Chromium, Edge, Arc and Vivaldi
(same layout, not yet tested). `bx list browsers` shows which are
installed, running and have debugging on; `bx use chrome` makes one the
default. bx drives `--browser <name>`, else `$BX_BROWSER`, else the
`bx use` default, else the first with remote debugging on. Adding a Chromium browser means adding
one entry to the table in `internal/browser/browser.go`.

## Security

bx gives agents your logged-in browser. [SECURITY.md](SECURITY.md) says
what that allows, what limits it, and what it doesn't protect against.

## License

MIT. See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).

## Credits

bx's interaction design follows chrome-devtools-mcp (Apache-2.0); bx
reimplements it in Go. See THIRD_PARTY_NOTICES.

## Develop

npm packages: `scripts/npm-build.sh vX.Y.Z` builds `npm/` for a version;
the release workflow publishes them with the org's `NPM_TOKEN` (the same
way facetsctl publishes `@facets-cloud/facetsctlv3`).


```sh
go test ./...
go build -o bin/bx .
```

The tests need no browser. Changing daemon code means `bx stop` and a new
Allow click, because the running daemon is the old binary.
