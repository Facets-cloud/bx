#!/bin/sh
# Runs in the background when a Claude Code session starts: installs bx into
# $BX_INSTALL_DIR (default ~/.local/bin) if it isn't there, so the first bx
# command doesn't wait for the download. Quiet, and never fails the session.
dir=${BX_INSTALL_DIR:-$HOME/.local/bin}
[ -x "$dir/bx" ] || sh "$(dirname "$0")/install.sh" >/dev/null 2>&1
exit 0
