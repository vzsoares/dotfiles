#!/usr/bin/env bash
set -e

# zen-lock <name> [--wait SECS] -- <cmd...>   run cmd holding ~/.cache/zen/<name>.lock
# zen-lock status <name>                      show the holder
# zen-lock break <name>                       remove the lock by hand
#
# The lock is a directory taken with mkdir (atomic, no flock on macOS) holding an
# `owner` file whose first line is "<session> <date>", the format of the hand-made
# locks. A lock without a pid line (hand-made) is never treated as stale.

BASE="${ZEN_LOCK_DIR:-$HOME/.cache/zen}"
POLL="${ZEN_LOCK_POLL:-5}"
DEFAULT_WAIT=1800
EX_TIMEOUT=75

usage() {
    cat >&2 <<'EOF'
usage: zen-lock <name> [--wait SECS] -- <cmd...>
       zen-lock status <name>
       zen-lock break <name>

  --wait SECS  give up after SECS seconds (default 1800); exit 75 on timeout
EOF
    exit 2
}

check_name() {
    case "$1" in
        "" | .* | *[!A-Za-z0-9._-]*)
            echo "zen-lock: invalid lock name '$1'" >&2
            exit 2
            ;;
    esac
}

owner_field() { sed -n "s/^$2=//p" "$1/owner" 2>/dev/null | head -n 1; }

describe() {
    local line
    line=$(head -n 1 "$1/owner" 2>/dev/null) || true
    echo "${line:-unknown owner}"
}

# Owner pid on this host that no longer exists.
is_stale() {
    local pid host
    pid=$(owner_field "$1" pid)
    host=$(owner_field "$1" host)
    [ -n "$pid" ] && [ "$host" = "$(hostname)" ] && ! kill -0 "$pid" 2>/dev/null
}

GUARD_TTL=60

# A reap guard is stale when its owner is gone or it outlived the milliseconds the
# reap section takes. Without an owner file (killed right after mkdir) use its mtime.
guard_stale() {
    local started
    is_stale "$1" && return 0
    started=$(owner_field "$1" started)
    case "$started" in
        "" | *[!0-9]*) [ -z "$(owner_field "$1" pid)" ] && [ -n "$(find "$1" -maxdepth 0 -mmin +1 2>/dev/null)" ] ;;
        *) [ $(($(date +%s) - started)) -gt "$GUARD_TTL" ] ;;
    esac
}

# Serialised by a second mkdir lock so two waiters cannot remove a fresh lock.
# Remaining race: a stale guard removed by two waiters in the instant one re-creates it.
reap_stale() {
    local lock=$1 guard=$1.reap
    local reaped=1
    if ! mkdir "$guard" 2>/dev/null; then
        guard_stale "$guard" || return 1
        rm -rf "$guard"
        mkdir "$guard" 2>/dev/null || return 1
    fi
    printf 'pid=%s\nhost=%s\nstarted=%s\n' "$$" "$(hostname)" "$(date +%s)" >"$guard/owner"
    if [ -d "$lock" ] && is_stale "$lock"; then
        echo "zen-lock: taking over stale lock: $(describe "$lock") (pid $(owner_field "$lock" pid) gone)" >&2
        rm -rf "$lock"
        reaped=0
    fi
    rm -rf "$guard"
    return "$reaped"
}

HELD=
CHILD=

release() {
    # Never remove a lock someone else holds; an owner file not yet written is ours.
    local pid
    if [ -n "$HELD" ]; then
        pid=$(owner_field "$HELD" pid)
        if [ -z "$pid" ] || [ "$pid" = "$$" ]; then
            rm -rf "$HELD"
        fi
    fi
    HELD=
}

on_signal() {
    if [ -n "$CHILD" ]; then
        kill -s "$1" -- "-$CHILD" 2>/dev/null || true
        wait "$CHILD" 2>/dev/null || true
    fi
    exit "$2"
}

cmd_status() {
    check_name "$1"
    local lock="$BASE/$1.lock"
    if [ -d "$lock" ]; then
        cat "$lock/owner" 2>/dev/null || echo "held, no owner file"
        if is_stale "$lock"; then
            echo "stale: pid $(owner_field "$lock" pid) is gone"
        fi
    else
        echo "free"
    fi
    if [ -d "$lock.reap" ]; then
        echo "reap guard present$(guard_stale "$lock.reap" && echo " (stale)")"
    fi
}

cmd_break() {
    check_name "$1"
    local lock="$BASE/$1.lock"
    if [ -d "$lock" ]; then
        echo "zen-lock: breaking $1 held by: $(describe "$lock")"
    else
        echo "zen-lock: $1 is not held"
    fi
    rm -rf "$lock" "$lock.reap"
}

cmd_run() {
    local name=$1 wait=$DEFAULT_WAIT
    shift
    while [ $# -gt 0 ]; do
        case "$1" in
            --wait)
                [ $# -ge 2 ] || usage
                wait=$2
                shift 2
                ;;
            --) shift; break ;;
            *) usage ;;
        esac
    done
    [ $# -gt 0 ] || usage
    case "$wait" in "" | *[!0-9]*) usage ;; esac
    check_name "$name"

    mkdir -p "$BASE"
    local lock="$BASE/$name.lock" last="" holder
    trap release EXIT
    trap 'on_signal INT 130' INT
    trap 'on_signal TERM 143' TERM
    trap 'on_signal HUP 129' HUP
    SECONDS=0
    until mkdir "$lock" 2>/dev/null; do
        if reap_stale "$lock"; then
            continue
        fi
        holder=$(describe "$lock")
        if [ "$holder" != "$last" ]; then
            echo "zen-lock: waiting for $name, held by: $holder" >&2
            last=$holder
        fi
        if [ "$SECONDS" -ge "$wait" ]; then
            echo "zen-lock: timed out after ${wait}s waiting for $name" >&2
            exit "$EX_TIMEOUT"
        fi
        sleep "$POLL"
    done

    HELD=$lock

    {
        echo "${AGENT_MANAGER_SESSION_ID:-zen-lock} $(date)"
        echo "session=${AGENT_MANAGER_SESSION_ID:-}"
        echo "pid=$$"
        echo "host=$(hostname)"
        echo "started=$(date +%s)"
        echo "cmd=$*"
    } >"$lock/owner"

    # Own process group, so a forwarded signal reaches the whole command tree
    # and the trap runs while the command is still going.
    local status=0
    set -m
    "$@" 0<&0 &
    CHILD=$!
    wait "$CHILD" || status=$?
    CHILD=
    exit "$status"
}

[ $# -gt 0 ] || usage
case "$1" in
    -h | --help) usage ;;
    status) [ $# -eq 2 ] || usage; cmd_status "$2" ;;
    break) [ $# -eq 2 ] || usage; cmd_break "$2" ;;
    *) cmd_run "$@" ;;
esac
