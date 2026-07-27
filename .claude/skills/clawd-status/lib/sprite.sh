#!/usr/bin/env bash
# clawd-status — sprite frame data + half-block renderer
#
# Sprites are authored as pixel grids. Each character is one pixel; two pixel
# rows are packed into one terminal row using the upper half block (U+2580),
# with the top pixel as foreground and the bottom pixel as background.
#
# Palette chars:
#   C coral body   K eye   Y gold (alert)   R love (error)   . transparent

declare -A CLAWD_PAL=(
  [C]='242;96;76'     # #F2604C clawd coral
  [K]='20;20;20'      # #141414 eye
  [Y]='246;193;119'   # #F6C177 rosé pine gold
  [R]='235;111;146'   # #EB6F92 rosé pine love
)

CLAWD_W=11 # sprite width in pixels/columns (col 10 is the alert slot)
CLAWD_H=3  # sprite height in terminal rows (6 pixel rows)

# The body spans cols 0-9 (wide row) / 1-8 (narrow rows), so its centre falls on
# 4.5. Eyes (2, 7) and legs (1, 3, 6, 8) mirror across that centre.

# --- idle: breathe in, breathe out, blink -----------------------------------

CLAWD_IDLE=(
".CCCCCCCC..
.CKCCCCKC..
CCCCCCCCCC.
.CCCCCCCC..
.C.C..C.C..
.C.C..C.C.."
"...........
.CCCCCCCC..
.CKCCCCKC..
CCCCCCCCCC.
.CCCCCCCC..
.C.C..C.C.."
".CCCCCCCC..
.CKCCCCKC..
CCCCCCCCCC.
.CCCCCCCC..
.C.C..C.C..
.C.C..C.C.."
".CCCCCCCC..
.CCCCCCCC..
CCCCCCCCCC.
.CCCCCCCC..
.C.C..C.C..
.C.C..C.C.."
)

# --- working: arms up, stride, arms down, stride ----------------------------

CLAWD_WORK=(
".CCCCCCCC..
CCKCCCCKCC.
.CCCCCCCC..
.CCCCCCCC..
.C.C..C.C..
.C.C..C.C.."
".CCCCCCCC..
.CKCCCCKC..
CCCCCCCCCC.
.CCCCCCCC..
.C.C..C.C..
......C.C.."
".CCCCCCCC..
.CKCCCCKC..
.CCCCCCCC..
CCCCCCCCCC.
.C.C..C.C..
.C.C..C.C.."
".CCCCCCCC..
.CKCCCCKC..
CCCCCCCCCC.
.CCCCCCCC..
.C.C..C.C..
.C.C......."
)

# --- waiting on you: wide eyes + blinking gold "!" --------------------------

CLAWD_WAIT=(
".CCCCCCCC.Y
CCKCCCCKCCY
.CCCCCCCC.Y
.CCCCCCCC..
.C.C..C.C..
.C.C..C.C.Y"
".CCCCCCCC.Y
CCKCCCCKCCY
.CCCCCCCC.Y
.CCCCCCCC..
.C.C..C.C..
.C.C..C.C.Y"
".CCCCCCCC..
CCKCCCCKCC.
.CCCCCCCC..
.CCCCCCCC..
.C.C..C.C..
.C.C..C.C.."
)

# --- mini clawd for subagent rows (5px wide, 1 terminal row) ----------------

CLAWD_MINI_W=5
CLAWD_MINI=(
".CCC.
CKCKC"
"CCCCC
.KCK."
".CCC.
CKCKC"
".CCC.
CCCCC"
)

# clawd_render <frame> <width>
# Fills CLAWD_ROWS with one rendered (ANSI) string per terminal row.
clawd_render() {
  local frame=$1 width=${2:-$CLAWD_W}
  local -a px
  mapfile -t px <<<"$frame"

  local rows=$(( ${#px[@]} / 2 ))
  local r x top bot t b tc bc out
  CLAWD_ROWS=()

  for ((r = 0; r < rows; r++)); do
    top=${px[r * 2]}
    bot=${px[r * 2 + 1]}
    # Claude Code strips leading whitespace from each status line row. The
    # narrow body rows and the leg row start on a transparent col 0, so without
    # a guard they lose it and render one pixel left of the wide row. Opening
    # with a reset means the row never starts with an actual space character.
    out=$'\e[0m'
    for ((x = 0; x < width; x++)); do
      t=${top:x:1}
      b=${bot:x:1}
      tc=${CLAWD_PAL[$t]-}
      bc=${CLAWD_PAL[$b]-}
      if [[ -z $tc && -z $bc ]]; then
        out+=" "
      elif [[ -n $tc && -z $bc ]]; then
        out+=$'\e[38;2;'"${tc}"$'m▀\e[0m'
      elif [[ -z $tc && -n $bc ]]; then
        out+=$'\e[38;2;'"${bc}"$'m▄\e[0m'
      else
        out+=$'\e[38;2;'"${tc}"$';48;2;'"${bc}"$'m▀\e[0m'
      fi
    done
    CLAWD_ROWS[r]=$out
  done
}

# clawd_frame <state> <tick> -> echoes the frame for this tick
clawd_frame() {
  local state=$1 tick=$2
  local -a cycle
  case "$state" in
    work | working) cycle=("${CLAWD_WORK[@]}") ;;
    wait | waiting) cycle=("${CLAWD_WAIT[@]}") ;;
    mini) cycle=("${CLAWD_MINI[@]}") ;;
    *) cycle=("${CLAWD_IDLE[@]}") ;;
  esac
  printf '%s' "${cycle[tick % ${#cycle[@]}]}"
}
