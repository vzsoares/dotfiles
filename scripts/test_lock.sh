#!/usr/bin/env bash
# Tests for scripts/lock.sh. Run with:  bash scripts/test_lock.sh
# Uses a temp lock dir; never touches ~/.cache/zen.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOCK="$HERE/lock.sh"
TMP="$(mktemp -d)"
export ZEN_LOCK_DIR="$TMP/locks"
export ZEN_LOCK_POLL=0.1
FAILS=0

trap 'rm -rf "$TMP"' EXIT

ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
check() { # name, condition result
    if [ "$2" = 0 ]; then ok "$1"; else bad "$1"; fi
}
reset() { rm -rf "$ZEN_LOCK_DIR"; mkdir -p "$ZEN_LOCK_DIR"; }
wait_for() { # path, tries of 0.1s
    local i=0
    while [ ! -e "$1" ] && [ "$i" -lt "${2:-50}" ]; do sleep 0.1; i=$((i + 1)); done
    [ -e "$1" ]
}

# acquire and release, exit code passthrough
reset
bash "$LOCK" t -- test -d "$ZEN_LOCK_DIR/t.lock"
check "lock is held while the command runs" $?
[ ! -d "$ZEN_LOCK_DIR/t.lock" ]
check "lock is released after the command" $?
bash "$LOCK" t -- bash -c 'exit 7'
[ $? = 7 ]
check "command exit code passes through" $?
[ ! -d "$ZEN_LOCK_DIR/t.lock" ]
check "lock is released after a failing command" $?

# owner file keeps the hand-made first-line format and adds fields
reset
AGENT_MANAGER_SESSION_ID=sess1 bash "$LOCK" t -- cat "$ZEN_LOCK_DIR/t.lock/owner" >"$TMP/owner"
head -n 1 "$TMP/owner" | grep -Eq '^sess1 .+'
check "owner first line is '<session> <date>'" $?
grep -q '^pid=[0-9]' "$TMP/owner" && grep -q '^host=' "$TMP/owner" && grep -q '^cmd=cat ' "$TMP/owner"
check "owner file has pid, host and cmd" $?

# timeout while held by a live hand-made lock (no pid line: never stale)
reset
mkdir "$ZEN_LOCK_DIR/t.lock"
echo "task-1 $(date)" >"$ZEN_LOCK_DIR/t.lock/owner"
bash "$LOCK" t --wait 1 -- true 2>"$TMP/err"
[ $? = 75 ]
check "exit 75 on timeout" $?
grep -q 'task-1' "$TMP/err"
check "waiting message names the holder" $?
[ -d "$ZEN_LOCK_DIR/t.lock" ]
check "hand-made lock is left alone" $?

# wait then acquire
reset
bash "$LOCK" t -- sleep 1 &
wait_for "$ZEN_LOCK_DIR/t.lock/owner"
bash "$LOCK" t --wait 10 -- touch "$TMP/got"
check "waiter acquires after the holder releases" $?
[ -e "$TMP/got" ]
check "waiter ran its command" $?
wait

# stale takeover: owner pid is gone
reset
mkdir "$ZEN_LOCK_DIR/t.lock"
sleep 0 &
DEAD=$!
wait "$DEAD"
printf 'old %s\npid=%s\nhost=%s\n' "$(date)" "$DEAD" "$(hostname)" >"$ZEN_LOCK_DIR/t.lock/owner"
bash "$LOCK" t --wait 5 -- true 2>"$TMP/err"
check "stale lock is taken over" $?
grep -q 'stale' "$TMP/err"
check "takeover is announced" $?

# a live owner pid is not stale
reset
mkdir "$ZEN_LOCK_DIR/t.lock"
printf 'live %s\npid=%s\nhost=%s\n' "$(date)" "$$" "$(hostname)" >"$ZEN_LOCK_DIR/t.lock/owner"
bash "$LOCK" t --wait 1 -- true 2>/dev/null
[ $? = 75 ]
check "lock held by a live pid is not taken over" $?

make_stale_lock() {
    reset
    mkdir "$ZEN_LOCK_DIR/t.lock"
    sleep 0 &
    DEAD=$!
    wait "$DEAD"
    printf 'old %s\npid=%s\nhost=%s\n' "$(date)" "$DEAD" "$(hostname)" >"$ZEN_LOCK_DIR/t.lock/owner"
}
make_guard() { # started offset in seconds, pid
    mkdir "$ZEN_LOCK_DIR/t.lock.reap"
    printf 'pid=%s\nhost=%s\nstarted=%s\n' "$2" "$(hostname)" "$(($(date +%s) - $1))" >"$ZEN_LOCK_DIR/t.lock.reap/owner"
}

