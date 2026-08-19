#!/usr/bin/env bash
#
# zen-bell — play a short feedback sound, unless you are already looking at the
# terminal that triggered it.
#
# Used by two callers that share the same "don't nag me if I can see it" rule:
#   - zsh/custom.zsh   long-running command finished (pass/fail tones)
#   - .claude/settings.json  Claude Code Notification / Stop hooks
#
# Usage:
#   zen-bell <sound> [-f|--force] [-v|--volume N]
#
#   <sound>  a freedesktop sound name (complete, dialog-error, window-attention)
#            or an absolute path to a sound file
#   -f       play even if the terminal is focused
#   -v N     volume, 0.0 - 1.0 (default 0.4, or $ZEN_BELL_VOLUME)
#
# Exits 0 whether or not a sound was played — this is feedback, never a failure.

set -euo pipefail

SOUNDS=/usr/share/sounds/freedesktop/stereo
volume="${ZEN_BELL_VOLUME:-0.4}"
force="${ZEN_BELL_WHEN_FOCUSED:-0}"
sound=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f | --force) force=1; shift ;;
        -v | --volume) volume="$2"; shift 2 ;;
        -h | --help) sed -n '2,20p' "$0" | sed 's/^# \?//'; exit 0 ;;
        -*) echo "zen-bell: unknown option $1" >&2; exit 2 ;;
        *) sound="$1"; shift ;;
    esac
done

if [[ -z $sound ]]; then
    echo "zen-bell: no sound given (try: zen-bell complete)" >&2
    exit 2
fi

# Bare names resolve against the freedesktop theme; paths are used as-is.
if [[ $sound != /* ]]; then
    sound="$SOUNDS/$sound"
    [[ $sound == *.oga ]] || sound="$sound.oga"
fi

# --- focus detection -------------------------------------------------------

# Print the parent pid of $1. /proc avoids forking a `ps` per hop.
ppid_of() {
    local stat
    stat=$(< "/proc/$1/stat") || return 1
    # "pid (comm) state ppid ..." — comm can contain spaces and parens, so trim
    # through the LAST ") " and take the field after the state character.
    stat=${stat##*") "}
    # shellcheck disable=SC2086
    set -- $stat
    printf '%s\n' "$2"
}

# Is pid $2 an ancestor of pid $1 (or $1 itself)? Bounded so a cycle or a
# reparented process can't spin.
is_ancestor() {
    local pid="$1" target="$2" n=0
    while [[ -n $pid && $pid != 0 ]] && ((n++ < 12)); do
        [[ $pid == "$target" ]] && return 0
        pid=$(ppid_of "$pid") || return 1
    done
    return 1
}

# True when the caller is running in the window the user is actually looking at.
# Fails closed-to-noisy: any uncertainty (Wayland, no xdotool, X error) reports
# "not focused" so the sound plays. A broken check must never eat a notification.
focused() {
    [[ -n ${DISPLAY:-} ]] || return 1
    command -v xdotool >/dev/null 2>&1 || return 1

    local fpid
    fpid=$(xdotool getactivewindow getwindowpid 2>/dev/null) || return 1
    [[ $fpid =~ ^[0-9]+$ ]] || return 1

    # Outside tmux the terminal emulator is an ancestor of this process.
    # Inside tmux it is not — our parent chain leads to the tmux *server* — so
    # anchor on the tmux client, which IS a child of the terminal emulator.
    local anchor=$$
    if [[ -n ${TMUX:-} ]]; then
        local vis
        vis=$(tmux display-message -p -t "${TMUX_PANE:-}" \
            '#{&&:#{pane_active},#{window_active}}' 2>/dev/null) || return 1
        [[ $vis == 1 ]] || return 1 # background pane is never "focused"
        anchor=$(tmux display-message -p -t "${TMUX_PANE:-}" \
            '#{client_pid}' 2>/dev/null) || return 1
        [[ $anchor =~ ^[0-9]+$ ]] || return 1
    fi

    is_ancestor "$anchor" "$fpid"
}

# --- play ------------------------------------------------------------------

# Inside tmux, ring the terminal bell so tmux flags the originating window with
# "!" in the window list — the sound says something finished, the flag says
# where. tmux.conf sets bell-action to none, so this is purely visual and never
# reaches the terminal emulator; all audio comes from this script.
# Skipped outside tmux, where the bell WOULD reach alacritty's [bell] handler
# and play a second, competing sound.
if [[ -n ${TMUX:-} ]]; then
    printf '\a' > /dev/tty 2>/dev/null || true
fi

if [[ $force != 1 ]] && focused; then
    exit 0
fi

if player=$(command -v pw-play 2>/dev/null) || player=$(command -v paplay 2>/dev/null); then
    # pw-play takes a 0.0-1.0 float; paplay wants an integer 0-65536.
    if [[ ${player##*/} == paplay ]]; then
        volume=$(awk -v v="$volume" 'BEGIN { printf "%d", v * 65536 }')
    fi
    "$player" "--volume=$volume" "$sound" >/dev/null 2>&1 || true
fi

exit 0
