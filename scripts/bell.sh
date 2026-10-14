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
#   zen-bell <sound> [-f] [-N] [-v N] [-n SUMMARY] [-b BODY] [-u URGENCY] [-t MS]
#
#   <sound>  a freedesktop sound name (complete, dialog-error, window-attention)
#            or an absolute path to a sound file. On macOS the names map to
#            system sounds (Glass, Basso, Ping).
#   -f       play even if the terminal is focused
#   -N       raise the notification even if the terminal is focused;
#            the sound stays gated
#   -v N     volume, 0.0 - 1.0 (default 0.4, or $ZEN_BELL_VOLUME)
#   -n TEXT  also raise a desktop notification with this summary
#   -b TEXT  body text for the notification (needs -n)
#   -u LEVEL notification urgency: low | normal | critical  (Linux only)
#            critical does not auto-dismiss — use it for "needs your input"
#   -t MS    notification expire time in milliseconds; 0 = never expires.
#            Overrides the dunst per-urgency timeout (urgency_normal is 10s).
#            Ignored on macOS, where Notification Center owns the timing.
#
# The notification is gated by the same focus check as the sound: if you are
# looking at the terminal, neither fires. Pass -f to always notify.
#
# Linux: notify-send + pw-play/paplay, focus via xdotool.
# macOS: terminal-notifier (if installed) or osascript + afplay, focus via
#        lsappinfo. No extra install needed.
#
# Exits 0 whether or not a sound was played — this is feedback, never a failure.

set -euo pipefail

volume="${ZEN_BELL_VOLUME:-0.4}"
force="${ZEN_BELL_WHEN_FOCUSED:-0}"
notify_always=0
sound=""
summary=""
body=""
urgency=""
timeout_ms=""

[[ $(uname -s) == Darwin ]] && macos=1 || macos=0

# Options taking a value must actually have one; `shift 2` past the end would
# abort under `set -u` with a confusing error.
need_arg() {
    [[ $# -ge 2 ]] || { echo "zen-bell: $1 needs a value" >&2; exit 2; }
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -f | --force) force=1; shift ;;
        -N | --notify-always) notify_always=1; shift ;;
        -v | --volume) need_arg "$@"; volume="$2"; shift 2 ;;
        -n | --notify) need_arg "$@"; summary="$2"; shift 2 ;;
        -b | --body) need_arg "$@"; body="$2"; shift 2 ;;
        -u | --urgency) need_arg "$@"; urgency="$2"; shift 2 ;;
        -t | --timeout) need_arg "$@"; timeout_ms="$2"; shift 2 ;;
        -h | --help) sed -n '2,33p' "$0" | sed 's/^# \?//'; exit 0 ;;
        -*) echo "zen-bell: unknown option $1" >&2; exit 2 ;;
        *) sound="$1"; shift ;;
    esac
done

if [[ -z $sound ]]; then
    echo "zen-bell: no sound given (try: zen-bell complete)" >&2
    exit 2
fi

# Bare names resolve against the platform theme; paths are used as-is.
if [[ $sound != /* ]]; then
    if (( macos )); then
        case "$sound" in
            complete) sound=Glass ;;
            dialog-error) sound=Basso ;;
            window-attention) sound=Ping ;;
        esac
        sound="/System/Library/Sounds/$sound"
        [[ $sound == *.aiff ]] || sound="$sound.aiff"
    else
        sound="/usr/share/sounds/freedesktop/stereo/$sound"
        [[ $sound == *.oga ]] || sound="$sound.oga"
    fi
fi

# --- focus detection -------------------------------------------------------

# Print the parent pid of $1. /proc avoids forking a `ps` per hop on Linux.
ppid_of() {
    if (( macos )); then
        local out
        out=$(ps -o ppid= -p "$1") || return 1
        printf '%s\n' "${out// /}"
        return
    fi
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

# Print the .app bundle name (without ".app") of the first ancestor of pid $1
# that runs out of a bundle — the terminal emulator.
app_of() {
    local pid="$1" comm n=0
    while [[ -n $pid && $pid != 0 && $pid != 1 ]] && ((n++ < 12)); do
        comm=$(ps -o comm= -p "$pid") || return 1
        if [[ $comm == *.app/Contents/MacOS/* ]]; then
            comm=${comm%%.app/Contents/MacOS/*}
            printf '%s\n' "${comm##*/}"
            return 0
        fi
        pid=$(ppid_of "$pid") || return 1
    done
    return 1
}