# waiter SIGKILLed mid-takeover: a stub rm holds it inside the reap section
# (it sleeps only on the lock removal, not the guard removal)
mkdir "$TMP/fakebin"
{
    echo '#!/bin/sh'
    echo "case \"\$*\" in *t.lock) echo \$\$ >\"$TMP/rmpid\"; sleep 30 ;; esac"
    echo 'exec /bin/rm "$@"'
} >"$TMP/fakebin/rm"
chmod +x "$TMP/fakebin/rm"
make_stale_lock
PATH="$TMP/fakebin:$PATH" bash "$LOCK" t --wait 30 -- true 2>/dev/null &
KILLED=$!
wait_for "$TMP/rmpid"
kill -9 "$KILLED"
wait "$KILLED" 2>/dev/null
kill "$(cat "$TMP/rmpid")" 2>/dev/null
[ -d "$ZEN_LOCK_DIR/t.lock.reap" ]
check "SIGKILL mid-takeover leaves the reap guard" $?
bash "$LOCK" status t | grep -q 'reap guard present (stale)'
check "status reports the stale guard" $?
bash "$LOCK" t --wait 5 -- true 2>/dev/null
check "stale lock is taken over despite the leftover guard" $?
[ ! -d "$ZEN_LOCK_DIR/t.lock.reap" ]
check "reap guard is gone after the takeover" $?

# guard older than 60s held by a live pid
make_stale_lock
make_guard 120 "$$"
bash "$LOCK" t --wait 5 -- true 2>/dev/null
check "guard older than 60s is removed" $?

# guard with no owner file (killed right after mkdir) and an old mtime
make_stale_lock
mkdir "$ZEN_LOCK_DIR/t.lock.reap"
touch -t 200001010000 "$ZEN_LOCK_DIR/t.lock.reap"
bash "$LOCK" t --wait 5 -- true 2>/dev/null
check "ownerless guard with an old mtime is removed" $?

# fresh guard held by a live process is never removed
make_stale_lock
make_guard 0 "$$"
bash "$LOCK" t --wait 1 -- true 2>/dev/null
[ $? = 75 ]
check "fresh live guard blocks the takeover (exit 75)" $?
[ -d "$ZEN_LOCK_DIR/t.lock.reap" ] && [ -d "$ZEN_LOCK_DIR/t.lock" ]
check "fresh live guard and stale lock are left alone" $?

# a stale guard never makes a hand-made lock stale
reset
mkdir "$ZEN_LOCK_DIR/t.lock"
echo "task-3 $(date)" >"$ZEN_LOCK_DIR/t.lock/owner"
make_guard 120 999999
bash "$LOCK" t --wait 1 -- true 2>/dev/null
[ $? = 75 ] && [ -d "$ZEN_LOCK_DIR/t.lock" ]
check "hand-made lock stays with a stale guard around" $?

# status and break
reset
bash "$LOCK" status t | grep -q free
check "status reports a free lock" $?
mkdir "$ZEN_LOCK_DIR/t.lock"
echo "task-2 now" >"$ZEN_LOCK_DIR/t.lock/owner"
bash "$LOCK" status t | grep -q task-2
check "status shows the holder" $?
bash "$LOCK" break t >/dev/null
[ ! -d "$ZEN_LOCK_DIR/t.lock" ]
check "break removes the lock" $?
mkdir "$ZEN_LOCK_DIR/t.lock" "$ZEN_LOCK_DIR/t.lock.reap"
bash "$LOCK" break t >/dev/null
[ ! -d "$ZEN_LOCK_DIR/t.lock" ] && [ ! -d "$ZEN_LOCK_DIR/t.lock.reap" ]
check "break removes the reap guard too" $?
reset
mkdir "$ZEN_LOCK_DIR/t.lock.reap"
bash "$LOCK" break t >/dev/null
[ ! -d "$ZEN_LOCK_DIR/t.lock.reap" ]
check "break removes a guard when the lock is free" $?

# release on signals. Job control: an async job in a plain script ignores SIGINT.
set -m
for sig in INT TERM; do
    reset
    bash "$LOCK" t -- sleep 30 &
    pid=$!
    wait_for "$ZEN_LOCK_DIR/t.lock/owner"
    kill -s "$sig" "$pid"
    wait "$pid" 2>/dev/null
    [ ! -d "$ZEN_LOCK_DIR/t.lock" ]
    check "lock released on SIG$sig" $?
done

set +m

# concurrent pair: only one holds the lock at a time
reset
: >"$TMP/log"
for i in 1 2; do
    bash "$LOCK" t --wait 30 -- bash -c "echo start $i >>'$TMP/log'; sleep 0.5; echo end $i >>'$TMP/log'" &
done
wait
[ "$(wc -l <"$TMP/log" | tr -d ' ')" = 4 ] &&
    [ "$(sed -n '1p' "$TMP/log" | cut -d' ' -f2)" = "$(sed -n '2p' "$TMP/log" | cut -d' ' -f2)" ] &&
    [ "$(sed -n '1p' "$TMP/log" | cut -d' ' -f1)" = start ] &&
    [ "$(sed -n '2p' "$TMP/log" | cut -d' ' -f1)" = end ]
check "concurrent runs do not overlap" $?

# bad usage
bash "$LOCK" 2>/dev/null
[ $? = 2 ]
check "no args is a usage error" $?
bash "$LOCK" ../x -- true 2>/dev/null
[ $? = 2 ]
check "path-like name is rejected" $?

[ "$FAILS" = 0 ] && echo "all passed" || { echo "$FAILS failed"; exit 1; }
