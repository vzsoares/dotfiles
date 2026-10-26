#!/usr/bin/env bash
set -e

# usage: caffeinate.sh [hours] — picks duration (0.5-24h) and what to keep awake.
# macOS uses caffeinate; Linux falls back to systemd-inhibit (idle + sleep only).

if ! command -v gum &>/dev/null; then
    echo "Error: gum is required."
    exit 1
fi

HOURS="$1"
if [ -z "$HOURS" ]; then
    HOURS=$(seq 0.5 0.5 24 | gum choose --header "Stay awake for (hours):" --height 12 --selected 1)
fi
if ! [[ "$HOURS" =~ ^[0-9]+(\.[0-9]+)?$ ]] || awk "BEGIN{exit !($HOURS < 0.5 || $HOURS > 24)}"; then
    gum style --foreground 196 "Hours must be a number between 0.5 and 24 (got '$HOURS')"
    exit 1
fi
SECONDS_TOTAL=$(awk "BEGIN{printf \"%d\", $HOURS * 3600}")

OPTIONS=(
    "-d  display  keep the screen on"
    "-i  idle     prevent idle sleep"
    "-s  system   prevent system sleep (AC power only)"
    "-m  disk     prevent disk idle sleep"
    "-u  user     declare user active (wakes the display)"
)
PICKED=$(printf '%s\n' "${OPTIONS[@]}" | gum choose --no-limit --header "Keep awake (space to toggle):" --selected "${OPTIONS[0]}")
if [ -z "$PICKED" ]; then
    gum style --foreground 196 "No option picked"
    exit 1
fi
read -ra FLAGS <<<"$(awk '{print $1}' <<<"$PICKED" | tr '\n' ' ')"

if date -v+1S &>/dev/null; then
    UNTIL=$(date -v+"${SECONDS_TOTAL}"S +%H:%M)
else
    UNTIL=$(date -d "+${SECONDS_TOTAL} seconds" +%H:%M)
fi

if command -v caffeinate &>/dev/null; then
    gum style --faint "caffeinate -t $SECONDS_TOTAL ${FLAGS[*]} — awake until $UNTIL (Ctrl-C to stop)"
    exec caffeinate -t "$SECONDS_TOTAL" "${FLAGS[@]}"
elif command -v systemd-inhibit &>/dev/null; then
    gum style --faint "systemd-inhibit idle:sleep for ${HOURS}h — awake until $UNTIL (Ctrl-C to stop)"
    exec systemd-inhibit --what=idle:sleep --who=caffeinate.sh --why="run caffeinate" sleep "$SECONDS_TOTAL"
else
    gum style --foreground 196 "Neither caffeinate nor systemd-inhibit is available"
    exit 1
fi
