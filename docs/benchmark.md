# bx vs Playwright MCP vs Chrome DevTools MCP

**Read this first.** This is a small benchmark run by the bx authors, not
an independent one:

- We wrote the two test pages, for the bx demo, before this benchmark
  existed. They are multi-step forms and a grid, which is where bx's
  `batch` helps; other kinds of task may come out differently.
- We changed bx after seeing the first round (sections "After the paste
  fix" and "Round 3"), so the later bx numbers are tuned to these tasks.
- Three runs per tool and task, one Mac, one model (Claude Opus 5.5).
  Single runs vary a lot; one Playwright MCP run took 124 s against 22 s
  for the others.
- Chrome DevTools MCP ran only in the first round, when the model answered
  more slowly, so its times aren't comparable with the later rounds.
- Time and cost depend on the model and will change with it. The call
  counts follow from how each tool works and should hold better.

Playwright MCP and Chrome DevTools MCP are good tools with features bx
doesn't have (cross-browser and headless runs, performance tracing). If
you rerun this and get different numbers, open an issue and we'll update
this page.

Measured 2026-11-14 on an Apple M5 MacBook (16 GB, macOS 27.2.1). Each tool ran the same two tasks 3 times through `claude -p` (Claude Code 2.1.325, model claude-opus-5-5), with only that tool's MCP server loaded and no built-in tools. All 18 runs reached the checked end state. Every number in the tables is a median of 3 runs unless the row says otherwise.

Versions: bx built from the bx repo at commit 986c73d, driving its agent browser (Brave 157.1.99.57). `@playwright/mcp@0.0.85` (playwright 1.66.0-alpha-2026-11-09) and `chrome-devtools-mcp@1.13.0`, both launching the installed Google Chrome 157.0.8103.79.

## Tasks

- **grid**: type a 4x3 table into `uc-grid/index.html`, a spreadsheet that only takes key presses (it ignores pasted text). Pass means cells A1 to C4 hold exactly the 12 values and no other cell has text.
- **wizard**: complete the 6-step vendor onboarding in `uc-wizard/index.html`, then approve the request in the Approvals inbox. The steps include a country combobox, a shadow-DOM bank form, a PDF upload, a terms box that must be scrolled to the end, a rich-text editor inside a shadow-root modal, an approver autocomplete whose suggestions arrive after 600 ms, and a confirm modal. Pass means the stored request has status Approved, the right approvers, route, due date and approval comment, and only one request was submitted.

The pages are copies of the bx-demo pages with one script added before `</body>`. Every 300 ms the script posts the grid cells and `localStorage.reqs` to the bench's local server, which writes them to `states/<run>.jsonl`. `scripts/check.py` grades the last state posted before the agent exited, so the pass/fail column doesn't depend on what the agent says it did.


### grid

| Metric (median of 3) | bx | Playwright MCP | Chrome DevTools MCP |
|---|---|---|---|
| Success | 3/3 | 3/3 | 3/3 |
| Wall time (s) | 29.1 | 27.3 | 31.5 |
| Turns | 13 | 8 | 31 |
| Tool calls | 12 | 7 | 30 |
| Input tokens (uncached) | 22 | 14 | 16 |
| Cache write tokens | 6,293 | 5,534 | 14,779 |
| Cache read tokens | 170,612 | 82,605 | 148,544 |
| Output tokens | 1,935 | 1,326 | 3,069 |
| Cost (USD) | 0.123 | 0.087 | 0.213 |
| Peak RSS, server + browser (MB) | 683.2 | 818.2 | 788.9 |
|   of which MCP server (MB) | 29.2 | 253.2 | 304.8 |
|   of which browser (MB) | 655.5 | 639.5 | 615.6 |
| First page loaded, from process start (s) | 4.3 | 5.2 | 4.7 |
| Navigate call latency (s) | 0.1 | 0.9 | 1.1 |
| JS-injection workarounds (total over 3 runs) | 0 | 0 | 0 |

Per run (wall s / tool calls / cost USD):

- bx: 39s / 12 / $0.127; 29s / 12 / $0.123; 28s / 12 / $0.114
- playwright: 27s / 7 / $0.087; 27s / 7 / $0.087; 25s / 7 / $0.083
- devtools: 34s / 32 / $0.213; 32s / 30 / $0.150; 31s / 24 / $0.229

### wizard

| Metric (median of 3) | bx | Playwright MCP | Chrome DevTools MCP |
|---|---|---|---|
| Success | 3/3 | 3/3 | 3/3 |
| Wall time (s) | 61.1 | 105.5 | 111.6 |
| Turns | 28 | 64 | 56 |
| Tool calls | 27 | 63 | 55 |
| Input tokens (uncached) | 52 | 92 | 66 |
| Cache write tokens | 14,021 | 30,130 | 32,312 |
| Cache read tokens | 518,709 | 1,137,831 | 988,454 |
| Output tokens | 3,952 | 4,766 | 6,820 |
| Cost (USD) | 0.291 | 0.563 | 0.593 |
| Peak RSS, server + browser (MB) | 684.6 | 760 | 857.5 |
|   of which MCP server (MB) | 29 | 251.4 | 289.7 |
|   of which browser (MB) | 656.6 | 634.5 | 629 |
| First page loaded, from process start (s) | 4.2 | 5.1 | 5.4 |
| Navigate call latency (s) | 0.1 | 0.6 | 1.1 |
| JS-injection workarounds (total over 3 runs) | 1 | 1 | 10 |

Per run (wall s / tool calls / cost USD):

- bx: 61s / 27 / $0.283; 69s / 26 / $0.291; 61s / 29 / $0.307
- playwright: 87s / 63 / $0.481; 130s / 72 / $0.608; 105s / 60 / $0.563
- devtools: 115s / 63 / $0.684; 102s / 55 / $0.593; 112s / 53 / $0.522


## What happened in the runs

- **wizard**: bx used `batch` to fill each step in one call (9 to 10 batches per run), so it needed 27 tool calls against 63 for Playwright and 55 for DevTools. That cut cache-read tokens by about half and cost by about half.
- **grid**: bx lost. Its default typing pastes text, and this grid only listens for key presses. In all 3 bx runs the first batch typed nothing. The agent read the page's script, called `start` again with `typing: "keys"`, and redid the work. Playwright's agent wrote one `browser_run_code_unsafe` script that pressed every key, which made it the cheapest and fastest tool on this task. DevTools clicked and typed cell by cell (24 to 32 calls).
- **Terms box**: every tool's agent fell back to JavaScript at least once to scroll the terms box (bx 1 run, Playwright 1 run, DevTools all 3). DevTools runs also used `evaluate_script` to dispatch synthetic mouse events on combobox options and to set the date input, 10 JS workarounds across 3 runs. No agent wrote the app's internal `state` directly.
- **Tool-definition overhead** (from a one-turn "Reply with ok" run per tool, `runs/smoke-*`): context before any work was 10,109 tokens with Playwright (25 tools), 13,336 with bx (33 tools) and 15,141 with DevTools (33 tools). bx's tool descriptions are about 3.2k tokens heavier than Playwright's.

## After the paste fix (same day, later)

The grid loss came from bx pasting text into a page that only reads key
presses. bx then changed: after a paste it checks that the text arrived,
and if not (a focused element that isn't a text field, or nothing focused
right after the agent clicked something and the page didn't change) it
types the text key by key itself. Typing into an element that can't take
focus now clicks it first.

Rerun of the grid task, bx and Playwright MCP three times each, back to
back in the same hour (so not comparable with the timings above: the model
answered more slowly then):

| grid, median of 3 | bx before the fix | bx after | Playwright MCP, same hour |
|---|---|---|---|
| Success | 3/3 | 3/3 | 3/3 |
| Tool calls | 12 | 10 | 7 |
| Cost (USD) | 0.123 | 0.096 | 0.090 |
| Wall time (s) | 29.1 | 33.7 | 30.8 (one run took 124) |

The fix took out the agent's detour (reading the page's script and asking
for key typing); Playwright MCP still needed fewer calls on this task.
bx's remaining extra calls: `list_profiles` before `open` (which needs a
profile name) and a separate `snapshot` after `open`. One wizard run on
the new build also completed (27 calls, $0.29); the page's state beacon
stopped posting once the window lost focus, so it was checked by reading
the request from the browser instead (status Approved, right comment).

