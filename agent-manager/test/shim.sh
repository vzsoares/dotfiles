#!/usr/bin/env bash
# agent-manager/shim/claude against a fake claude, a copy of the repo layout and
# a temp HOME; nothing live changes.
# Run with:  bash agent-manager/test/shim.sh

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
TMP="$(cd "$(mktemp -d)" && pwd -P)"
FAILS=0
trap 'rm -rf "$TMP"' EXIT

ok() { echo "ok   $1"; }
bad() {
  echo "FAIL $1"
  FAILS=$((FAILS + 1))
}
check() { if [ "$2" = 0 ]; then ok "$1"; else bad "$1"; fi; }

# uv keeps its interpreters and cache under the real HOME.
UV_PYTHON_INSTALL_DIR="$(uv python dir)"
UV_CACHE_DIR="$(uv cache dir)"
export UV_PYTHON_INSTALL_DIR UV_CACHE_DIR

FAKE_REPO="$TMP/repo"
SHIM_DIR="$FAKE_REPO/agent-manager/shim"
SHIM="$SHIM_DIR/claude"
mkdir -p "$SHIM_DIR" "$FAKE_REPO/scripts" "$FAKE_REPO/agent-manager/roles" \
  "$FAKE_REPO/agents" "$TMP/bin" "$TMP/out" "$TMP/hooks" "$TMP/project" "$TMP/bare"
echo '{"mcpServers": {}}' >"$FAKE_REPO/agents/mcp.json"
cp "$REPO/agent-manager/shim/claude" "$SHIM"
cp "$REPO/scripts/role.py" "$FAKE_REPO/scripts/role.py"
echo "Role instructions." >"$FAKE_REPO/agent-manager/roles/full.md"
ROLES="$FAKE_REPO/agent-manager/roles.json"
cat >"$FAKE_REPO/agent-manager/config.json" <<'JSON'
{"presets": [{"name": "worker-rules", "instructions": "x"}]}
JSON
write_roles() {
  cat >"$ROLES" <<'JSON'
{
  "plain": {"saved": "worker-rules", "model": "opus", "effort": "medium"},
  "full": {
    "saved": "worker-rules",
    "model": "fable",
    "effort": "high",
    "autocompact": 300000,
    "settings": {
      "outputStyle": "default",
      "statusLine": "role",
      "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "role-stop"}]}]}
    },
    "skills": {"only": ["zen-flow"], "off": ["figma:figma-use"]},
    "tools": {"deny": ["WebSearch", "mcp__x__y"]},
    "append": "full.md"
  },
  "iso": {
    "saved": "worker-rules",
    "isolate": true,
    "settings": {
      "outputStyle": "default",
      "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "role-stop"}]}]}
    },
    "skills": {"only": ["zen-flow"]},
    "instructions": {"keep": ["Tools", "User"]},
    "append": "full.md"
  }
}
JSON
}
write_roles

export HOME="$TMP/home"
SKILLS="$HOME/.claude/skills"
mkdir -p "$SKILLS/zen-flow" "$SKILLS/wiki" "$SKILLS/synced/bucket/zen-flow"
touch "$SKILLS/zen-flow/SKILL.md" "$SKILLS/wiki/SKILL.md"
cat >"$HOME/.claude/settings.json" <<'JSON'
{
  "autoCompactWindow": 800000,
  "modelSettings": {"claude-fable-5-2": {"autoCompactWindow": 300000}, "claude-opus-5-5": {"effortLevel": "high"}},
  "outputStyle": "adhd",
  "statusLine": "global",
  "permissions": {"allow": ["Bash(git status)"]},
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "global-stop"}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "global-end"}]}]
  }
}
JSON
cat >"$HOME/.claude/CLAUDE.md" <<'MD'
# User

- zsh

# Board

Write the board.

# Tools

Use uv.
MD
printf '# Project\n\nCodeword.\n' >"$TMP/project/CLAUDE.md"
RUN="$HOME/.cache/zen/roles/run"

cat >"$TMP/bin/claude" <<'FAKE'
#!/usr/bin/env bash
out="${FAKE_OUT:?}"
mkdir -p "$out"
: >"$out/argv"
prev=""
for arg in "$@"; do
  printf '%s\n--ARG--\n' "$arg" >>"$out/argv"
  [ "$prev" = --settings ] && cp "$arg" "$out/settings.json"
  [ "$prev" = --append-system-prompt-file ] && cp "$arg" "$out/instructions.md"
  [ "$prev" = --plugin-dir ] && ls "$arg/skills" >"$out/skills"
  prev="$arg"
