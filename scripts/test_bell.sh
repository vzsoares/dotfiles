#!/usr/bin/env bash
# Tests for scripts/bell.sh --chief. Run with:  bash scripts/test_bell.sh
# agent-manager and the notifier/player binaries are stubs on PATH.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BELL="$HERE/bell.sh"
TMP="$(mktemp -d)"
FAILS=0

trap 'rm -rf "$TMP"' EXIT

mkdir "$TMP/bin" "$TMP/state" "$TMP/cfg"
for name in terminal-notifier notify-send osascript afplay pw-play paplay; do
	cat >"$TMP/bin/$name" <<STUB
#!/usr/bin/env bash
echo "$name \$*" >>"\$STUB_DIR/calls"
STUB
done
cat >"$TMP/bin/agent-manager" <<'STUB'
#!/usr/bin/env bash
cat "$STUB_DIR/sessions.json"
STUB
chmod +x "$TMP"/bin/*

export STUB_DIR="$TMP/state"
export PATH="$TMP/bin:$PATH"
export XDG_CONFIG_HOME="$TMP/cfg"
export ZEN_BELL_WHEN_FOCUSED=1
unset TMUX ZEN_BELL_OFF AGENT_MANAGER_SESSION_ID

cat >"$STUB_DIR/sessions.json" <<'JSON'
[
  {"id": "c1", "name": "chief", "archived": false},
  {"id": "c0", "name": "chief", "archived": true},
  {"id": "w1", "name": "worker-1", "archived": false}
]
JSON

ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
ncalls() {
	local n
	n=$(grep -cE "$1" "$STUB_DIR/calls" 2>/dev/null)
	echo "${n:-0}"
}
set_bell() {
	mkdir -p "$TMP/cfg/zen"
	echo "{\"bell\": {\"enabled\": $1}}" >"$TMP/cfg/zen/config.json"
}
run() {
	rm -f "$STUB_DIR/calls"
	bash "$BELL" complete "$@" -n Title -b Body
	RC=$?
}
expect() { # name notify-count sound-count
	local notif sound
	notif=$(ncalls '^(terminal-notifier|notify-send|osascript)')
	sound=$(ncalls '^(afplay|pw-play|paplay)')
	if [ "$RC" = 0 ] && [ "$notif" = "$2" ] && [ "$sound" = "$3" ]; then ok "$1"; else bad "$1 (rc=$RC notify=$notif sound=$sound)"; fi
}

set_bell false

AGENT_MANAGER_SESSION_ID=c1 run --chief
expect "chief, bell off: banner, no sound" 1 0

AGENT_MANAGER_SESSION_ID=w1 run --chief
expect "non-chief: nothing" 0 0

AGENT_MANAGER_SESSION_ID=c0 run --chief
expect "archived chief: nothing" 0 0

run --chief
expect "session id unset: nothing" 0 0

echo 'not json' >"$STUB_DIR/sessions.json.bad"
mv "$STUB_DIR/sessions.json" "$STUB_DIR/sessions.json.ok"
mv "$STUB_DIR/sessions.json.bad" "$STUB_DIR/sessions.json"
AGENT_MANAGER_SESSION_ID=c1 run --chief
expect "bad JSON: nothing, exit 0" 0 0
mv "$STUB_DIR/sessions.json.ok" "$STUB_DIR/sessions.json"

mv "$TMP/bin/agent-manager" "$TMP/agent-manager.off"
AGENT_MANAGER_SESSION_ID=c1 run --chief
expect "agent-manager missing: nothing" 0 0
mv "$TMP/agent-manager.off" "$TMP/bin/agent-manager"

AGENT_MANAGER_SESSION_ID=c1 run
expect "no --chief, bell off: nothing" 0 0

AGENT_MANAGER_SESSION_ID=c1 ZEN_BELL_OFF=1 run --chief
expect "ZEN_BELL_OFF=1: nothing" 0 0

set_bell true
AGENT_MANAGER_SESSION_ID=c1 run --chief
expect "chief, bell on: banner and sound" 1 1

echo
if [ "$FAILS" = 0 ]; then
	echo "all passed"
else
	echo "$FAILS failed"
	exit 1
fi
