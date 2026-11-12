#!/usr/bin/env node
// Runs the bx binary from the platform package npm installed alongside
// this one (@facets-cloud/bx-<os>-<cpu>), passing arguments, stdio and
// the exit code through. bx mcp speaks MCP over this same stdio.
"use strict";
const { spawn } = require("node:child_process");

const pkg = `@facets-cloud/bx-${process.platform}-${process.arch}`;
let bin;
try {
  bin = require.resolve(`${pkg}/bin/bx`);
} catch (err) {
  if (err.code !== "MODULE_NOT_FOUND") throw err;
  console.error(`bx: no build for ${process.platform}-${process.arch} (looked for ${pkg}). bx supports macOS on arm64 and x64.`);
  process.exit(1);
}

const child = spawn(bin, process.argv.slice(2), { stdio: "inherit", env: { ...process.env, BX_VIA_NPM: "1" } });
for (const sig of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(sig, () => child.kill(sig));
}
child.on("exit", (code, signal) => {
  if (signal) process.kill(process.pid, signal);
  else process.exit(code ?? 1);
});
