# Coding agents run the bx command, Claude Desktop runs the server

bx is one binary that is both a CLI and an MCP server. Its skill already
teaches the CLI. So an agent with a shell doesn't need the server, and the
`bx` plugin gave Claude Code both: 39 MCP tools and a skill that teaches the
same commands.

## What the server costs

- Its `tools/list` is 23.7k characters for 39 tools, about 6k tokens
  (measured 2026-11-26). `batch` alone is 1.6k. Claude Code defers MCP
  schemas until it needs them, so there it costs little. Agents that load
  every schema up front pay it in every session.
- The `bx` plugin starts the server through `npx`, which needs Node 18 or
  later and uses about 95 MB per session, against 29 MB for bx alone.

The CLI costs nothing until the agent runs it.

## Two plugins in one marketplace

Claude Code updates a plugin in place, so changing `bx` would change it for
everyone who has it. Instead the marketplace has two:

- `bx` (`plugin/`) stays as it was: the MCP server through `npx`, and the
  skill. Codex uses it too.
- `bx-cli` (`plugin-cli/`) is new and Claude Code only. It has no
  `.mcp.json`, since Claude Code always starts a plugin's MCP server. It has:
  - `bin/bx`, a launcher. Claude Code puts a plugin's `bin/` on the Bash
    tool's PATH after the user's own entries, so a bx the user installed
    runs first. Otherwise the launcher runs `~/.local/bin/bx` (or
    `$BX_INSTALL_DIR/bx`), installing it first if it's missing. If that
    fails, it runs bx through `npx`.
  - A SessionStart hook (`hooks/setup.sh`, `async: true`) that installs bx
    in the background, so the first command doesn't wait for the download.
  - `hooks/install.sh` and `skills/bx/SKILL.md`, copies of
    `site/install.sh` and `plugin/skills/bx/SKILL.md`, because a plugin can
    only use files inside its folder. `plugin/plugincli_test.go` fails when
    a copy drifts.

`site/install.sh` downloads `bx_darwin_<arch>.tar.gz` and `checksums.txt`
from the latest release (or `BX_VERSION`), refuses a download that doesn't
match, and installs into `~/.local/bin`. GitHub Pages serves it for
`curl | sh`. After that, `bx update` updates bx as before.

Trade-off: an MCP approval covers one tool, while a Bash rule covers a
command prefix. `Bash(bx:*)` allows every command, `eval` included. Users who
want less can allow prefixes such as `Bash(bx snapshot:*)`.

Codex doesn't put a plugin's `bin/` on PATH, so Codex keeps the server.

## Claude Desktop

Claude Desktop's chat has no shell, so it keeps the server, as `bx.mcpb`: a
Desktop Extension with one universal (arm64 and x86_64) binary, ad hoc
signed. `scripts/mcpb-build.sh` builds it, and `release.yml` attaches it to
each release and lists it in `checksums.txt`. Its manifest sets `BX_MCPB=1`.
Claude Desktop owns the extension folder, so with that set, `bx update`
refuses to replace the binary, and `doctor` points at the new `bx.mcpb`
instead of `bx update`.

Full Disk Access (macOS 27 and later) has to go to bx itself. Claude Desktop
starts extension servers through `Claude.app/Contents/Helpers/disclaimer`,
which disclaims responsibility for the child, so macOS checks the bx binary
and ignores Claude's own access. With `BX_MCPB=1`, bx's permission error
names its own path and the steps.

macOS keys that grant to the binary's designated requirement. Ad hoc signed,
that is the code hash, so the grant would lapse with every update. So the
release signs the binary with a self-signed "bx" identity and the fixed
identifier `cloud.facets.bx` (`scripts/sign-macos.sh`). The requirement is
then `identifier "cloud.facets.bx" and certificate root = H"<cert hash>"`,
the same for every release, and the grant carries over. It is self-signed,
not a Developer ID: Gatekeeper doesn't check a binary Claude Desktop unpacks
itself. `scripts/make-signing-identity.sh` makes the identity once; its
.p12 and password go in the `BX_SIGNING_P12` and `BX_SIGNING_P12_PASSWORD`
secrets. Without them the release signs ad hoc. The certificate holds only
`CN=bx, O=Facets`. Losing the .p12 means a new certificate, and every user
grants Full Disk Access once more.

## Not done

- Cowork: not checked whether a server started from Cowork can reach the
  browser's debugging port and profile files.
- A real Claude Desktop install of `bx.mcpb`. `mcpb validate` passes, and
  the bundled binary answers `initialize` and `tools/list`.
- Removing the server from Codex, which needs a Codex hook that installs
  bx first.
