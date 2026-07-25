#!/usr/bin/env bash
# clawd-status — hook handler: records what clawd should be doing.
#
# Usage: state-hook.sh <idle|working|waiting>
# Reads the hook payload on stdin to pick up session_id. Deliberately forks
# nothing (no jq, no sed) because PostToolUse runs this on every tool call.
# Always exits 0 and prints nothing, so it can never disturb the session.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/state.sh
source "$HERE/../lib/state.sh"

state=${1:-idle}
payload=$(cat 2>/dev/null)

session=default
[[ $payload =~ \"session_id\"[[:space:]]*:[[:space:]]*\"([^\"]+)\" ]] && session=${BASH_REMATCH[1]}

clawd_set_state "$session" "$state"
exit 0
