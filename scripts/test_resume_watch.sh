#!/usr/bin/env bash
# Tests for scripts/resume-watch.sh. Run with:  bash scripts/test_resume_watch.sh
# agent-manager and claude are stubs on PATH; the real ones are never called.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCH="$HERE/resume-watch.sh"
TMP="$(mktemp -d)"
FAILS=0

trap 'rm -rf "$TMP"' EXIT

mkdir "$TMP/bin"
cat >"$TMP/bin/agent-manager" <<'STUB'
#!/usr/bin/env bash
case "$1" in
sessions) cat "$STUB_DIR/sessions.json" ;;
read) cat "$STUB_DIR/screen-$2" 2>/dev/null ;;
send)
	echo "$*" >>"$STUB_DIR/calls"
	n=$(cat "$STUB_DIR/sends" 2>/dev/null || echo 0)
	echo $((n + 1)) >"$STUB_DIR/sends"
	[ "$n" -ge "${STUB_SEND_FAILS:-0}" ]
	;;
*) echo "$*" >>"$STUB_DIR/calls" ;;
esac
STUB
# Fails the first STUB_PROBE_FAILS probes, then passes.
cat >"$TMP/bin/claude" <<'STUB'
#!/usr/bin/env bash
n=$(cat "$STUB_DIR/probes" 2>/dev/null || echo 0)
echo $((n + 1)) >"$STUB_DIR/probes"
if [ "$n" -lt "${STUB_PROBE_FAILS:-0}" ]; then echo "usage limit reached"; else echo OK; fi
STUB
# Records the probe count at each tick; STUB_ROTATE_FAIL makes it fail.
cat >"$TMP/bin/zen-rotate" <<'STUB'
#!/usr/bin/env bash
echo "$* probes=$(cat "$STUB_DIR/probes" 2>/dev/null || echo 0)" >>"$STUB_DIR/rotate"
if [ -n "${STUB_ROTATE_FAIL:-}" ]; then
	echo "zen-rotate: boom" >&2
	exit 1
fi
[ -z "${STUB_ROTATE_OUT:-}" ] || echo "$STUB_ROTATE_OUT"
STUB
# Never sleeps; with STUB_PASSES set it ends the watcher's loop after that many.
cat >"$TMP/bin/sleep" <<'STUB'
#!/usr/bin/env bash
[ -n "${STUB_PASSES:-}" ] || exit 0
n=$(cat "$STUB_DIR/sleeps" 2>/dev/null || echo 0)
echo $((n + 1)) >"$STUB_DIR/sleeps"
[ $((n + 1)) -lt "$STUB_PASSES" ] || kill -TERM "$PPID"
STUB
# Records each call; STUB_INBOX_FAIL makes it fail. Never the real zen-inbox,
# which would scan the live db.
cat >"$TMP/bin/zen-inbox" <<'STUB'
#!/usr/bin/env bash
echo "$*" >>"$STUB_DIR/inbox"
if [ -n "${STUB_INBOX_FAIL:-}" ]; then
	echo "zen-inbox: db unavailable" >&2
	exit 4
fi
echo "scanned 3 messages · 0 open entries · 0 false drops this week"
STUB
chmod +x "$TMP/bin/agent-manager" "$TMP/bin/claude" "$TMP/bin/zen-rotate" "$TMP/bin/sleep" "$TMP/bin/zen-inbox"

export STUB_DIR="$TMP/state"
export PATH="$TMP/bin:$PATH"
export ZEN_RESUME_PROBE_WAIT=0

ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
check() { if [ "$2" = 0 ]; then ok "$1"; else bad "$1"; fi; }
reset() { rm -rf "$STUB_DIR"; mkdir -p "$STUB_DIR"; }
calls() { cat "$STUB_DIR/calls" 2>/dev/null; }
ticks() { cat "$STUB_DIR/rotate" 2>/dev/null; }
scans() { grep -c '^delivery scan$' "$STUB_DIR/inbox" 2>/dev/null || echo 0; }

LIMIT_SCREEN="You've hit your usage limit. Your limit will reset at 3pm."

bash "$WATCH" --help | grep -q '^usage: zen-resume-watch'
check "--help prints usage" $?
bash "$WATCH" --bogus >/dev/null 2>&1
if [ $? = 2 ]; then ok "unknown flag exits 2"; else bad "unknown flag exits 2"; fi

reset
echo '{"sessions":[{"id":"c1","name":"chief","tool":"claude","running":false,"archived":false}]}' >"$STUB_DIR/sessions.json"
bash "$WATCH" --once >/dev/null
if [ "$(calls)" = "revive c1" ]; then ok "dead chief is revived"; else bad "dead chief is revived"; fi

