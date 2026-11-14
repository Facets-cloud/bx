# bx · browser exec

[![scuffmark](https://scuffmark.facets.cloud/badge/bx.svg)](https://scuffmark.facets.cloud/c/bx)

Like `docker exec`, but the container is your browser. bx lets coding
agents (Claude Code, Codex, Cursor, Claude Desktop) run their steps inside
the Chromium browser you already have open, in the profile you name, with
your logins. No separate test browser and no copied cookies. It can click
through a page and read its console, network and storage, like you would
in DevTools.

See it run: **https://facets-cloud.github.io/bx/** has the demo video and
six real agent runs.

bx is one Go binary with no runtime dependencies. It is a CLI and an MCP
server (JSON-RPC 2.0 on stdio), talks to the browser over the DevTools
Protocol, and has no telemetry.

**Platforms:** macOS, on Apple silicon and Intel. bx's lookup of browsers,
profiles and processes is Mac-specific; the browsers offer the same remote
debugging on every OS, so Linux and Windows ports are welcome.

```
> Place order does nothing on staging. Reproduce it and find the root cause.

start     "Debug Place order on staging"
open      127.0.0.1:8765  profile=Personal    "Checkout · Acme Store"
snapshot  e25 button "Place order"
click     e25                                  navigated: false
console   TypeError: Cannot read properties of undefined (reading 'toFixed')
network   POST /api/checkout 500
network   request=n2                           {"error": "coupon SAVE10 expired"}
done      "Reproduced Place order failure; found root cause"
```

## How it works

The agent snapshots a page and gets one line per thing it can click or type
in, each with a ref:

```
e4 textbox "Email" = "ana@example.com"
e12 button "Next"
e27 combobox "Country" value="India"
```

It acts by ref (`click e12`), by role and name (`button "Next"`, or
`textbox "Email" #2` for the second), or by CSS selector. A role and name
waits up to 10 s for the element to appear, so a `batch` can plan screens it
hasn't seen yet. After each action bx waits the way a person would, for a
navigation the action started to finish and then for the page to go 100 ms
without changing, and returns only what changed: `+` added, `-` removed,
`~` changed. After a new page it returns the new screen instead. Refs stay
the same while an element stays on the page, so the agent rarely needs a
second snapshot.

Measured:

- `snapshot --interactive` of a Wikipedia article: 29 KB instead of 155 KB
  for the full tree. `--within e12` shows one part of the page.
- A six-step signup wizard ran as one 32-step `batch`: 8 s in fast mode.
- The MCP server uses about 29 MB of memory (about 95 MB started through
  npx).

### One Allow per browser run

The browser asks you to Allow each new DevTools connection. So the first bx
command starts a background process that holds one connection, and every
later command, from any terminal or agent session, goes through it over a
Unix socket in `~/Library/Application Support/bx/`. You click Allow once per
browser run, and once more after an upgrade replaces the background process.
It exits when the browser quits or on `bx stop`, never just for being idle.

The process stays attached to every tab it touches. That keeps the tab's
focus emulated, so a background tab takes typing like the front one; it
lets an action that opens an alert or confirm stop and report it (`dialog`
answers it); and it keeps refs valid until the tab navigates.

The DevTools Protocol opens tabs only in the profile the browser treats as
current. For any other profile bx asks the browser to open the page with
`--profile-directory` and finds the new tab. To learn which tabs belong to
which profile, it opens a `data:` page carrying a random nonce in that
profile once, reads which context it landed in, and closes it.

## Modes

You choose per task, in plain words. The agent passes your choice to bx's
`start` tool, and can call `start` again to change it halfway through.

| You say | Mode | What happens |
| --- | --- | --- |
| nothing | `fast` (default) | The pointer jumps to each element and each step runs as soon as the page settles. |
| "let me watch", "go slowly" | `watch` | The pointer glides along a curve with the real mouse, clicks land near the middle rather than on it, a native dropdown unfolds to the choice, and batch steps pause briefly. |

A 32-step form took 8 s in fast mode and 50 s in watch mode. Both modes open
new tabs in front, scroll what they act on into view, and send real mouse
and keyboard events, the kind a page can't tell from yours.

`typing` is `paste` (default: the text goes in at once, as a paste, which
plain fields, rich text editors and autocompletes accept) or `keys` (typed
key by key, for sites that react only to key presses). If a page ignores
pasted text, bx types key by key on its own.

### The pointer

While a task runs, a pointer with a "bx" tag shows where bx is acting. The
tag says what it is doing (`Click "Next"`, `Paste 74 characters`,
`Read the page`) and, in a batch, which step it is on (`7/32`). A click
shows a ripple. Between calls it shows `Working on: <title>`. After `done`
it shows "Done" and the summary, then goes away. If an agent never calls
`done`, the pointer goes away 20 seconds after its last command (2 minutes
for a titled task), or when its MCP connection ends.

## Install

**Claude Code or Codex plugin** (the server plus the bx skill, which
teaches the agent the workflow and the safety rules, updated by the plugin
manager):

```sh
claude plugin marketplace add Facets-cloud/bx && claude plugin install bx@bx
codex plugin marketplace add Facets-cloud/bx && codex plugin add bx@bx
```

**Claude Code or Codex, server only** (needs Node 18 or later for `npx`):

```sh
claude mcp add -s user bx -- npx -y @facets-cloud/bx mcp
codex mcp add bx -- npx -y @facets-cloud/bx mcp
```

**Cursor, Claude Desktop and other MCP apps**, in their MCP config, or run
`npx -y @facets-cloud/bx mcp install` to add it for you:

```json
{ "mcpServers": { "bx": { "command": "npx", "args": ["-y", "@facets-cloud/bx", "mcp"] } } }
```

Restart the app after adding it, then ask it to run bx's `doctor` tool. If
you move from `claude mcp add` to the plugin, remove the old copy
(`claude mcp remove -s user bx`) so the agent doesn't see two.

Always use the scoped name `@facets-cloud/bx`: the unscoped npm package `bx`
is an unrelated WebdriverIO tool.

### Turn on remote debugging

Once per browser, turn on its remote-debugging switch. `doctor` names the
page (such as `brave://inspect/#remote-debugging`), or quit the browser and
let the agent run the `setup` tool, which turns it on and starts the browser
again. The first bx call after that makes the browser ask you to Allow.

On macOS 27 and later, give the app that runs bx (your terminal, editor or
agent app) **Full Disk Access** in System Settings > Privacy & Security.
macOS blocks other apps from browser data folders, where the browser writes
its debugging port and profile names. Without it, bx exits with code 4.

### The binary and the CLI

In any Claude Code or Codex session, paste this:

> Install bx from https://github.com/Facets-cloud/bx

Your agent follows the steps below, installs the bx skill for every agent
it finds, and tells you the two things only you can do.

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
   Disk Access** (see above).
5. Run `bx setup`. It turns on the browser's remote-debugging switch; the
   browser must be quit first, and bx starts it again.
6. Run `bx doctor -o json` and tell the user what still fails, and that the
   first bx command makes the browser ask to Allow, once per browser run.

The CLI does everything the MCP tools do:

```sh
bx list profiles
id=$(bx open https://example.com --profile Work -o json | jq -r .id)
bx snapshot "$id" --interactive        # e12 button "Save"
bx fill "$id" e4 'hello' e7 Green e9 true
bx click "$id" 'button "Save"'         # reports navigation, new tabs, dialogs, changes
bx batch "$id" --file steps.json       # many steps in one call
bx eval "$id" '() => document.title'
bx screenshot "$id" --out page.png
```

Every command takes `-o json`, never prompts, and exits 0 (ok), 1 (user
error), 2 (bad flags), 3 (browser failed) or 4 (permission). `bx --help`
lists them all.

`bx update` installs the latest release after checking its SHA-256 against
`checksums.txt`, and reinstalls the skill; `bx update --check` only
reports. At a terminal, bx mentions a newer release at most once a day.

### Defaults

Environment variables where the agent starts bx set what it does when the
agent doesn't say. What the agent passes to `start` wins.

| Variable | Values | Default |
| --- | --- | --- |
| `BX_MODE` | `fast`, `watch` | `fast` |
| `BX_TYPING` | `paste`, `keys` | `paste` |
| `BX_BROWSER` | `brave`, `chrome`, `brave-agent`, ... | see `bx use` |

With the plugin, `export` them in your shell profile and restart the app.
With `claude mcp add`, pass `-e BX_MODE=watch`. Codex without the plugin
takes `env = { BX_MODE = "watch" }` under `[mcp_servers.bx]` in
`~/.codex/config.toml`, and other MCP apps take an `env` object in the
server entry.

## Tools

34 tools.

| Group | Tools |
| --- | --- |
| Session | `start` (title, mode, typing), `done` (summary; returns time taken, action count and the tabs it opened) |
| Setup | `doctor`, `list_browsers`, `setup`, `launch`, `settings` |
| Read | `list_profiles`, `list_tabs`, `snapshot`, `read`, `screenshot`, `eval`, `wait` |
| Act | `open`, `goto`, `click`, `hover`, `drag`, `fill`, `type`, `key`, `upload`, `dialog`, `close_tab`, `batch` |
| Debug | `console`, `network`, `storage`, `performance`, `emulate`, `cdp` |
| Audit log | `history`, `pins` |

- `open` takes a URL and an optional profile (default: the one you last
  used), and returns the page's elements, so the agent can act at once.
- `batch` runs `click`, `fill`, `type`, `key`, `hover`, `goto`, `wait`,
  `upload` and `scroll` steps in one call. It stops at the first failure and
  says which step failed.
- `console` and `network` take `since` (the last id seen), so an agent sees
  only what an action caused; `network` with `request` returns that
  request's headers and bodies.
- `emulate` sets the viewport, a slow network or CPU, a location or a color
  scheme. `cdp` sends any raw DevTools Protocol command.
- Elements inside shadow roots (web components, many post composers) work
  like any other.

## What it can reach

- **Browsers:** Brave and Chrome (tested), Chromium, Edge, Arc and Vivaldi
  (same layout, not yet tested). `bx list browsers` shows which are
  installed, running and have debugging on; `bx use chrome` makes one the
  default. Adding a Chromium browser is one entry in
  `internal/browser/browser.go`.
- **Profiles:** every profile of that browser, by name, with its own
  logins. A task can act on any tab in any profile.
- **An agent browser:** `bx launch` starts a separate instance with its own
  profile folder under `~/Library/Application Support/bx/`. It needs no
  setup and never shows the Allow dialog. Sign in there only to what agents
  need, and drive it with `--browser brave-agent`.

## Safety

bx acts as you, with your logins and cookies, and can do anything you can
do in the browser. [SECURITY.md](SECURITY.md) explains the threat model.
What limits it:

- **The browser asks first.** Each new DevTools connection shows an Allow
  dialog. The agent browser is the exception, and it is opt-in.
- **Local only.** bx listens on no network port. Its background process
  takes commands over a Unix socket in a folder only your account can open.
- **The skill's rules.** The agent is told to ask you before submitting
  forms, sending messages, buying or deleting, and before accepting a
  confirm dialog that deletes or sends. bx itself doesn't block those
  clicks, so the rule depends on the agent following it.
- **Fence it with a profile.** Point agents at a profile that holds only the
  logins the task needs.
- **Page text is data.** A page can try to tell an agent to act in another
  tab (prompt injection). Give agents narrow tasks.

## Audit log

bx writes one JSON line per tool call to
`~/Library/Application Support/bx/sessions/<session>.jsonl` (mode 0600). Inside
Claude Code or Codex, a session is the app's own conversation, whether the
agent used the MCP tools or the CLI. Each line names the browser, profile,
tab, the page it acted on and the page it led to, the element (role and
name, such as `button "Next"`), whether it worked and how long it took.

It leaves out what was typed and what a link carries. A fill shows the
field, never the value; typed text and `eval` scripts appear only as their
length. URLs lose their query string, where sign-in codes and reset tokens
travel, and a `#` part with `key=value` pairs.

```sh
bx history                        # recent sessions
bx history previous               # one session as a tree: tasks, pages, actions
bx history pin previous "Onboard vendor"   # name a finished task so an agent can repeat it
bx history pinned                 # list pins; add a name to see its steps
bx config                         # log settings
bx config set log.values full     # also log typed values and whole URLs
```

```
bx-4c1e9a07d2f3  claude-code  bx  Nov 14 13:00:29-13:01:02  "Read example.com"  4 actions, 2 failed
resume: claude --resume 1b6e2c90-5a4d-…
├─ "Read example.com"  Nov 14 13:00:29-13:00:32  3 actions, 1 failed  done: "Followed the link"
│  ├─ Personal  example.com/
│  │  ├─ 13:00:29  open https://example.com  ok 413ms
│  │  └─ 13:00:29  click a  ok 364ms  -> iana.org/help/example-domains
│  └─ Personal  iana.org/help/example-domains
│     └─ 13:00:30  fill #nope  FAILED: no visible element matches "#nope"
└─ (untitled)  Nov 14 13:00:32-13:01:02  1 action
```

Logs are kept forever by default. One action is about 285 bytes, so 2 GB
holds about 7.5 million actions; an age limit would only throw history
away. Past 2 GB the oldest idle files are gzipped, and past 5 GB of archives
the oldest are deleted (`log.max`, `log.archive_max`, `log.days`). With
800,000 logged actions, listing sessions or opening one takes about 10 ms.
Agents get the same views through the `history`, `pins` and `settings`
tools. An agent can raise retention but can't lower logging; it gets the
`bx config set` command to hand to you.

A pin is a copy of one task, kept in `~/Library/Application Support/bx/pins/`
outside the log limits. Say "do the Onboard vendor pin for Lumen Analytics"
and the agent reads its steps, asks you for anything to type (values were
never logged), and follows them page by page the way a person follows notes,
so a moved button doesn't break it.

To send the logs to Loki, Splunk, Datadog or a SIEM, point a log shipper
such as Fluent Bit, Vector or the OpenTelemetry Collector at the sessions
folder. [mx](https://github.com/Facets-cloud/mx) writes the same format.

## Limits

- **macOS only.** The binary isn't signed or notarized yet.
- **Chromium only.** bx speaks Chromium's DevTools Protocol, so Firefox and Safari are out.
- **Remote debugging and Allow.** You turn on remote debugging once per
  browser, and click Allow once per browser run and after each upgrade.
- **Full Disk Access on macOS 27** for the app that runs bx.
- **Tabs it opens stay open** unless the agent closes them.
- **No confirm gate.** bx doesn't stop a click on Submit or Send by itself;
  the skill tells the agent to ask you first.

## How it compares

- **Playwright MCP and Chrome DevTools MCP** by default start their own
  browser with a separate profile, so the agent isn't signed in to anything. bx uses your
  running browser and its profiles. On a 6-step vendor onboarding form, the
  same agent needed a median of 24 tool calls with bx against 62 with
  Playwright MCP, because `batch` fills a whole step in one call; bx's
  server used about 29 MB of memory against 251 MB and 290 MB. It's a small
  benchmark we ran ourselves, on pages we wrote, after tuning bx: the
  method, every run, where bx lost and the caveats are in
  [docs/benchmark.md](docs/benchmark.md).
- **Computer use with screenshots** sees pixels: each step costs a
  screenshot and a guess at coordinates, and the window must be in front.
  bx reads the page as text with exact refs and works in background tabs.
- **Browser extensions** need installing per profile and run inside the
  browser's extension sandbox. bx needs no extension; it uses the browser's
  own debugging switch.

## Develop

```sh
go test ./...
go build -o bin/bx .
```

The tests need no browser. Changing daemon code means `bx stop` and a new
Allow click, because the running daemon is the old binary.

`scripts/npm-build.sh vX.Y.Z` builds `npm/` for a version. Pushing a `v*`
tag runs the release workflow: it builds, attaches tarballs and checksums to
a GitHub release and publishes `@facets-cloud/bx` to npm. Changes under
`site/` on main redeploy the landing page.

## License

MIT. See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).
bx's interaction design follows chrome-devtools-mcp (Apache-2.0), which bx
reimplements in Go.