done
printf '%s\n' "$PATH" >"$out/path"
printf '%s\n' "${VIRTUAL_ENV:-}" >"$out/venv"
echo run >>"$out/runs"
exit "${FAKE_EXIT:-0}"
FAKE
chmod +x "$TMP/bin/claude"
export FAKE_OUT="$TMP/out"
BASE_PATH="$SHIM_DIR:$TMP/bin:$PATH"

argv_of() { for arg in "$@"; do printf '%s\n--ARG--\n' "$arg"; done; }
same_argv() { # name, expected args...
  local name="$1"
  shift
  if [ "$(argv_of "$@")" = "$(cat "$FAKE_OUT/argv")" ]; then ok "$name"; else
    bad "$name"
    cat "$FAKE_OUT/argv"
  fi
}
shim() { PATH="$BASE_PATH" claude "$@"; }
setting() { jq -r "$1" "$FAKE_OUT/settings.json"; }

LAUNCHER="$TMP/hooks/6c328783.settings.json"
cat >"$LAUNCHER" <<'JSON'
{
  "env": {"AGENT_MANAGER_SESSION": "6c328783"},
  "statusLine": "launcher",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "am-stop"}]}],
    "Notification": [{"hooks": [{"type": "command", "command": "am-notify"}]}]
  }
}
JSON
# shellcheck disable=SC2016
PROMPT='Line one "quoted"
line two with $HOME and '"'"'single'"'"' quotes'

# --- no role: same argv, same exit code ---------------------------------------
(
  unset ZEN_ROLE
  FAKE_EXIT=7 shim --model opus "$PROMPT" --settings "$LAUNCHER"
)
check "no role: exit code passes through" "$([ $? = 7 ] && echo 0 || echo 1)"
same_argv "no role: argv unchanged" --model opus "$PROMPT" --settings "$LAUNCHER"

# --- pass-through under a role -------------------------------------------------
export ZEN_ROLE=full
for sub in mcp auth config plugin plugins update upgrade doctor agents install \
  setup-token logs attach auto-mode gateway import purge respawn rm stop kill \
  ultrareview; do
  FAKE_EXIT=3 shim "$sub" list --settings "$LAUNCHER"
  code=$?
  if [ "$code" = 3 ] && [ "$(argv_of "$sub" list --settings "$LAUNCHER")" = "$(cat "$FAKE_OUT/argv")" ]; then
    ok "pass-through: $sub"
  else
    bad "pass-through: $sub"
  fi
done
for flag in -p --print --version --help; do
  FAKE_EXIT=4 shim --model haiku "$flag" "$PROMPT"
  code=$?
  if [ "$code" = 4 ] && [ "$(argv_of --model haiku "$flag" "$PROMPT")" = "$(cat "$FAKE_OUT/argv")" ]; then
    ok "pass-through: $flag"
  else
    bad "pass-through: $flag"
  fi
done
# A broken roles.json must not reach the pass-through path: no interpreter runs.
echo '{not json' >"$ROLES"
shim -p hello
check "pass-through: reads no role config" $?
write_roles

# --- overlay launch: real argv --------------------------------------------------
MCP="$TMP/hooks/mcp claude.json"
export ZEN_ROLE=plain
FAKE_EXIT=5 shim --model sonnet --effort low "$PROMPT" --mcp-config "$MCP" --settings "$LAUNCHER"
check "launch: exit code passes through" "$([ $? = 5 ] && echo 0 || echo 1)"
MERGED="$RUN/6c328783.settings.json/settings.json"
same_argv "launch: one --settings, the rest unchanged" \
  --settings "$MERGED" --model sonnet --effort low "$PROMPT" --mcp-config "$MCP"
check "launch: launcher env survives" \
  "$([ "$(setting .env.AGENT_MANAGER_SESSION)" = 6c328783 ] && echo 0 || echo 1)"
check "launch: PATH and VIRTUAL_ENV as the caller had them" \
  "$([ "$(cat "$FAKE_OUT/path")" = "$BASE_PATH" ] && [ -z "$(cat "$FAKE_OUT/venv")" ] && echo 0 || echo 1)"
check "launch: no source restriction" \
  "$(grep -q -e '--setting-sources' -e '--strict-mcp-config' "$FAKE_OUT/argv" && echo 1 || echo 0)"

shim --resume 5a151f40 --mcp-config "$MCP" --settings "$LAUNCHER"
same_argv "resume: model and effort added when absent" \
  --model opus --effort medium --settings "$MERGED" --resume 5a151f40 --mcp-config "$MCP"

# --- overlay launch: every field -------------------------------------------------
export ZEN_ROLE=full
shim "$PROMPT" --mcp-config "$MCP" "--settings=$LAUNCHER"
same_argv "full role: flags" \
  --model fable --effort high --autocompact 300000 \
  --disallowedTools "WebSearch,mcp__x__y" \
  --append-system-prompt-file "$FAKE_REPO/agent-manager/roles/full.md" \
  --settings "$MERGED" "$PROMPT" --mcp-config "$MCP"
