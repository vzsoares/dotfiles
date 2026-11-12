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

echo
if [ "$FAILS" = 0 ]; then
    echo "all passed"
else
    echo "$FAILS failed"
    exit 1
fi
