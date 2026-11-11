#!/usr/bin/env bash
# Tests for statusline.sh's quota file. Run with:
#   bash .claude/skills/clawd-status/scripts/test_statusline.sh
# Uses a temp HOME; never touches ~/.cache/zen.
# shellcheck disable=SC2319 # check reads the [ ] result on purpose

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
export HOME="$TMP/home" CLAWD_HIDDEN=1 XDG_RUNTIME_DIR="$TMP/run"
QUOTA="$HOME/.cache/zen/claude-quota.json"
FAILS=0

trap 'rm -rf "$TMP"' EXIT

ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
check() { # name, condition result
    if [ "$2" = 0 ]; then ok "$1"; else bad "$1"; fi
}
line() { printf '%s' "$1" | bash "$HERE/statusline.sh" >"$TMP/out" 2>"$TMP/err"; }

FULL='{"cwd":"/tmp","rate_limits":{"five_hour":{"used_percentage":42.5,"resets_at":1794420000,"extra":1},"seven_day":{"used_percentage":63,"resets_at":1794800000}}}'

line '{"cwd":"/tmp"}'
check "no rate_limits: exit 0" $?
[ ! -e "$QUOTA" ] && [ -s "$TMP/out" ] && [ ! -s "$TMP/err" ]
check "no rate_limits: line drawn, no quota file" $?

line "$FULL"
check "rate_limits: exit 0" $?
want='{"five_hour":{"used_percentage":42.5,"resets_at":1794420000},"seven_day":{"used_percentage":63,"resets_at":1794800000}}'
[ "$(jq -c 'del(.updated_at)' "$QUOTA")" = "$want" ]
check "writes only the two windows" $?
now=$(date +%s)
[ "$(jq '.updated_at - '"$now"' | fabs < 5' "$QUOTA")" = true ]
check "updated_at is now" $?
[ -z "$(find "$HOME/.cache/zen" -name 'claude-quota.json.*')" ]
check "no temp file left behind" $?

echo '{"kept":true}' >"$QUOTA"
line "$FULL"
[ "$(cat "$QUOTA")" = '{"kept":true}' ]
check "rewrites at most every 10s" $?

touch -t 202001010000 "$QUOTA"
line "$FULL"
[ "$(jq -c 'del(.updated_at)' "$QUOTA")" = "$want" ]
check "rewrites a file older than 10s" $?

touch -t 202001010000 "$QUOTA"
line '{"cwd":"/tmp","rate_limits":{"five_hour":{"used_percentage":7,"resets_at":1}}}'
[ "$(jq -c '[.five_hour.used_percentage, .seven_day.used_percentage]' "$QUOTA")" = '[7,63]' ]
check "a missing window keeps the previous values" $?

touch -t 202001010000 "$QUOTA"
line '{"cwd":"/tmp","rate_limits":{"five_hour":{"used_percentage":null,"resets_at":null},"seven_day":{"used_percentage":null,"resets_at":null}}}'
check "null input: exit 0" $?
[ "$(jq -c '[.five_hour, .seven_day.used_percentage]' "$QUOTA")" = '[{"used_percentage":7,"resets_at":1},63]' ]
check "null input keeps the old values" $?
[ -z "$(find "$HOME/.cache/zen" -name 'claude-quota.json.*')" ]
check "null input leaves no temp file" $?

rm -f "$QUOTA"
line '{"cwd":"/tmp","rate_limits":{"five_hour":{"used_percentage":null,"resets_at":null}}}'
[ "$(jq -c '.five_hour' "$QUOTA")" = '{"used_percentage":null,"resets_at":null}' ]
check "null input with no previous file writes nulls" $?

echo 'not json' >"$QUOTA"
touch -t 202001010000 "$QUOTA"
line "$FULL"
[ "$(jq -c 'del(.updated_at)' "$QUOTA")" = "$want" ]
check "a corrupt previous file is replaced" $?

if [ "$FAILS" != 0 ]; then
    echo "$FAILS failed"
    exit 1
fi
echo "all passed"
