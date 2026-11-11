#!/usr/bin/env bash
# clawd-status — animated status line
#
# Renders a 3-row half-block sprite on the left and the session info on the
# right. Deliberately no `set -e`: a non-zero exit blanks the status line.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/sprite.sh
source "$HERE/../lib/sprite.sh"
# shellcheck source=../lib/state.sh
source "$HERE/../lib/state.sh"

JQ=$(clawd_jq)
input=$(cat)

# One jq call for everything; the script runs up to a few times a second.
# Fields are joined with US (0x1f) rather than a tab: tab counts as IFS
# whitespace, so `read` would collapse runs of it and drop empty fields.
IFS=$'\x1f' read -r cwd model ctx quota effort fast session quota_json < <(
  printf '%s' "$input" | "$JQ" -r '[
    (.cwd // .workspace.current_dir // ""),
    (.model.display_name // ""),
    (.context_window.used_percentage // 0 | floor | tostring),
    (.rate_limits.five_hour.used_percentage // 0 | floor | tostring),
    (.effort.level // ""),
    (.fast_mode // false | tostring),
    (.session_id // "default"),
    (if .rate_limits then {
      five_hour: (.rate_limits.five_hour // {} | {used_percentage, resets_at}),
      seven_day: (.rate_limits.seven_day // {} | {used_percentage, resets_at}),
      updated_at: (now | floor)
    } | tojson else "" end)
  ] | join("\u001f")' 2>/dev/null
)
cwd=${cwd:-$PWD}
session=${session:-default}

# mtime <file> -> epoch seconds, 0 when missing (GNU stat, else BSD)
mtime() { stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null || echo 0; }

# --- quota for zen-inbox's cockpit header, written at most every 10s --------

if [[ -n ${quota_json:-} ]]; then
  quota_file="$HOME/.cache/zen/claude-quota.json"
  if (( $(date +%s) - $(mtime "$quota_file") >= 10 )); then
    mkdir -p "${quota_file%/*}" 2>/dev/null
    # Null fields in the new input keep the previous file's values.
    old=$(cat "$quota_file" 2>/dev/null)
    # shellcheck disable=SC2016 # jq variables
    merged=$("$JQ" -nc --argjson new "$quota_json" --arg old "$old" '
      (try ($old | fromjson) catch {}) as $o
      | (if ($o | type) == "object" then $o else {} end) as $o
      | $new | with_entries(
          if .key == "updated_at" then . else
            .value = {
              used_percentage: (.value.used_percentage // $o[.key].used_percentage?),
              resets_at: (.value.resets_at // $o[.key].resets_at?)
            }
          end)' 2>/dev/null)
    [[ -n $merged ]] &&
      { { printf '%s\n' "$merged" >"$quota_file.$$" && mv -f "$quota_file.$$" "$quota_file"; } 2>/dev/null ||
        rm -f "$quota_file.$$"; }
  fi
fi

# --- git, cached (this script runs on a 1s timer) ---------------------------

git_cache="$CLAWD_DIR/git-$(printf '%s' "$cwd" | cksum | cut -d' ' -f1)"
branch="" dirty=""
if [[ -f $git_cache ]] && (( $(date +%s) - $(mtime "$git_cache") < 3 )); then
  IFS=$'\t' read -r branch dirty <"$git_cache"
else
  branch=$(git -C "$cwd" symbolic-ref --short HEAD 2>/dev/null) ||
    branch=$(git -C "$cwd" rev-parse --short HEAD 2>/dev/null) || branch=""
  if [[ -n $branch ]]; then
    if ! git -C "$cwd" diff --quiet 2>/dev/null || ! git -C "$cwd" diff --cached --quiet 2>/dev/null; then
      dirty="1"
    fi
  fi
  mkdir -p "$CLAWD_DIR" 2>/dev/null
  printf '%s\t%s\n' "$branch" "$dirty" >"$git_cache" 2>/dev/null || true
fi

# --- sprite -----------------------------------------------------------------

state=$(clawd_get_state "$session")
if clawd_hidden; then
  ROWS=1
else
  ROWS=3
  tick=$(clawd_tick "$session")
  clawd_render "$(clawd_frame "$state" "$tick")" "$CLAWD_W"
fi

# --- info column (rosé pine, matching the zenha zsh theme) ------------------

CYAN=$'\e[1;38;2;156;207;216m'
BLUE=$'\e[1;38;2;49;116;143m'
LOVE=$'\e[1;38;2;235;111;146m'
GOLD=$'\e[1;38;2;246;193;119m'
DIM=$'\e[2;38;2;224;222;244m'
DGOLD=$'\e[2;38;2;246;193;119m'
RST=$'\e[0m'

declare -a LINE=("" "" "")
declare -a LEN=(0 0 0)
if (( ROWS == 1 )); then
  budget=$(( ${COLUMNS:-100} - 2 ))
else
  budget=$(( ${COLUMNS:-100} - CLAWD_W - 4 ))
fi
(( budget < 8 )) && budget=8

# seg <row> <plain> <colored>
# With the sprite hidden every segment lands on row 0, so the separator is
# added here rather than baked into each segment.
seg() {
  local r=$1 plain=$2 colored=$3 sep=""
  (( ROWS == 1 )) && r=0
  (( LEN[r] > 0 )) && sep=" "
  (( LEN[r] + ${#sep} + ${#plain} > budget )) && return 0
  LINE[r]+="${sep}${colored}"
  LEN[r]=$(( LEN[r] + ${#sep} + ${#plain} ))
}

dir=$(basename "$cwd")
seg 0 "$dir" "${CYAN}${dir}${RST}"
if [[ -n $branch ]]; then
  seg 0 "git:$branch" "${BLUE}git:${LOVE}${branch}${RST}"
  [[ -n $dirty ]] && seg 0 "✗" "${GOLD}✗${RST}"
fi

[[ -n $model ]] && seg 1 "$model" "${DGOLD}${model}${RST}"
[[ $fast == true ]] && seg 1 "⚡" "${GOLD}⚡${RST}"
[[ -n $effort && $effort != "medium" ]] && seg 1 "$effort" "${DIM}${effort}${RST}"
seg 1 "ctx:${ctx}%" "${DIM}ctx:${ctx}%${RST}"

seg 2 "quota:${quota}%" "${DIM}quota:${quota}%${RST}"
case $state in
  working) seg 2 "working" "${GOLD}working${RST}" ;;
  waiting) seg 2 "needs you" "${LOVE}needs you${RST}" ;;
  *) seg 2 "idle" "${DIM}idle${RST}" ;;
esac

if (( ROWS == 1 )); then
  printf '%s\n' "${LINE[0]}"
else
  for r in 0 1 2; do
    printf '%s  %s\n' "${CLAWD_ROWS[r]}" "${LINE[r]}"
  done
fi
