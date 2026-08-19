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
#   zen-bell <sound> [-f] [-v N] [-n SUMMARY] [-b BODY] [-u URGENCY] [-t MS]
#
#   <sound>  a freedesktop sound name (complete, dialog-error, window-attention)
#            or an absolute path to a sound file
#   -f       play even if the terminal is focused
#   -v N     volume, 0.0 - 1.0 (default 0.4, or $ZEN_BELL_VOLUME)
#   -n TEXT  also raise a desktop notification with this summary
#   -b TEXT  body text for the notification (needs -n)
#   -u LEVEL notification urgency: low | normal | critical
#            critical does not auto-dismiss — use it for "needs your input"
#   -t MS    notification expire time in milliseconds; 0 = never expires.
#            Overrides the dunst per-urgency timeout (urgency_normal is 10s).
#
# The notification is gated by the same focus check as the sound: if you are
# looking at the terminal, neither fires. Pass -f to always notify.
#
# Exits 0 whether or not a sound was played — this is feedback, never a failure.

set -euo pipefail

SOUNDS=/usr/share/sounds/freedesktop/stereo
volume="${ZEN_BELL_VOLUME:-0.4}"
force="${ZEN_BELL_WHEN_FOCUSED:-0}"
sound=""
summary=""
body=""
urgency=""
timeout_ms=""

# Options taking a value must actually have one; `shift 2` past the end would
# abort under `set -u` with a confusing error.
need_arg() {
    [[ $# -ge 2 ]] || { echo "zen-bell: $1 needs a value" >&2; exit 2; }
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f | --force) force=1; shift ;;
        -v | --volume) need_arg "$@"; volume="$2"; shift 2 ;;
        -n | --notify) need_arg "$@"; summary="$2"; shift 2 ;;
        -b | --body) need_arg "$@"; body="$2"; shift 2 ;;
        -u | --urgency) need_arg "$@"; urgency="$2"; shift 2 ;;
        -t | --timeout) need_arg "$@"; timeout_ms="$2"; shift 2 ;;
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
# The subshell is what gets 2>/dev/null: a FAILED redirection is reported by the
# shell itself, before the command's own stderr redirection applies, so
# `printf ... > /dev/tty 2>/dev/null` still leaks "No such device or address"
# when there is no controlling tty (which is exactly how an async hook runs).
if [[ -n ${TMUX:-} ]]; then
    (printf '\a' > /dev/tty) 2>/dev/null || true
fi

if [[ $force != 1 ]] && focused; then
    exit 0
fi

if [[ -n $summary ]] && command -v notify-send >/dev/null 2>&1; then
    notify_args=()
    [[ -n $urgency ]] && notify_args+=("--urgency=$urgency")
    [[ -n $timeout_ms ]] && notify_args+=("--expire-time=$timeout_ms")
    notify_args+=("$summary")
    [[ -n $body ]] && notify_args+=("$body")
    notify-send "${notify_args[@]}" || true
fi

if player=$(command -v pw-play 2>/dev/null) || player=$(command -v paplay 2>/dev/null); then
    # pw-play takes a 0.0-1.0 float; paplay wants an integer 0-65536.
    if [[ ${player##*/} == paplay ]]; then
        volume=$(awk -v v="$volume" 'BEGIN { printf "%d", v * 65536 }')
    fi
    "$player" "--volume=$volume" "$sound" >/dev/null 2>&1 || true
fi

exit 0
