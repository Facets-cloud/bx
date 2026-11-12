# bx

bx lets coding agents (Claude Code, Codex) drive the Chromium browser you
already have open, in the profile you name, over the DevTools Protocol. It
is one Go binary with no runtime dependencies and no telemetry.

<p align="center">
  <a href="docs/demo.mp4"><img src="docs/demo.gif" width="900" alt="Left, a terminal runs bx: it opens TodoMVC in the Personal profile, takes a snapshot that numbers the elements, fills the new-todo box by its ref, types two more todos, clicks a checkbox and the Active filter by their refs, and reads the remaining list. Right, the same browser tab follows each command."></a>
</p>
<p align="center"><sub>bx driving a real Brave tab: snapshot, refs, fill, type, click, read. <a href="docs/demo.mp4">Watch it in full quality</a>.</sub></p>

```sh
bx list profiles
id=$(bx open https://example.com --profile Work -o json | jq -r .id)
bx snapshot "$id"                      # elements with refs: e12 button "Save"
bx fill "$id" e4 'hello' e7 Green e9 true
bx click "$id" e12                     # reports navigation, new tabs, dialogs
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
once per browser run; `bx update` keeps the connection open, so upgrading
doesn't cost another click. The process exits when the browser quits or on
`bx stop`. It never exits just for being idle.

The daemon also stays attached to every tab it touches. That keeps the
tab's focus emulated, so a background tab takes typing like the front one;
it lets an action that opens an alert or confirm stop and report it
(`bx dialog` answers it); and it keeps snapshot refs valid until the tab
navigates. After each action bx waits the way a person would: for a
navigation the action started to finish loading, then for the page to go
100ms without changing.

The DevTools Protocol can't open a tab in a chosen profile:
`Target.createTarget` works only for the last-focused one. So bx asks the
browser to open the page with `--profile-directory`, then finds the new
tab. Tabs don't say which profile they belong to, so the first time bx
needs a profile's tabs it opens a `data:` page carrying a random nonce in
that profile, reads which context the page landed in, and closes it.

## Install (macOS)

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
   While the repo is private those URLs answer 404; use the GitHub CLI
   instead: `gh release download --repo Facets-cloud/bx --pattern 'bx_darwin_<arch>.tar.gz' --pattern checksums.txt`.
2. Unpack it and put `bx` on the PATH, in `~/.local/bin` if that is on the
   PATH, else `/usr/local/bin`. Remove macOS's quarantine flag:
   `xattr -d com.apple.quarantine <path>/bx 2>/dev/null || true`.
3. Run `bx skill install`. It installs the skill for Claude Code and Codex.
   If the user asked for bx as an MCP server, run `bx mcp install` instead
   (or as well), and tell them to restart Claude Desktop or Cursor.
4. Ask the user to give the terminal app **Full Disk Access** (System
   Settings > Privacy & Security), because macOS blocks reading the
   browser's profile folder.
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
use it. `bx mcp` serves 28 tools on stdio: setup (`doctor`,
`list_browsers`, `setup`, `launch`), reading (`list_profiles`,
`list_tabs`, `snapshot`, `read`, `screenshot`, `eval`, `wait`), acting
(`open`, `goto`, `click`, `hover`, `drag`, `fill`, `type`, `key`,
`upload`, `dialog`, `close_tab`) and debugging (`console`, `network`,
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
`"env": {"BX_BROWSER": "chrome"}`, or pass `browser` in a tool call.

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