reset
echo '[{"id":"c1","name":"chief","tool":"claude","running":false,"archived":true},
{"id":"c2","name":"chief","tool":"claude","running":true,"archived":false},
{"id":"w1","name":"worker","tool":"claude","running":false,"archived":false}]' >"$STUB_DIR/sessions.json"
bash "$WATCH" --once >/dev/null
if [ -z "$(calls)" ]; then ok "archived, live and non-chief sessions are left alone"; else bad "archived, live and non-chief sessions are left alone"; fi

reset
echo '[{"id":"w1","name":"worker","tool":"claude","running":true,"archived":false}]' >"$STUB_DIR/sessions.json"
echo "$LIMIT_SCREEN" >"$STUB_DIR/screen-w1"
bash "$WATCH" --once >"$TMP/out"
if [ -z "$(calls)" ] && grep -q 'probe passed' "$TMP/out"; then ok "limit screen with a passing probe sends nothing"; else bad "limit screen with a passing probe sends nothing"; fi

reset
echo '[{"id":"w1","name":"worker","tool":"claude","running":true,"archived":false},
{"id":"w2","name":"worker","tool":"claude","running":true,"archived":false}]' >"$STUB_DIR/sessions.json"
echo "$LIMIT_SCREEN" >"$STUB_DIR/screen-w1"
echo "all quiet" >"$STUB_DIR/screen-w2"
STUB_PROBE_FAILS=2 bash "$WATCH" --once >/dev/null
if [ "$(calls)" = "send w1 The Claude usage limit has reset. Continue exactly where you left off." ]; then ok "failing then passing probe resumes only the limited session"; else bad "failing then passing probe resumes only the limited session"; fi

SEND="send w1 The Claude usage limit has reset. Continue exactly where you left off."
RUNNING='[{"id":"w1","name":"worker","tool":"claude","running":true,"archived":false}]'

