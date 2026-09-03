#!/usr/bin/env bash
# Pause/resume AeroSpace window management — e.g. before handing the Mac to
# someone who'd be confused by tiling. `aerospace enable off` stops
# intercepting keys and unpacks other workspaces onto the screen; `on`
# restores it. See: https://nikitabobko.github.io/AeroSpace/commands#enable
set -e

if ! command -v aerospace &>/dev/null; then
    echo "aerospace not found in PATH (only relevant on macOS)" >&2
    exit 1
fi

read -r -p "Toggle AeroSpace tiling on/off? [y/N] " reply
case "$reply" in
    y|Y) aerospace enable toggle ;;
    *) echo "Cancelled." ;;
esac
