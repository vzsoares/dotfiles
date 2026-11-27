#!/usr/bin/env bash
# pi-box/link against a temp HOME and a fake agent-manager; nothing live changes.
# Run with:  bash pi-box/test/link.sh

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINK="$HERE/../link"
TMP="$(cd "$(mktemp -d)" && pwd -P)"
FAILS=0
trap 'rm -rf "$TMP"' EXIT

ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
check() { if [ "$2" = 0 ]; then ok "$1"; else bad "$1"; fi; }

mkdir -p "$TMP/bin" "$TMP/home"
cat >"$TMP/bin/agent-manager" <<'FAKE'
#!/usr/bin/env bash
echo "$*" >>"$FAKE_AM_LOG"
if [ "$1" = groups ]; then
  [ -f "$FAKE_AM_GROUP" ] && echo '[{"path": "pi-box"}]' || echo '[]'
elif [ "$1" = create-group ]; then
  touch "$FAKE_AM_GROUP"
fi
FAKE
chmod +x "$TMP/bin/agent-manager"
export HOME="$TMP/home" FAKE_AM_LOG="$TMP/am.log" FAKE_AM_GROUP="$TMP/group"
R="$HOME/pi-box"
base="$TMP/bin:/usr/bin:/bin"

PATH="$base" bash "$LINK" >/dev/null
check "link runs" $?
[ "$(readlink "$R/shim/pi")" = "$(cd "$HERE/.." && pwd)/wrapper" ]
check "shim/pi links to the wrapper" $?
grep -qx "env set pi-box PATH=$R/shim:$base PI_CODING_AGENT_DIR=$R/agent" "$TMP/am.log"
check "profile puts the shim first and points pi at the box agent dir" $?
grep -qx "create-group pi-box --directory $R/work" "$TMP/am.log"
check "creates the pi-box group" $?

: >"$TMP/am.log"
PATH="$R/shim:$R/shim/:$R/shim:$base:$R/shim" bash "$LINK" >/dev/null
grep -qx "env set pi-box PATH=$R/shim:$base PI_CODING_AGENT_DIR=$R/agent" "$TMP/am.log"
check "a re-run keeps one shim entry" $?
! grep -q create-group "$TMP/am.log"
check "a re-run keeps the existing group" $?

grep -q '"glm-5-3-flash-uncensored"' "$R/agent/models.json" &&
  grep -q '"deepseek-v4-1-flash-uncensored"' "$R/agent/models.json" &&
  ! grep -q gemma "$R/agent/models.json"
check "copies the box models.json (default + fallback, no gemma)" $?

[ -f "$R/agent/skills/pi/SKILL.md" ] && [ -f "$R/agent/skills/pi/references/mcp.md" ] &&
  [ ! -L "$R/agent/skills/pi" ]
check "copies the pi skill with its references" $?
echo stale >"$R/agent/skills/pi/stale.md"
mkdir -p "$HERE/../../.claude/skills/pi/scripts/__pycache__"
PATH="$base" bash "$LINK" >/dev/null
[ ! -e "$R/agent/skills/pi/stale.md" ] && [ ! -e "$R/agent/skills/pi/scripts/__pycache__" ]
check "a re-run refreshes the pi skill without bytecode caches" $?

[ -f "$R/agent/skills/chrome/SKILL.md" ] && [ -f "$R/agent/skills/browser/SKILL.md" ]
check "copies the chrome skill beside the browser skill" $?

M="$R/agent/mcp.json"
cmp -s "$M" "$HERE/../agent/mcp.json" && grep -q '"command": "/usr/local/bin/abb-mcp"' "$M" &&
  grep -q '"exposure": "direct"' "$M"
