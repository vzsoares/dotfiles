#!/usr/bin/env bash
set -e

# zen-wrapup — end-of-session wrap-up for Claude Code. Digests a session
# transcript and runs the `wrapup` skill headless on it: memories, a dated
# session log in the second-brain vault, and periodic consolidation.
#
#   zen-wrapup hook                      SessionEnd hook entry (JSON on stdin)
#   zen-wrapup run <transcript> [cwd]    run in the foreground
#   zen-wrapup digest <transcript>       print the digest only

CONFIG="$HOME/.claude/second-brain.json"
STATE_DIR="$HOME/.cache/zen-wrapup"
MODEL="${ZEN_WRAPUP_MODEL:-sonnet}"
MIN_USER_TURNS=3

usage() {
    cat <<'EOF'
usage: zen-wrapup hook
       zen-wrapup run <transcript.jsonl> [cwd]
       zen-wrapup digest <transcript.jsonl>

  hook     read the SessionEnd JSON on stdin, then wrap up in the background
  run      wrap up one transcript in the foreground (cwd defaults to $PWD)
  digest   print the condensed transcript the skill reads

  env: ZEN_WRAPUP_MODEL (default: sonnet)
EOF
}

# Text turns, tool calls (one line each) and tool errors; tool output is dropped.
digest() {
    jq -r '
      def clip($n): if length > $n then .[:$n] + " …" else . end;
      select(.type == "user" or .type == "assistant")
      | select(.isMeta != true)
      | .type as $role
      | .message.content
      | if type == "string" then [{type: "text", text: .}] else . end
      | .[]
      | if .type == "text" then
          (.text | gsub("<system-reminder>[\\s\\S]*?</system-reminder>"; "") | gsub("^\\s+|\\s+$"; "")) as $t
          | select($t != "")
          | "\n\($role | ascii_upcase): \($t | clip(2000))"
        elif .type == "tool_use" then
          "  [tool] \(.name): \((.input.description // .input.command // .input.file_path // .input.skill // .input.query // "") | tostring | gsub("\n"; " ") | clip(160))"
        elif .type == "tool_result" and .is_error == true then
          "  [tool error] \((.content | if type == "string" then . else (map(.text? // "") | join(" ")) end) | gsub("\n"; " ") | clip(300))"
        else empty end
    ' "$1"
}

user_turns() {
    jq -s '[.[] | select(.type == "user" and .isMeta != true)
            | select((.message.content | type) == "string"
                     or any(.message.content[]; .type == "text"))] | length' "$1"
}

run() {
    local transcript="$1" cwd="${2:-$PWD}"
    [ -f "$transcript" ] || { echo "zen-wrapup: no transcript at $transcript" >&2; exit 1; }
    [ -f "$CONFIG" ] || { echo "zen-wrapup: missing $CONFIG" >&2; exit 1; }
    command -v claude &>/dev/null || { echo "zen-wrapup: claude not on PATH" >&2; exit 1; }

    local vault id digest_file memory_root
    vault=$(jq -r '.vault' "$CONFIG")
    id=$(basename "$transcript" .jsonl)
    memory_root="$HOME/.claude/projects"
    mkdir -p "$STATE_DIR"
    digest_file="$STATE_DIR/$id.md"
    digest "$transcript" >"$digest_file"

    cd "$cwd"
    # ZEN_WRAPUP stops the headless session's own SessionEnd from recursing.
    ZEN_WRAPUP=1 claude -p "Run the wrapup skill in headless mode. Session id: $id. Project cwd: $cwd. Transcript digest: $digest_file. Vault: $vault." \
        --model "$MODEL" \
        --permission-mode acceptEdits \
        --add-dir "$vault" --add-dir "$memory_root" --add-dir "$STATE_DIR" \
        --allowedTools "Skill Read Write Edit Glob Grep Bash(zen-doc reindex) Bash(git -C $vault *) Bash(ls *) Bash(date *)"
}

hook() {
    [ -z "$ZEN_WRAPUP" ] || exit 0
    local input transcript cwd
    input=$(cat)
    transcript=$(jq -r '.transcript_path // empty' <<<"$input")
    cwd=$(jq -r '.cwd // empty' <<<"$input")
    [ -n "$transcript" ] && [ -f "$transcript" ] || exit 0
    [ "$(user_turns "$transcript")" -ge "$MIN_USER_TURNS" ] || exit 0

    mkdir -p "$STATE_DIR"
    # Detached so it outlives the exiting Claude Code process.
    nohup "$0" run "$transcript" "${cwd:-$HOME}" >>"$STATE_DIR/hook.log" 2>&1 </dev/null &
    disown
}

case "${1:-}" in
    hook) hook ;;
    run) shift; [ -n "${1:-}" ] || { usage; exit 1; }; run "$@" ;;
    digest) [ -n "${2:-}" ] || { usage; exit 1; }; digest "$2" ;;
    -h|--help) usage ;;
    *) usage; exit 1 ;;
esac
