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
chmod +x "$TMP/bin/agent-manager" "$TMP/bin/claude"

export STUB_DIR="$TMP/state"
export PATH="$TMP/bin:$PATH"
export ZEN_RESUME_PROBE_WAIT=0

ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
check() { if [ "$2" = 0 ]; then ok "$1"; else bad "$1"; fi; }
reset() { rm -rf "$STUB_DIR"; mkdir -p "$STUB_DIR"; }
calls() { cat "$STUB_DIR/calls" 2>/dev/null; }

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

echo
if [ "$FAILS" = 0 ]; then
    echo "all passed"
else
    echo "$FAILS failed"
    exit 1
fi
