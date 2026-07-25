#!/usr/bin/env bash
# clawd-status — subagent status line
#
# Replaces each subagent row in the agent panel with a one-row mini clawd
# followed by the agent name and its token count. Reads every visible row as a
# single JSON object on stdin; writes one {"id","content"} JSON line per row.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/sprite.sh
source "$HERE/../lib/sprite.sh"
# shellcheck source=../lib/state.sh
source "$HERE/../lib/state.sh"

# Hidden: emit nothing and every row keeps Claude Code's default rendering.
clawd_hidden && exit 0

JQ=$(clawd_jq)
input=$(cat)

session=$(printf '%s' "$input" | "$JQ" -r '.session_id // "default"' 2>/dev/null)
columns=$(printf '%s' "$input" | "$JQ" -r '.columns // 80' 2>/dev/null)
[[ $columns =~ ^[0-9]+$ ]] || columns=80

# Shared tick with the main status line: the panel and the sprite bob together.
tick=$(cat "$CLAWD_DIR/${session:-default}.tick" 2>/dev/null) || tick=0
[[ $tick =~ ^[0-9]+$ ]] || tick=0

DIM=$'\e[2;38;2;224;222;244m'
TEXT=$'\e[38;2;224;222;244m'
GOLD=$'\e[2;38;2;246;193;119m'
RST=$'\e[0m'

while IFS=$'\x1f' read -r id name status tokens; do
  [[ -z $id ]] && continue

  # Sleeping (closed eyes) unless the agent is actually running; each row is
  # offset by its own hash so a panel of agents doesn't blink in lockstep.
  if [[ $status == running || $status == in_progress || $status == active ]]; then
    offset=$(( ${#id} + ${#name} ))
    frame=${CLAWD_MINI[(tick + offset) % ${#CLAWD_MINI[@]}]}
  else
    frame=${CLAWD_MINI[3]}
  fi
  clawd_render "$frame" "$CLAWD_MINI_W"

  # Build the trailing bits first, then give the name whatever width is left.
  extra_plain="" extra_color=""
  if [[ $tokens =~ ^[0-9]+$ ]] && (( tokens > 0 )); then
    extra_plain+=" ${tokens}tok"
    extra_color+=" ${GOLD}${tokens}tok${RST}"
  fi
  if [[ $status != running && $status != in_progress && $status != active && -n $status ]]; then
    extra_plain+=" ${status}"
    extra_color+=" ${DIM}${status}${RST}"
  fi

  max=$(( columns - CLAWD_MINI_W - 1 - ${#extra_plain} ))
  (( max < 4 )) && max=4
  (( ${#name} > max )) && name="${name:0:max-1}…"

  content="${CLAWD_ROWS[0]} ${TEXT}${name}${RST}${extra_color}"

  "$JQ" -cn --arg id "$id" --arg content "$content" '{id: $id, content: $content}'
done < <(
  printf '%s' "$input" | "$JQ" -r '
    (.tasks // [])[]
    | [(.id // ""), (.name // .label // "agent"), (.status // ""), ((.tokenCount // 0) | tostring)]
    | join("\u001f")' 2>/dev/null
)
