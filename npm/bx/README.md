# @facets-cloud/bx

An MCP server that lets Claude, Codex, Cursor and other MCP clients drive
the Chromium browser you already have open, in the profile you name, with
your logins. macOS only. No telemetry.

## Install

Claude Code and Codex:

```sh
claude mcp add -s user bx -- npx -y @facets-cloud/bx mcp
codex mcp add bx -- npx -y @facets-cloud/bx mcp
```

Claude Desktop, Cursor and other apps, in their MCP config:

```json
{ "mcpServers": { "bx": { "command": "npx", "args": ["-y", "@facets-cloud/bx", "mcp"] } } }
```

Or run `npx -y @facets-cloud/bx mcp install`, which adds that entry to
Claude Desktop and Cursor if they are installed.

Use the full name `@facets-cloud/bx`. The npm package called plain `bx` is
an unrelated WebdriverIO tool.

## First run

1. Turn on remote debugging in your browser. It is off by default, and bx
   can't connect until it's on. Open this page in your browser and switch
   it on:

   | Browser | Page |
   | --- | --- |
   | Brave | `brave://inspect/#remote-debugging` |
   | Chrome, Chromium, Arc | `chrome://inspect/#remote-debugging` |
   | Edge | `edge://inspect/#remote-debugging` |
   | Vivaldi | `vivaldi://inspect/#remote-debugging` |

   If the page has no remote-debugging switch, update the browser. Older
   versions don't have it.
2. Give the app that runs the MCP server Full Disk Access (System
   Settings > Privacy & Security > Full Disk Access): your terminal for
   Claude Code and Codex, or Claude Desktop or Cursor itself. On recent
   macOS versions bx can't read the browser's debugging port file without
   it.
3. Restart your MCP app and ask it to run the `doctor` tool to check.
4. On the first browser action, the browser asks you to Allow the
   connection. Click it once. It holds until the browser quits.

Rather not do step 1 by hand? Quit the browser and ask the agent to run
the `setup` tool. It switches the setting on for you (`off: true` switches
it back off).

## Tools

| Group | Tools |
| --- | --- |
| Setup | `doctor`, `list_browsers`, `setup`, `launch` |
| Read | `list_profiles`, `list_tabs`, `snapshot`, `read`, `screenshot`, `eval`, `wait` |
| Act | `open`, `goto`, `click`, `hover`, `drag`, `fill`, `type`, `key`, `upload`, `dialog`, `close_tab` |
| Debug | `console`, `network`, `storage`, `performance`, `emulate`, `cdp` |

A typical run: `list_profiles`, then `open` a URL in a profile, then
`snapshot` the tab. The snapshot gives every element a ref like `e12`,
which `click`, `fill` and the other actions take. Each action reports
whether the page navigated, opened a tab or showed a dialog.

The debug tools are DevTools for the agent:
- `console` lists console messages, uncaught exceptions and failed loads,
  with the file and line.
- `network` lists requests with status and timing. Pass a request id for
  its headers and request and response bodies.
- `storage` shows the page's cookies, localStorage and sessionStorage.
- `performance` reports Core Web Vitals (LCP, CLS, FCP, TTFB) with
  ratings, the long tasks and the scripts behind them, the slowest
  resources, and heap and DOM size.
- `emulate` sets a phone viewport, slow 3G or offline, a slower CPU, a
  location, or dark mode.
- `cdp` sends any other DevTools Protocol command.

So "log in to staging, add an item, check out, and tell me why it fails"
ends with the agent reading the failing request's response body and the
exception's file and line.

## How the one Allow works

Chromium asks you to Allow every new DevTools connection. So the first
tool call starts a small background process that holds one connection,
and every MCP session, from every app, goes through it over a Unix socket
in `~/Library/Application Support/bx/`. It stops when the browser quits.

## Browsers

Brave and Chrome are tested. Chromium, Edge, Arc and Vivaldi use the same
layout and should work. The server picks the first browser with remote
debugging on. To pin one, pass `browser` in a tool call, or set it for the
whole server:

```json
"env": { "BX_BROWSER": "chrome" }
```

To skip the Allow dialog entirely, the `launch` tool starts a separate
copy of the browser just for agents, with its own profile and logins.
Drive it with `browser: "brave-agent"` (or `chrome-agent`, and so on).
Any program on your Mac can connect to that copy, so sign in there only to
what agents need.

## Security

The server gives the agent your logged-in browser: it can read any page
and act as you on any site you're signed in to. The server instructions
tell the agent to ask before submitting forms, sending messages, buying or
deleting, but that is the agent's judgment, not a hard limit. Use a
separate profile, or the `launch` browser, for anything you don't want an
agent to touch.

## License

MIT. See LICENSE and THIRD_PARTY_NOTICES.