# Pid that sits inside the terminal window: us, or inside tmux the tmux client
# (our parent chain leads to the tmux *server*, which is nobody's child).
# Returns 1 when the tmux pane is not the visible one — never "focused".
anchor_pid() {
    if [[ -z ${TMUX:-} ]]; then
        printf '%s\n' "$$"
        return 0
    fi
    local vis anchor
    vis=$(tmux display-message -p -t "${TMUX_PANE:-}" \
        '#{&&:#{pane_active},#{window_active}}' 2>/dev/null) || return 1
    [[ $vis == 1 ]] || return 1
    anchor=$(tmux display-message -p -t "${TMUX_PANE:-}" \
        '#{client_pid}' 2>/dev/null) || return 1
    [[ $anchor =~ ^[0-9]+$ ]] || return 1
    printf '%s\n' "$anchor"
}

# True when the caller is running in the window the user is actually looking at.
# Fails closed-to-noisy: any uncertainty (Wayland, no xdotool, X error) reports
# "not focused" so the sound plays. A broken check must never eat a notification.
focused() {
    local anchor
    anchor=$(anchor_pid) || return 1

    if (( macos )); then
        # lsappinfo needs no Automation permission, unlike System Events.
        # Prints: "LSDisplayName"="Alacritty"
        local front mine
        front=$(lsappinfo info -only name "$(lsappinfo front)" 2>/dev/null) || return 1
        front=${front#*=}; front=${front//\"/}
        mine=$(app_of "$anchor") || return 1
        # tr, not ${x,,}: macOS /bin/bash is 3.2 and lacks case conversion.
        [[ $(tr '[:upper:]' '[:lower:]' <<<"$front") == "$(tr '[:upper:]' '[:lower:]' <<<"$mine")" ]]
        return
    fi

    [[ -n ${DISPLAY:-} ]] || return 1
    command -v xdotool >/dev/null 2>&1 || return 1

    local fpid
    fpid=$(xdotool getactivewindow getwindowpid 2>/dev/null) || return 1
    [[ $fpid =~ ^[0-9]+$ ]] || return 1

    is_ancestor "$anchor" "$fpid"
}

# --- notify ----------------------------------------------------------------

notify() {
    if (( macos )); then
        if command -v terminal-notifier >/dev/null 2>&1; then
            terminal-notifier -title "$summary" -message "${body:-$summary}" \
                -group zen-bell >/dev/null 2>&1 || true
        else
            # Sent as "Script Editor" in Notification Center; allow it there once.
            osascript -e 'on run argv' \
                -e 'display notification (item 2 of argv) with title (item 1 of argv)' \
                -e 'end run' -- "$summary" "$body" >/dev/null 2>&1 || true
        fi
        return
    fi
    command -v notify-send >/dev/null 2>&1 || return 0
    local args=()
    [[ -n $urgency ]] && args+=("--urgency=$urgency")
    [[ -n $timeout_ms ]] && args+=("--expire-time=$timeout_ms")
    args+=("$summary")
    [[ -n $body ]] && args+=("$body")
    notify-send "${args[@]}" || true
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

is_focused=0
if [[ $force != 1 ]] && focused; then
    is_focused=1
fi

if [[ -n $summary ]] && { (( ! is_focused )) || (( notify_always )); }; then
    notify
fi

(( is_focused )) && exit 0

if (( macos )); then
    afplay -v "$volume" "$sound" >/dev/null 2>&1 || true
elif player=$(command -v pw-play 2>/dev/null) || player=$(command -v paplay 2>/dev/null); then
    # pw-play takes a 0.0-1.0 float; paplay wants an integer 0-65536.
    if [[ ${player##*/} == paplay ]]; then
        volume=$(awk -v v="$volume" 'BEGIN { printf "%d", v * 65536 }')
    fi
    "$player" "--volume=$volume" "$sound" >/dev/null 2>&1 || true
fi

exit 0
