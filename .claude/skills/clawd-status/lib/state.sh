#!/usr/bin/env bash
# clawd-status — shared runtime state
#
# The status line script (wired into ~/.claude/settings.json) and the hook
# scripts (shipped by the plugin) are separate processes, so they meet at a
# fixed path rather than ${CLAUDE_PLUGIN_DATA}, which only the plugin sees.

CLAWD_DIR="${XDG_RUNTIME_DIR:-/tmp}/clawd-status"
CLAWD_CONFIG="${CLAWD_CONFIG:-$HOME/.claude/clawd-status.json}"
CLAWD_STALE_AFTER=180 # seconds before a "working" state is assumed dead

# clawd_hidden -> 0 when the sprite should be suppressed
# CLAWD_HIDDEN in the environment wins, so a single session can opt out without
# touching the config. Matched with a bash regex rather than jq: this runs on
# every status line tick.
clawd_hidden() {
  case "${CLAWD_HIDDEN:-}" in
    1 | true | yes) return 0 ;;
    0 | false | no) return 1 ;;
  esac
  local cfg
  cfg=$(cat "$CLAWD_CONFIG" 2>/dev/null) || return 1
  [[ $cfg =~ \"hidden\"[[:space:]]*:[[:space:]]*true ]]
}

# clawd_set_hidden <true|false>
clawd_set_hidden() {
  mkdir -p "$(dirname "$CLAWD_CONFIG")" 2>/dev/null
  printf '{\n  "hidden": %s\n}\n' "$1" >"$CLAWD_CONFIG"
}

# clawd_jq -> path to a usable jq
clawd_jq() {
  if [[ -n ${CLAWD_JQ:-} ]]; then
    printf '%s' "$CLAWD_JQ"
  elif command -v jq >/dev/null 2>&1; then
    printf 'jq'
  else
    printf '%s' "$HOME/.local/share/mise/installs/jq/latest/jq"
  fi
}

# clawd_set_state <session_id> <state>
clawd_set_state() {
  local session=${1:-default} state=$2
  mkdir -p "$CLAWD_DIR" 2>/dev/null || return 0
  printf '%s' "$state" >"$CLAWD_DIR/${session}.state" 2>/dev/null || true
}

# clawd_get_state <session_id> -> idle | working | waiting
# A "working" state older than CLAWD_STALE_AFTER decays to idle, so a missed
# Stop hook can't leave clawd running on the spot forever.
clawd_get_state() {
  local session=${1:-default}
  local file="$CLAWD_DIR/${session}.state"
  local state
  state=$(cat "$file" 2>/dev/null) || state=idle
  [[ -z $state ]] && state=idle

  if [[ $state == working ]]; then
    local mtime now
    mtime=$(stat -c %Y "$file" 2>/dev/null) || mtime=0
    now=$(date +%s)
    (( now - mtime > CLAWD_STALE_AFTER )) && state=idle
  fi

  printf '%s' "$state"
}

# clawd_tick <session_id> -> monotonically increasing frame counter
clawd_tick() {
  local session=${1:-default}
  local file="$CLAWD_DIR/${session}.tick"
  local n
  mkdir -p "$CLAWD_DIR" 2>/dev/null || { printf '0'; return; }
  n=$(cat "$file" 2>/dev/null) || n=0
  [[ $n =~ ^[0-9]+$ ]] || n=0
  n=$(( (n + 1) % 100000 ))
  printf '%s' "$n" >"$file" 2>/dev/null || true
  printf '%s' "$n"
}
