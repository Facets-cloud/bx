---
name: bx
description: Drive the user's own running Chromium browser (Brave and others) from the shell, in a named profile. Use when a task needs a logged-in session in the user's browser - reading a dashboard, filling a form, checking a page, taking a screenshot - or when the user says "in my browser", "in my <name> profile", names their browser, or says "bx".
---

# bx

`bx` drives the user's real, running browser over the DevTools Protocol.
Tabs live in the user's profiles with their logins and cookies. Run
`bx list profiles` to see the profile names; never guess them. Treat every
action as something the user did with their own hands.

## Rules

- Pass `-o json` when you parse output. Every command supports it.
- Never prompts. Exit codes: 0 ok, 1 user error (bad tab ID, unknown
  profile), 2 bad flags, 3 browser failed or unreachable, 4 permission.
- Exit 4 needs the human: either the terminal app lacks Full Disk Access,
  or nobody clicked Allow in the browser. Tell the user which; don't retry in a loop.
- References are exact: profile display name or folder name, and the full
  tab ID from `bx list tabs`.
- The first command after the browser starts shows an Allow dialog in the
  browser. Tell the user to click it. Later commands reuse that connection.
- If remote debugging is off (exit 3 saying so), ask the user to quit the
  browser, then run `bx setup`. Never quit their browser yourself.
- If the user names a browser ("in Chrome"), pass `--browser chrome`. Check
  `bx list browsers` when unsure which one bx will drive.
- `--browser <name>-agent` (or `BX_BROWSER`) drives a separate agent browser
  started with `bx launch`: no Allow dialog, but its own logins. Use it only
  when the user set it up or asks for it.
- Ask before submitting forms, sending messages, buying, deleting or
  changing settings on the user's behalf. Reading is fine.
- `bx close tab` needs `--yes`. Close only tabs you opened.

## Commands

```
bx list profiles
bx list tabs [--profile <name>]
bx open <url> --profile <name>            # prints the new tab's ID
bx show tab <id>
bx snapshot <tab> [--out <file>]          # elements with refs: e12 button "Save"
bx read <tab> [--selector <el>] [--format text|html]
bx screenshot <tab> [--selector <el>] [--full] [--out <file.png>]
bx eval <tab> '<js expression or () => function>' | --file <script.js>
bx wait <tab> --text <text> | --selector <el> [--gone]
bx goto <tab> <url|back|forward|reload>
bx click <tab> <el> [--double]
bx hover <tab> <el>
bx drag <tab> <from-el> <to-el>
bx fill <tab> <el> <value> [<el> <value> ...]
bx type <tab> <text> [--submit Enter]     # into whatever has focus
bx key <tab> <key>                        # Enter, Escape, Tab, ArrowDown, Meta+a
bx upload <tab> <el> <file> [<file> ...]
bx dialog <tab> accept|dismiss [--text <reply>]
bx front <tab>
bx close tab <id> --yes
bx cdp <Method> [<json-params>] [--tab <id>]
bx console <tab> [--errors] [--since <id>] [--limit <n>]   # console, exceptions, failed loads
bx network <tab> [--errors] [--since <id>] [--limit <n>]   # requests: status, type, time
bx network <tab> --request <id>           # one request's headers and bodies
bx storage <tab>                          # cookies, localStorage, sessionStorage
bx performance <tab> [--reload]           # Web Vitals, long frames, slow resources
bx emulate <tab> [--viewport 390x844@3,mobile] [--network offline|slow-3g|fast-3g]
          [--cpu 4] [--geo <lat>,<lon>] [--color dark|light] [--reset]
bx status | bx stop | bx doctor | bx setup | bx launch
bx list browsers                          # installed, running, debugging on, default
bx use <browser>                          # set the default browser
```

An `<el>` is a ref from the tab's latest `bx snapshot` (such as `e12`) or
a CSS selector. `--timeout <dur>` (default 30s) bounds waits.

## How to work a page

1. `bx snapshot <tab>` first. It lists the page's elements with refs, roles,
   names, values and states like `[checked]` or `[disabled]`. Prefer it to a
   screenshot: it is text, and its refs are exact.
2. Act with refs: `bx click <tab> e12`, `bx fill <tab> e4 'text' e7 Green e9 true`.
   One fill call sets several fields: text fields are typed into, a select
   takes an option's value or visible text, a checkbox or radio takes true
   or false.
3. Read the action's result. Every action waits for the page to settle,
   then reports `navigated`, `url`, `title`, `new_tabs` and `dialog`.
4. Take a new snapshot after a navigation or a big page change. A ref from
   an old snapshot fails with exit 1 and says so.

Typing into canvas apps such as Google Sheets: click a real field first
(for Sheets, `bx fill <tab> '#t-name-box' E5` then `bx key <tab> Enter` to
select a cell), then `bx type <tab> 'text' --submit Enter`. bx keeps every
tab it touches focused in the background, so the tab need not be in front.

On macOS, use Meta for shortcuts: `bx key <tab> Meta+a` selects all.

## Dialogs

If an action opens an alert, confirm or prompt, the action returns at once
with `dialog` set, and every other action on that tab fails with exit 1
until you answer it: `bx dialog <tab> accept` or `dismiss` (`--text` fills
a prompt). Ask the user before accepting a confirm that deletes or sends.

## Example

```sh
id=$(bx open https://app.example.com --profile Work -o json | jq -r .id)
bx snapshot "$id"
bx fill "$id" e5 'search text' -o json
bx click "$id" e6 -o json | jq '{navigated, url, new_tabs}'
bx eval "$id" '() => [...document.querySelectorAll("tr")].map(r => r.innerText)'
```

`bx list tabs --profile <name>` may briefly open and close a helper tab in
that profile the first time, to learn which tabs belong to it. In
`bx list tabs` without `--profile`, PROFILE shows `?` for tabs in profiles
bx hasn't seen yet.

## Debugging a page

bx records each tab's console and network from the first time it touches
the tab (and replays console messages from before). To find why something
breaks:

1. Note where the logs end: `bx console "$id" -o json --limit 1` gives the
   last id; do the same for `bx network`.
2. Do the steps that fail (click, fill, ...).
3. `bx console "$id" --errors --since <id>` and
   `bx network "$id" --errors --since <id>` show only what those steps
   caused. `bx network "$id" --request n12` shows the failing request's
   request and response bodies, which usually name the cause.

Requests made before bx touched the tab aren't recorded: `bx goto "$id"
reload` to capture a page load. `bx emulate` lasts until `bx stop`; run
`bx emulate "$id" --reset` when done.

## Escape hatch

`bx cdp` sends any DevTools Protocol method over the same connection and
prints the raw JSON result. Without `--tab` it goes to the browser (Target.*,
Browser.*); with `--tab` it goes to that page (Page.*, Runtime.*, DOM.*).
Use it for what bx has no command for, such as Lighthouse-style audits or
DOM breakpoints.

## When something fails

Run `bx doctor -o json`. It checks that the browser is installed, profiles
are readable (Full Disk Access), remote debugging is on (the browser's
inspect page, such as brave://inspect/#remote-debugging), and the skill is
current. It also reports when a newer bx exists: tell the user, and run
`bx update` if they agree. Pass `--browser <name>` to any command to drive
a browser other than the default.

If clicks time out on a tab where the user has a DevTools window open,
ask them to close DevTools on that tab (or resume it if it is paused in
the debugger); bx reads console and network without it.

To let the user watch, set `BX_FOLLOW=1`: every tab bx opens or acts in
is brought to the front of its window.