reset
echo "$RUNNING" >"$STUB_DIR/sessions.json"
echo "$LIMIT_SCREEN" >"$STUB_DIR/screen-w1"
export ZEN_RESUME_STATE="$TMP/watch-state"
rm -rf "$ZEN_RESUME_STATE"
STUB_PROBE_FAILS=1 STUB_SEND_FAILS=1 bash "$WATCH" --once >"$TMP/out1"
STUB_PROBE_FAILS=1 STUB_SEND_FAILS=1 bash "$WATCH" --once >"$TMP/out2"
if grep -q 'could not message w1' "$TMP/out1" && grep -q 'resumed w1' "$TMP/out2" \
	&& [ "$(calls)" = "$SEND
$SEND" ] && [ "$(cat "$STUB_DIR/probes")" = 2 ]; then ok "failed send is retried without a new probe"; else bad "failed send is retried without a new probe"; fi

reset
echo "$RUNNING" >"$STUB_DIR/sessions.json"
echo "$LIMIT_SCREEN" >"$STUB_DIR/screen-w1"
rm -rf "$ZEN_RESUME_STATE"
STUB_PROBE_FAILS=1 STUB_SEND_FAILS=1 bash "$WATCH" --once >/dev/null
echo '[{"id":"w9","name":"other","tool":"claude","running":true,"archived":false}]' >"$STUB_DIR/sessions.json"
STUB_SEND_FAILS=1 bash "$WATCH" --once >"$TMP/out"
if grep -q 'dropped w1: not running' "$TMP/out" && [ "$(calls)" = "$SEND" ]; then ok "pending id that stopped running is dropped"; else bad "pending id that stopped running is dropped"; fi

reset
echo "$RUNNING" >"$STUB_DIR/sessions.json"
echo "$LIMIT_SCREEN" >"$STUB_DIR/screen-w1"
rm -rf "$ZEN_RESUME_STATE"
export STUB_SEND_FAILS=99
STUB_PROBE_FAILS=1 bash "$WATCH" --once >/dev/null
for _ in 1 2 3; do bash "$WATCH" --once >>"$TMP/out3"; done
bash "$WATCH" --once >>"$TMP/out3"
if [ "$(grep -c 'giving up on w1' "$TMP/out3")" = 1 ] && [ "$(calls | wc -l | tr -d ' ')" = 4 ]; then ok "gives up after 3 retries and sends nothing more"; else bad "gives up after 3 retries and sends nothing more"; fi
unset STUB_SEND_FAILS ZEN_RESUME_STATE

IDLE='[{"id":"w1","name":"worker","tool":"claude","running":true,"archived":false}]'

reset
echo "$IDLE" >"$STUB_DIR/sessions.json"
STUB_ROTATE_OUT="12:00 rotated orchestrator" bash "$WATCH" --once >"$TMP/out"
if [ "$(ticks)" = "tick probes=0" ] && grep -qx '12:00 rotated orchestrator' "$TMP/out"; then ok "--once runs zen-rotate tick once and shows its output"; else bad "--once runs zen-rotate tick once and shows its output"; fi

reset
echo "$IDLE" >"$STUB_DIR/sessions.json"
STUB_PASSES=3 bash "$WATCH" >/dev/null
if [ "$(ticks | wc -l | tr -d ' ')" = 3 ] && [ "$(cat "$STUB_DIR/sleeps")" = 3 ]; then ok "the loop ticks once per minute pass"; else bad "the loop ticks once per minute pass"; fi

reset
echo "$IDLE" >"$STUB_DIR/sessions.json"
echo "$LIMIT_SCREEN" >"$STUB_DIR/screen-w1"
STUB_PROBE_FAILS=3 bash "$WATCH" --once >/dev/null
if [ "$(ticks)" = "tick probes=4" ]; then ok "no tick while a confirmed limit is waited out"; else bad "no tick while a confirmed limit is waited out"; fi

reset
echo '{"sessions":[{"id":"c1","name":"chief","tool":"claude","running":false,"archived":false}]}' >"$STUB_DIR/sessions.json"
STUB_ROTATE_FAIL=1 STUB_PASSES=2 bash "$WATCH" >"$TMP/out"
rc=$?
if [ "$rc" = 0 ] && [ "$(grep -c 'zen-rotate tick failed (exit 1): zen-rotate: boom$' "$TMP/out")" = 2 ] \
	&& [ "$(calls)" = "revive c1" ]; then ok "a failing tick prints one line per pass and the loop goes on"; else bad "a failing tick prints one line per pass and the loop goes on"; fi

reset
echo "$IDLE" >"$STUB_DIR/sessions.json"
mv "$TMP/bin/zen-rotate" "$TMP/zen-rotate.off"
PATH="$TMP/bin:/usr/bin:/bin" bash "$WATCH" --once >"$TMP/out"
rc=$?
mv "$TMP/zen-rotate.off" "$TMP/bin/zen-rotate"
if [ "$rc" = 0 ] && [ "$(grep -c 'zen-rotate tick failed (exit 127)' "$TMP/out")" = 1 ] \
	&& [ "$(wc -l <"$TMP/out" | tr -d ' ')" = 1 ]; then ok "a missing zen-rotate prints one line"; else bad "a missing zen-rotate prints one line"; fi

reset
echo "$IDLE" >"$STUB_DIR/sessions.json"
bash "$WATCH" --once >"$TMP/out"
if [ "$(scans)" = 1 ] && [ ! -s "$TMP/out" ]; then ok "--once runs one quiet delivery scan"; else bad "--once runs one quiet delivery scan"; fi

reset
echo "$IDLE" >"$STUB_DIR/sessions.json"
STUB_PASSES=11 bash "$WATCH" >/dev/null
if [ "$(scans)" = 3 ] && [ "$(ticks | wc -l | tr -d ' ')" = 11 ]; then ok "the loop scans deliveries every 5th pass"; else bad "the loop scans deliveries every 5th pass ($(scans) scans)"; fi

reset
echo "$IDLE" >"$STUB_DIR/sessions.json"
STUB_INBOX_FAIL=1 STUB_PASSES=6 bash "$WATCH" >"$TMP/out"
rc=$?
if [ "$rc" = 0 ] && [ "$(grep -c 'zen-inbox delivery scan failed (exit 4): zen-inbox: db unavailable$' "$TMP/out")" = 2 ] \
	&& [ "$(ticks | wc -l | tr -d ' ')" = 6 ]; then ok "a failing scan prints one line and the loop goes on"; else bad "a failing scan prints one line and the loop goes on"; fi

reset
echo "$IDLE" >"$STUB_DIR/sessions.json"
mv "$TMP/bin/zen-inbox" "$TMP/zen-inbox.off"
PATH="$TMP/bin:/usr/bin:/bin" bash "$WATCH" --once >"$TMP/out"
rc=$?
mv "$TMP/zen-inbox.off" "$TMP/bin/zen-inbox"
if [ "$rc" = 0 ] && [ "$(grep -c 'zen-inbox delivery scan failed (exit 127)' "$TMP/out")" = 1 ] \
	&& [ "$(wc -l <"$TMP/out" | tr -d ' ')" = 1 ]; then ok "a missing zen-inbox prints one line"; else bad "a missing zen-inbox prints one line"; fi

echo
if [ "$FAILS" = 0 ]; then
    echo "all passed"
else
    echo "$FAILS failed"
    exit 1
fi
