#!/usr/bin/env bash
# "Friend mode" — e.g. before handing the Mac to someone who'd be confused
# by tiling. `aerospace enable off` only stops key interception; it doesn't
# un-tile windows already on screen or bring back the autohidden Dock, so
# this also floats + stretches every open window full-screen (AeroSpace's
# own windowed fullscreen — still a normal managed window, no macOS Space
# switch) and shows the Dock (all reversed when you toggle back on).
#
# Once disabled, the AeroSpace CLI refuses every command (including
# list-windows/layout), so windows must be adjusted *before* disabling, and
# *after* re-enabling — reverse order breaks silently.
# See: https://nikitabobko.github.io/AeroSpace/commands#enable
set -e

if ! command -v aerospace &>/dev/null; then
    echo "aerospace not found in PATH (only relevant on macOS)" >&2
    exit 1
fi

read -r -p "Toggle AeroSpace friend-mode (float + fill-screen windows, pause tiling)? [y/N] " reply
case "$reply" in
    y|Y) ;;
    *) echo "Cancelled."; exit 0 ;;
esac

each_window() {
    local id
    while IFS= read -r id; do
        [ -n "$id" ] && "$@" --window-id "$id" >/dev/null 2>&1 || true
    done <<<"$(aerospace list-windows --all --format '%{window-id}' 2>/dev/null)"
}

# A successful list-windows means the server is currently enabled -> we're
# entering friend mode. If it's disabled, the CLI errors out instead.
if aerospace list-windows --all >/dev/null 2>&1; then
    each_window aerospace layout floating
    each_window aerospace fullscreen on
    aerospace enable off
    defaults write com.apple.dock autohide -bool false && killall Dock
    echo "Friend mode ON — tiling paused, windows filling the screen, Dock visible."
else
    aerospace enable on
    # fullscreen must be cleared *before* going back to tiling — layout
    # tiling doesn't clear it on its own, and a tiled window stuck
    # "fullscreen" would swallow the whole workspace.
    each_window aerospace fullscreen off
    each_window aerospace layout tiling
    defaults write com.apple.dock autohide -bool true && killall Dock
    echo "Friend mode OFF — tiling resumed, all windows re-tiled, Dock hidden."
fi