## Round 3: fewer calls and a smaller tool list

What the grid runs above showed bx spending calls on, and the change:

- `list_profiles` before every `open`, because `open` required a profile.
  `open` now defaults to the profile the user last used.
- A `snapshot` right after `open` (sometimes after guessing the tab id).
  `open` now returns the page's interactive elements.
- About 1,700 tokens a turn for `browser` and `timeout`, described in full
  on all 34 tools, and about 600 for the element description on ten of
  them. Both are now explained once in the server instructions; `timeout`
  stays only on `open`, `goto`, `wait` and `batch`. Context before any
  work went from 13,336 tokens to 11,087 (Playwright MCP: 10,109).

bx and Playwright MCP, three rounds, each round running both tools at the
same time; every run passed the checker:

| median of 3 | bx | Playwright MCP |
|---|---|---|
| grid: wall time (s) | 21.0 | 21.8 |
| grid: tool calls | 7 | 6 |
| grid: cost (USD) | 0.077 | 0.093 |
| wizard: wall time (s) | 55.4 | 84.2 |
| wizard: tool calls | 24 | 62 |
| wizard: cost (USD) | 0.274 | 0.538 |

Per run (wall s / calls / cost): grid bx 21/7/0.078, 24/7/0.075, 19/7/0.077;
grid Playwright 21/6/0.073, 22/6/0.098, 22/4/0.093; wizard bx 60/27/0.284,
54/24/0.258, 55/24/0.274; wizard Playwright 84/69/0.538, 71/61/0.448,
105/62/0.605. Chrome DevTools MCP wasn't rerun.

