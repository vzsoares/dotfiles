#!/usr/bin/env bash
# Tests for the SessionEnd hook of scripts/wrapup.sh. Run with:
#   bash scripts/test_wrapup.sh
# claude is a stub on PATH and HOME is a temp dir; no wrap-up really runs.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WRAPUP="$HERE/wrapup.sh"
TMP="$(mktemp -d)"
FAILS=0

trap 'rm -rf "$TMP"' EXIT

mkdir "$TMP/bin"
cat >"$TMP/bin/claude" <<'STUB'
#!/usr/bin/env bash
echo "$*" >>"$STUB_DIR/claude"
STUB
chmod +x "$TMP/bin/claude"

export STUB_DIR="$TMP/stub"
export PATH="$TMP/bin:$PATH"
export HOME="$TMP/home"
export ZEN_WRAPUP_MIN_TURNS=3
unset ZEN_WRAPUP ZEN_ROTATE_STATE
MARKERS="$HOME/.cache/zen/rotate"
STARTED="$HOME/.cache/zen-wrapup"

ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
reset() {
	rm -rf "$STUB_DIR" "$HOME"
	mkdir -p "$STUB_DIR" "$HOME/.claude" "$MARKERS" "$TMP/vault"
	echo "{\"vault\": \"$TMP/vault\"}" >"$HOME/.claude/second-brain.json"
	for n in 1 2 3; do
		echo "{\"type\":\"user\",\"message\":{\"content\":\"turn $n\"}}"
	done >"$TMP/t.jsonl"
}
hook() { # reason
	printf '{"transcript_path":"%s","cwd":"%s","reason":"%s"}' "$TMP/t.jsonl" "$TMP" "$1" |
		bash "$WRAPUP" hook
}
# The hook makes $STARTED right before it detaches the run.
ran() {
	[ -d "$STARTED" ] || return 1
	local i=0
	while [ ! -s "$STUB_DIR/claude" ] && [ "$i" -lt 50 ]; do sleep 0.1; i=$((i + 1)); done
	grep -q 'Run the wrapup skill in headless mode' "$STUB_DIR/claude"
}

reset
touch "$MARKERS/clearing-s1"
if AGENT_MANAGER_SESSION_ID=s1 hook clear && [ ! -d "$STARTED" ]; then ok "clear plus the marker runs no wrap-up"; else bad "clear plus the marker runs no wrap-up"; fi

reset
AGENT_MANAGER_SESSION_ID=s1 hook clear
if ran; then ok "clear without a marker wraps up"; else bad "clear without a marker wraps up"; fi

reset
touch "$MARKERS/clearing-s1"
AGENT_MANAGER_SESSION_ID=s1 hook prompt_input_exit
if ran; then ok "another reason wraps up despite the marker"; else bad "another reason wraps up despite the marker"; fi

reset
touch "$MARKERS/clearing-s2"
AGENT_MANAGER_SESSION_ID=s1 hook clear
if ran; then ok "another session's marker does not skip"; else bad "another session's marker does not skip"; fi

reset
touch "$MARKERS/clearing-"
(unset AGENT_MANAGER_SESSION_ID; hook clear)
if ran; then ok "clear outside agent-manager wraps up"; else bad "clear outside agent-manager wraps up"; fi

reset
mkdir -p "$TMP/elsewhere"
touch "$TMP/elsewhere/clearing-s1"
AGENT_MANAGER_SESSION_ID=s1 ZEN_ROTATE_STATE="$TMP/elsewhere" hook clear
if [ ! -d "$STARTED" ]; then ok "the marker is read from ZEN_ROTATE_STATE"; else bad "the marker is read from ZEN_ROTATE_STATE"; fi

echo
if [ "$FAILS" = 0 ]; then
	echo "all passed"
else
	echo "$FAILS failed"
	exit 1
fi