check "a first run copies mcp.json with the pinned browser entry" $?
mcp_is() { # other-entry json; the browser entry must equal the template's
  python3 - "$M" "$HERE/../agent/mcp.json" "$1" <<'PY'
import json, sys
got, want = (json.load(open(p))["mcpServers"] for p in sys.argv[1:3])
other = json.loads(sys.argv[3])
sys.exit(got.pop("browser", None) != want["browser"] or got != other)
PY
}
echo '{"mcpServers":{"mine":{"command":"npx","args":["-y","x@1.0.0"]}}}' >"$M"
PATH="$base" bash "$LINK" >/dev/null
mcp_is '{"mine":{"command":"npx","args":["-y","x@1.0.0"]}}'
check "a re-run merges the browser entry and keeps pi's own servers" $?
echo '{"mcpServers":{"browser":{"command":"/bin/evil"},"mine":{"command":"npx"}}}' >"$M"
PATH="$base" bash "$LINK" >/dev/null
mcp_is '{"mine":{"command":"npx"}}'
check "a re-run resets an edited browser entry" $?
PATH="$base" bash "$LINK" --force >/dev/null
cmp -s "$M" "$HERE/../agent/mcp.json"
check "--force resets mcp.json to the template" $?
echo '{"secret":"host"}' >"$TMP/host.json"
rm -f "$M"
ln -s "$TMP/host.json" "$M"
! PATH="$base" bash "$LINK" >/dev/null 2>&1 && [ -L "$M" ] && [ "$(cat "$TMP/host.json")" = '{"secret":"host"}' ]
check "refuses a symlinked mcp.json without following it" $?
PATH="$base" bash "$LINK" --force >/dev/null
[ ! -L "$M" ] && cmp -s "$M" "$HERE/../agent/mcp.json" &&
  [ "$(cat "$TMP/host.json")" = '{"secret":"host"}' ]
check "--force replaces a symlinked mcp.json, not its target" $?

S="$R/agent/settings.json"
key() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get(sys.argv[2]))' "$S" "$1"; }
[ "$(key tuiMode)" = regular ]
check "a first run copies settings.json with tuiMode regular" $?
echo '{"lastChangelogVersion":"1.5.2","defaultModel":"mine"}' >"$S"
PATH="$base" bash "$LINK" >/dev/null
[ "$(key tuiMode)" = regular ] && [ "$(key lastChangelogVersion)" = 1.5.2 ] &&
  [ "$(key defaultModel)" = mine ]
check "a re-run adds a missing tuiMode and keeps the other keys" $?
echo '{"tuiMode":"fullscreen","x":1}' >"$S"
PATH="$base" bash "$LINK" >/dev/null
[ "$(key tuiMode)" = fullscreen ] && [ "$(key x)" = 1 ]
check "a re-run keeps an existing tuiMode" $?
PATH="$base" bash "$LINK" --force >/dev/null
[ "$(key tuiMode)" = regular ] && cmp -s "$S" "$HERE/../agent/settings.json"
check "--force resets settings.json to the template" $?
printf '{"tuiMode": ' >"$S"
cp "$S" "$TMP/bad.json"
! PATH="$base" bash "$LINK" >/dev/null 2>&1 && cmp -s "$S" "$TMP/bad.json"
check "a malformed settings.json fails and stays byte-identical" $?
rm -f "$S"
echo '{"secret":"host"}' >"$TMP/host-settings.json"
ln -s "$TMP/host-settings.json" "$S"
! PATH="$base" bash "$LINK" >/dev/null 2>&1 && [ -L "$S" ] &&
  [ "$(cat "$TMP/host-settings.json")" = '{"secret":"host"}' ]
check "refuses a symlinked settings.json without following it" $?
rm -f "$S"

for f in "$HERE/../../agents/settings.json" "$HERE/../agent/settings.json"; do
  python3 -c 'import json,sys; sys.exit(json.load(open(sys.argv[1])).get("tuiMode") != "regular")' "$f"
  check "$(basename "$(dirname "$f")")/settings.json parses with tuiMode regular" $?
done

echo
if [ "$FAILS" -gt 0 ]; then
  echo "$FAILS failed"
  exit 1
fi
echo "all passed"