## Setup comparison

| | bx | Playwright MCP | Chrome DevTools MCP |
|---|---|---|---|
| Needs installed | the bx binary (Go, no runtime); a Chromium browser (Brave, Chrome, Chromium, Edge, Arc, Vivaldi); macOS only | Node.js + npx; Chrome or Edge, or a Playwright-downloaded browser | Node.js + npx; Chrome (stable/beta/dev/canary, or `--executablePath`) |
| Install size | 12.8 MB binary built locally; npm package `@facets-cloud/bx-darwin-arm64` is 8.6 MB unpacked | 19 MB `node_modules` for `@playwright/mcp@0.0.85` | 17 MB `node_modules` for `chrome-devtools-mcp@1.13.0` |
| Browser download | none, uses an installed browser | none here (used installed Chrome); without one you run `npx playwright install` for a bundled browser | none, needs installed Chrome |
| Use the user's existing logged-in profile | yes: attaches to the running browser in the named profile after one Allow click per browser run (or `bx launch` for a separate agent browser with its own logins, which these runs used) | only with the "Playwright Extension" installed in Chrome/Edge (`--extension`), or `--cdp-endpoint` to a browser started with remote debugging; `--user-data-dir` gives it its own persistent profile, not your running one | `--autoConnect` to a running Chrome 144+ after turning on remote debugging at chrome://inspect/#remote-debugging, or `--browserUrl` to a Chrome started with a debugging port; default is its own profile under `~/.cache/chrome-devtools-mcp` |
| Telemetry | none | none found in flags | usage statistics on by default, as its README documents (`--usageStatistics=false` or `CHROME_DEVTOOLS_MCP_NO_USAGE_STATISTICS`); performance traces send URLs to CrUX unless `--performanceCrux=false`. Both were off for these runs |
| Agent browser launch to first page | 0.41 s for `bx launch` + first command (median of 0.84, 0.41, 0.35 s), then 0.1 s per `open` | 0.6 to 0.9 s for the first `browser_navigate`, which launches Chrome | 1.1 s for the first `new_page`, which launches Chrome |
| First page loaded, from `claude -p` start | 4.2 to 4.3 s (browser already running) | 5.1 to 5.2 s | 4.7 to 5.4 s |