check "full role: launcher wins a scalar" \
  "$([ "$(setting .statusLine)" = launcher ] && echo 0 || echo 1)"
check "full role: overlay scalar kept" \
  "$([ "$(setting .outputStyle)" = default ] && echo 0 || echo 1)"
check "full role: hook lists joined" \
  "$([ "$(setting '[.hooks.Stop[].hooks[].command] | join(",")')" = "role-stop,am-stop" ] && echo 0 || echo 1)"
check "full role: launcher-only hook kept" \
  "$([ "$(setting '.hooks.Notification[0].hooks[0].command')" = am-notify ] && echo 0 || echo 1)"
check "full role: skills.only turns the rest off" \
  "$([ "$(setting '.skillOverrides | keys | join(",")')" = "anthropic-skills:zen-flow,figma:figma-use,wiki" ] && echo 0 || echo 1)"
check "full role: window over the global top-level value" \
  "$([ "$(setting .autoCompactWindow)" = 300000 ] && echo 0 || echo 1)"
check "full role: window over each per-model value" \
  "$([ "$(setting '.modelSettings | tojson')" = '{"claude-fable-5-2":{"autoCompactWindow":300000}}' ] && echo 0 || echo 1)"

# --- inline JSON --settings, and none --------------------------------------------
rm -rf "$RUN"
shim --settings '{"env": {"INLINE": "1"}, "statusLine": "inline"}' "$PROMPT"
check "inline --settings: merged" \
  "$([ "$(setting .env.INLINE)$(setting .statusLine)$(setting .outputStyle)" = 1inlinedefault ] && echo 0 || echo 1)"
check "inline --settings: a pid-named run set" \
  "$(find "$RUN" -maxdepth 1 -name 'pid-*' | grep -q . && echo 0 || echo 1)"
shim "$PROMPT"
check "no launcher --settings: overlay alone" \
  "$([ "$(setting .outputStyle)" = default ] && echo 0 || echo 1)"

# --- isolate ---------------------------------------------------------------------
export ZEN_ROLE=iso
ISO="$RUN/6c328783.settings.json"
(cd "$TMP/project" && shim "$PROMPT" --mcp-config "$MCP" --settings "$LAUNCHER")
same_argv "isolate: flags" \
  --setting-sources local --plugin-dir "$ISO/plugin" \
  --mcp-config "$FAKE_REPO/agents/mcp.json" \
  --append-system-prompt-file "$ISO/instructions.md" \
  --settings "$ISO/settings.json" "$PROMPT" --mcp-config "$MCP"
check "isolate: MCP servers are not restricted" \
  "$(grep -q -e '--strict-mcp-config' "$FAKE_OUT/argv" && echo 1 || echo 0)"
check "isolate: every global hook, then the role's, then the launcher's" \
  "$([ "$(setting '[.hooks.Stop[].hooks[].command] | join(",")')" = "global-stop,role-stop,am-stop" ] &&
    [ "$(setting '.hooks.SessionEnd[0].hooks[0].command')" = global-end ] &&
    [ "$(setting '.hooks.Notification[0].hooks[0].command')" = am-notify ] && echo 0 || echo 1)"
check "isolate: global permissions kept, overlay over global, launcher over both" \
  "$([ "$(setting '.permissions.allow[0] + "|" + .outputStyle + "|" + .statusLine + "|" + .env.AGENT_MANAGER_SESSION')" = "Bash(git status)|default|launcher|6c328783" ] && echo 0 || echo 1)"
EXPECTED="$(printf '# User\n\n- zsh\n\n# Tools\n\nUse uv.\n\n# Project instructions (%s)\n\n# Project\n\nCodeword.\n\nRole instructions.' "$TMP/project/CLAUDE.md")"
check "isolate: kept sections in file order, project CLAUDE.md, append" \
  "$([ "$(cat "$FAKE_OUT/instructions.md")" = "$EXPECTED" ] && echo 0 || echo 1)"
check "isolate: only the kept skills are in the plugin dir" \
  "$([ "$(cat "$FAKE_OUT/skills")" = zen-flow ] && echo 0 || echo 1)"
(cd "$TMP/bare" && shim "$PROMPT" --settings "$LAUNCHER")
EXPECTED="$(printf '# User\n\n- zsh\n\n# Tools\n\nUse uv.\n\nRole instructions.')"
check "isolate: a working directory without CLAUDE.md" \
  "$([ "$(cat "$FAKE_OUT/instructions.md")" = "$EXPECTED" ] && echo 0 || echo 1)"
export ZEN_ROLE=full

