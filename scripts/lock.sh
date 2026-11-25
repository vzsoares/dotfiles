#!/usr/bin/env bash
set -e

# zen-lock <name> [--wait SECS] -- <cmd...>   run cmd holding ~/.cache/zen/<name>.lock
#                                             full output goes to a log; prints its last
#                                             40 lines and an exit/waited/ran/log line
# zen-lock status <name>                      show the holder, then the queue in order
# zen-lock break <name>                       remove the lock by hand
#
# The lock is a directory taken with mkdir (atomic, no flock on macOS) holding an
# `owner` file whose first line is "<session> <date>", the format of the hand-made
# locks. A lock without a pid line (hand-made) is never treated as stale.
#
# Waiters queue FIFO: each drops a ticket "<arrival>.<pid>" in <name>.queue/ and only
# the oldest live ticket may take the lock. A ticket whose pid is gone is pruned by
# the next poll of any waiter. A waiter from an older zen-lock has no ticket and is
# not ordered.

BASE="${ZEN_LOCK_DIR:-$HOME/.cache/zen}"
POLL="${ZEN_LOCK_POLL:-5}"
DEFAULT_WAIT=1800
EX_TIMEOUT=75
TAIL_LINES=40
LOG_KEEP_DAYS=7

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

field() { sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1; }
owner_field() { field "$1/owner" "$2"; }

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

# A ticket is stale when its pid is gone on this host, or it is unreadable (tickets
# are written whole and renamed into place).
ticket_stale() {
    local pid host
    pid=$(field "$1" pid)
    host=$(field "$1" host)
    [ -z "$pid" ] || { [ "$host" = "$(hostname)" ] && ! kill -0 "$pid" 2>/dev/null; }
}

# Microseconds since the epoch, zero-padded so tickets sort by name.
arrival() {
    local t
    t=$(perl -MTime::HiRes=time -e 'printf "%016d", time * 1e6' 2>/dev/null) || t=
    [ -n "$t" ] || t=$(printf '%016d' $(($(date +%s) * 1000000)))
    echo "$t"
}

enqueue() {
    local tmp
    mkdir -p "$QUEUE"
    TICKET="$QUEUE/$(arrival).$$"
    tmp="$QUEUE/.${TICKET##*/}"
    {
        echo "session=${AGENT_MANAGER_SESSION_ID:-}"
        echo "pid=$$"
        echo "host=$(hostname)"
        echo "started=$(date +%s)"
        echo "cmd=$(printf '%s' "$CMDLINE" | tr '\t\n' '  ' | cut -c1-200)"
    } >"$tmp"
    mv "$tmp" "$TICKET"
}

# True when this waiter holds the oldest live ticket; prunes dead tickets on the way.
is_head() {
    local t
    for t in "$QUEUE"/[0-9]*; do
        [ "$t" = "$TICKET" ] && return 0
        [ -e "$t" ] || continue
        if ticket_stale "$t"; then
            rm -f "$t"
            continue
        fi
        return 1
    done
    return 1
}

HELD=
TICKET=
QUEUE=
CHILD=
NAME=
LOG=
WAITED=0
RAN_FROM=0

# Tab-separated, one printf >> under PIPE_BUF, so concurrent writers never interleave.
record_run() {
    local status=$1 ran=$2 cmd
    cmd=$(printf '%s' "$3" | tr '\t\n' '  ' | cut -c1-200)
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$NAME" \
        "${AGENT_MANAGER_SESSION_ID:-}" "$WAITED" "$ran" "$status" "$cmd" \
        >>"$BASE/lock-stats.tsv" 2>/dev/null || true
}

finish() {
    local status=$1 ran=$((SECONDS - RAN_FROM))
    tail -n "$TAIL_LINES" "$LOG" 2>/dev/null || true
    # Output without a trailing newline would swallow the summary line.
    if [ -s "$LOG" ] && [ -n "$(tail -c 1 "$LOG")" ]; then echo; fi
    echo "zen-lock: $NAME exit $status · waited ${WAITED}s · ran ${ran}s · log $LOG"
    record_run "$status" "$ran" "$CMDLINE"
}

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
    [ -z "$TICKET" ] || rm -f "$TICKET"
    TICKET=
}

on_signal() {
    if [ -n "$CHILD" ]; then
        kill -s "$1" -- "-$CHILD" 2>/dev/null || true
        wait "$CHILD" 2>/dev/null || true
        CHILD=
        finish "$2"
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
    local t n=0 started
    for t in "$BASE/$1.queue"/[0-9]*; do
        [ -e "$t" ] || continue
        ticket_stale "$t" && continue
        n=$((n + 1))
        [ "$n" -gt 1 ] || echo "queue:"
        started=$(field "$t" started)
        echo "  $n. $(field "$t" session | sed 's/^$/-/') waiting $(($(date +%s) - ${started:-0}))s: $(field "$t" cmd)"
    done
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

    mkdir -p "$BASE/lock-logs"
    NAME=$name
    CMDLINE=$*
    local lock="$BASE/$name.lock" last="" holder
    QUEUE="$BASE/$name.queue"
    trap release EXIT
    trap 'on_signal INT 130' INT
    trap 'on_signal TERM 143' TERM
    trap 'on_signal HUP 129' HUP
    enqueue
    SECONDS=0
    until is_head && mkdir "$lock" 2>/dev/null; do
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
    rm -f "$TICKET"
    TICKET=
    WAITED=$SECONDS
    RAN_FROM=$SECONDS
    find "$BASE/lock-logs" -type f -name '*.log' -mtime +"$LOG_KEEP_DAYS" -delete 2>/dev/null || true
    LOG=$(mktemp "$BASE/lock-logs/$name-$(date +%Y%m%d-%H%M%S).XXXXXX") || exit 1
    mv "$LOG" "$LOG.log"
    LOG=$LOG.log

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
    "$@" 0<&0 >"$LOG" 2>&1 &
    CHILD=$!
    wait "$CHILD" || status=$?
    CHILD=
    finish "$status"
    exit "$status"
}

[ $# -gt 0 ] || usage
case "$1" in
    -h | --help) usage ;;
    status) [ $# -eq 2 ] || usage; cmd_status "$2" ;;
    break) [ $# -eq 2 ] || usage; cmd_break "$2" ;;
    *) cmd_run "$@" ;;
esac