The "first page loaded" times include Claude Code's own startup and the model's first turn (about 3 to 4 s), so most of that number isn't the browser tool.

## How the runs were made

Per tool MCP config (`mcp/*.json`):

```json
{"mcpServers":{"bx":{"command":"./bx","args":["mcp"],"env":{"BX_BROWSER":"brave-agent"}}}}
{"mcpServers":{"playwright":{"command":"npx","args":["-y","@playwright/mcp@0.0.85","--isolated"]}}}
{"mcpServers":{"chrome-devtools":{"command":"npx","args":["-y","chrome-devtools-mcp@1.13.0","--isolated","--performanceCrux=false","--usageStatistics=false"],"env":{"CHROME_DEVTOOLS_MCP_NO_USAGE_STATISTICS":"1"}}}}
```

Each run (`scripts/run.py <tool> <task> <n>`) executes, from the bench folder:

```sh
claude -p "<prompt>" --output-format stream-json --verbose \
  --mcp-config mcp/<tool>.json --strict-mcp-config --permission-mode bypassPermissions \
  --tools "" --setting-sources "" --disable-slash-commands --no-session-persistence
```

`--tools ""` removes Bash, Read and the other built-ins so the agent can only use the browser tool. `--setting-sources ""` keeps the user's hooks, plugins and CLAUDE.md out of the runs. Before each bx run, `scripts/bx-reset.sh` clears the origin's localStorage and closes every tab but one blank one in bx's agent browser. Playwright and DevTools ran with `--isolated`, so each run got a fresh browser profile.

The three tools ran at the same time in each round (one round per run number), so they shared the machine and the API under the same conditions. A sampler ran `ps` every second and summed RSS over the `claude` process's descendants (the MCP server, the npx wrapper and any browser it launched). For bx it also added the bx daemon and the agent browser's process tree, which live outside that tree. Token counts and cost come from the final `result` event.

Prompts (`{RUN}` is the run id, `{PDF}` the absolute path of `fixtures/signed-agreement.pdf`):

grid:

```
Use the browser tools to open http://localhost:8771/uc-grid/index.html?run={RUN} . It is a small spreadsheet. Enter this table starting at cell A1, one value per cell, and leave every other cell empty:

Row 1: Item, Qty, Price
Row 2: Apples, 12, 0.50
Row 3: Pears, 7, 0.80
Row 4: Total, 19, 9.60

When you are done, reply with the values of A1 to C4 exactly as the page shows them.
```

wizard:

```
Use the browser tools to open http://localhost:8771/uc-wizard/index.html?run={RUN} . It is a 6-step vendor onboarding wizard. Onboard this vendor, submit it, then approve it.

1. Company: legal name "Lumen Analytics Pvt Ltd", country India, company type Private limited, GSTIN 27ABCDE1234F1Z5.
2. Contacts: Priya Nair, priya@lumen.example, role Billing; Rahul Shah, rahul@lumen.example, role Technical.
3. Bank & documents: account holder "Lumen Analytics Pvt Ltd", account number IN12LUMN0000123456, SWIFT LUMNINBB. Upload the signed agreement {PDF} . Read the vendor terms to the end and accept them.
4. Announcement: post "Welcome Lumen Analytics, our new analytics vendor."
5. Approvals: approvers Asha Rao (Legal) and Ben Carter (Security), route Sequential, due date 2026-12-15.
6. Review: submit for approval and confirm.

Then open the Approvals inbox, review the request you just submitted and approve it with the comment "Documents verified and bank details checked." Reply with the request id and its final status.
```

Files: `runs/<task>-<tool>-<n>.jsonl` (stream-json, each line prefixed with seconds since start), `.rss.tsv` (1 s memory samples), `.meta.json`, `.err`; `runs/summary.json` (all per-run metrics); `runs/nav.json`; `states/` (page-posted end states); `scripts/` (runner, checker, tables); `pages/` (bench copies of the test pages). Rebuild the tables with `python3 scripts/summarize.py && python3 scripts/tables.py`. Cost: 21 `claude -p` runs (18 measured + 3 one-turn smoke runs), $5.59 in total.
