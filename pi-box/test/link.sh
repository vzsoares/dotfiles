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

echo
if [ "$FAILS" -gt 0 ]; then
  echo "$FAILS failed"
  exit 1
fi
echo "all passed"
