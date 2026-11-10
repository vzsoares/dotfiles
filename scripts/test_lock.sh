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