# --- errors: one line, no CLI ----------------------------------------------------
fails_clean() { # name, expected text, command...
  local name="$1" text="$2" out code
  shift 2
  rm -f "$FAKE_OUT/runs"
  out="$("$@" 2>&1)"
  code=$?
  if [ "$code" != 0 ] && [ "$(printf '%s\n' "$out" | wc -l)" -eq 1 ] &&
    [[ "$out" = *"$text"* ]] && [ ! -e "$FAKE_OUT/runs" ]; then
    ok "$name"
  else
    bad "$name ($code: $out)"
  fi
}
ZEN_ROLE=nope fails_clean "error: unknown role" "unknown role 'nope'" shim "$PROMPT"
echo '{not json' >"$ROLES"
fails_clean "error: invalid roles.json" "not valid JSON" shim "$PROMPT"
rm "$ROLES"
fails_clean "error: unreadable roles.json" "cannot read" shim "$PROMPT"
echo '{"full": {"saved": "worker-rules", "autocompact": 5}}' >"$ROLES"
fails_clean "error: invalid role" "full.autocompact" shim "$PROMPT"
write_roles
sed -i.bak 's/^# Tools$/# Tooling/' "$HOME/.claude/CLAUDE.md"
ZEN_ROLE=iso fails_clean "error: a kept section is gone" "iso.instructions.keep: 'Tools'" shim "$PROMPT"
mv "$HOME/.claude/CLAUDE.md.bak" "$HOME/.claude/CLAUDE.md"
mv "$HOME/.claude/settings.json" "$HOME/.claude/settings.json.bak"
ZEN_ROLE=iso fails_clean "error: isolate without the global settings" "cannot read" shim "$PROMPT"
mv "$HOME/.claude/settings.json.bak" "$HOME/.claude/settings.json"
fails_clean "error: unreadable launcher settings" "launcher --settings" \
  shim --settings "$TMP/hooks/gone.settings.json" "$PROMPT"
no_real() { PATH="$SHIM_DIR:$SHIM_DIR/" "$BASH" "$SHIM" "$@"; }
fails_clean "error: no real claude, shim twice on PATH" "no real claude" no_real "$PROMPT"

rm -f "$FAKE_OUT/runs"
PATH="$SHIM_DIR:$SHIM_DIR/:$TMP/bin:$PATH" claude "$PROMPT"
check "shim twice on PATH: the real CLI runs once" \
  "$([ "$(wc -l <"$FAKE_OUT/runs" | tr -d ' ')" = 1 ] && echo 0 || echo 1)"

# --- run files -------------------------------------------------------------------
rm -rf "$RUN"
mkdir -p "$RUN/gone.settings.json" "$RUN/pid-999999"
echo "$TMP/hooks/gone.settings.json" >"$RUN/gone.settings.json/launcher"
echo '{}' >"$RUN/gone.settings.json/settings.json"
mkdir -p "$RUN/live.settings.json"
echo "$TMP/hooks/live.settings.json" >"$RUN/live.settings.json/launcher"
echo "$$" >"$RUN/live.settings.json/pid"
for id in aaaa bbbb cccc; do
  jq --arg id "$id" '.env.AGENT_MANAGER_SESSION = $id' "$LAUNCHER" >"$TMP/hooks/$id.settings.json"
done
for id in aaaa bbbb cccc; do
  FAKE_OUT="$TMP/out-$id" shim "$PROMPT" --settings "$TMP/hooks/$id.settings.json" &
done
wait
complete=0
for id in aaaa bbbb cccc; do
  [ "$(jq -r '.env.AGENT_MANAGER_SESSION + .outputStyle' "$TMP/out-$id/settings.json" 2>/dev/null)" = "${id}default" ] ||
    complete=1
  [ "$(cat "$RUN/$id.settings.json/launcher")" = "$TMP/hooks/$id.settings.json" ] || complete=1
done
check "run files: three launches together, each set complete" "$complete"
check "run files: a set whose launcher file is gone is pruned" \
  "$([ ! -e "$RUN/gone.settings.json" ] && echo 0 || echo 1)"
check "run files: a set whose process still runs is kept" \
  "$([ -d "$RUN/live.settings.json" ] && echo 0 || echo 1)"
check "run files: a set whose process is gone is pruned" \
  "$([ ! -e "$RUN/pid-999999" ] && echo 0 || echo 1)"
shim "$PROMPT" --settings "$TMP/hooks/aaaa.settings.json"
check "run files: live sets survive a later launch" \
  "$([ -f "$RUN/bbbb.settings.json/settings.json" ] && [ -f "$RUN/cccc.settings.json/settings.json" ] && echo 0 || echo 1)"

echo
if [ "$FAILS" = 0 ]; then echo "all passed"; else
  echo "$FAILS failed"
  exit 1
fi
