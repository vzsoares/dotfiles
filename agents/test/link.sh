#!/usr/bin/env bash
# shellcheck disable=SC2319
# agents/link against a temp HOME with fake codex and agy; nothing live changes.
# Run with:  bash agents/test/link.sh

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINK="$HERE/../link"
REPO="$(cd "$HERE/../.." && pwd)"
TMP="$(cd "$(mktemp -d)" && pwd -P)"
FAILS=0
trap 'rm -rf "$TMP"' EXIT

ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
check() { if [ "$2" = 0 ]; then ok "$1"; else bad "$1"; fi; }

mkdir -p "$TMP/bin"
cat >"$TMP/bin/codex" <<'FAKE'
#!/usr/bin/env bash
echo "codex $*" >>"$FAKE_LOG"
S="$FAKE_CODEX_STATE"
mkdir -p "$S"
[ "$1 $2" = "mcp get" ] && {
  [ -f "$S/$3.json" ] || { echo "Error: No MCP server named '$3' found." >&2; exit 1; }
  jq -n --slurpfile t "$S/$3.json" '{name: "x", transport: $t[0]}'
  exit 0
}
[ "$1 $2" = "mcp remove" ] && { rm -f "$S/$3.json"; exit 0; }
if [ "$1 $2" = "mcp add" ]; then
  name="$3"
  shift 3
  env='{}'
  while [ "$1" = --env ]; do
    env="$(jq -c --arg kv "$2" '. + {($kv | split("=")[0]): ($kv | split("=")[1:] | join("="))}' <<<"$env")"
    shift 2
  done
  shift
  a="$(printf '%s\n' "$@" | jq -Rn '[inputs]')"
  jq -n --argjson a "$a" --argjson e "$env" \
    '{type: "stdio", command: $a[0], args: $a[1:], env: (if $e == {} then null else $e end)}' >"$S/$name.json"
fi
FAKE
cat >"$TMP/bin/agy" <<'FAKE'
#!/usr/bin/env bash
echo "agy $*" >>"$FAKE_LOG"
if [ "$1 $2" = "mcp add" ]; then
  shift 2
  env='{}'
  while [ "$1" = --env ]; do
    env="$(jq -c --arg kv "$2" '. + {($kv | split("=")[0]): ($kv | split("=")[1:] | join("="))}' <<<"$env")"
    shift 2
  done
  name="$1"
  shift 2
  f="$HOME/.gemini/config/mcp_config.json"
  mkdir -p "$(dirname "$f")"
  [ -s "$f" ] || echo '{"mcpServers":{}}' >"$f"
  a="$(printf '%s\n' "$@" | jq -Rn '[inputs]')"
  jq --arg n "$name" --argjson a "$a" --argjson e "$env" \
    '.mcpServers[$n] = ({command: $a[0], args: $a[1:], disabled: false}
      + (if $e == {} then {} else {env: $e} end))' "$f" >"$f.new"
  mv "$f.new" "$f"
fi
FAKE
chmod +x "$TMP/bin/codex" "$TMP/bin/agy"
ln -s "$(command -v jq)" "$TMP/bin/jq"

cat >"$TMP/mcp.json" <<'J'
{"mcpServers": {
  "pw": {"type": "stdio", "command": "npx", "args": ["-y", "pw", "--out", "/tmp/x"]},
  "qmd": {"command": "qmd", "args": ["mcp"], "env": {"K": "a=b"}},
  "remote": {"type": "http", "url": "https://example.invalid/mcp"}
}}
J

export FAKE_LOG="$TMP/log"
export AGENTS_MCP_JSON="$TMP/mcp.json"
base="$TMP/bin:/usr/bin:/bin"
n=0
newhome() {
  n=$((n + 1))
  export HOME="$TMP/home$n" FAKE_CODEX_STATE="$TMP/codex$n"
  mkdir -p "$HOME"
  : >"$FAKE_LOG"
}
run() { PATH="$base" bash "$LINK" "$@" 2>&1; }
snapshot() { (cd "$HOME" && find . | LC_ALL=C sort); }

# 1. fresh run
newhome
out="$(run)"
check "fresh run exits 0" $?
src="$REPO/.claude/CLAUDE.md"
[ "$(readlink "$HOME/.pi/agent/AGENTS.md")" = "$src" ] &&
  [ "$(readlink "$HOME/.codex/AGENTS.md")" = "$src" ] &&
  [ "$(readlink "$HOME/.gemini/config/AGENTS.md")" = "$src" ]
check "AGENTS.md links point at .claude/CLAUDE.md" $?
[ "$(readlink "$HOME/.agents/skills/wiki")" = "$REPO/.claude/skills/wiki" ] &&
  [ "$(readlink "$HOME/.codex/skills/second-brain")" = "$REPO/.claude/skills/second-brain" ] &&
  [ "$(readlink "$HOME/.gemini/config/skills/wiki")" = "$REPO/.claude/skills/wiki" ]
check "skills link into all three tools" $?
[ "$(readlink "$HOME/.pi/agent/mcp.json")" = "$REPO/agents/mcp.json" ]
check "pi mcp.json still links" $?
[ "$(grep -c '^codex mcp add' "$FAKE_LOG")" = 2 ] &&
  [ "$(grep -c '^agy mcp add' "$FAKE_LOG")" = 2 ]
check "one mcp add per server per CLI, remote server skipped" $?
grep -qx 'agy mcp add --env K=a=b qmd -- qmd mcp' "$FAKE_LOG" &&
  grep -qx 'codex mcp add qmd --env K=a=b -- qmd mcp' "$FAKE_LOG" &&
  grep -qx 'agy mcp add pw -- npx -y pw --out /tmp/x' "$FAKE_LOG"
check "mcp add argv order (agy flags before the name)" $?
grep -q "skipping non-stdio MCP server 'remote'" <<<"$out"
check "warns about the non-stdio server" $?

# 2. rerun
: >"$FAKE_LOG"
out="$(run)"
! grep -qE 'linked|moved|replaced|added|updated' <<<"$out" &&
  [ "$(grep -c '^unchanged' <<<"$out")" -ge 14 ]
check "rerun prints unchanged only" $?
! grep -qE '^(codex|agy) mcp (add|remove)' "$FAKE_LOG"
check "rerun runs no add or remove" $?
! find "$HOME" -name '*.bak.*' | grep -q .
check "rerun makes no backup" $?

# 3. backups
newhome
mkdir -p "$HOME/.codex" "$HOME/.gemini/config"
echo precious >"$HOME/.codex/AGENTS.md"
ln -s /elsewhere "$HOME/.gemini/config/AGENTS.md"
out="$(run codex agy)"
bak="$(find "$HOME/.codex" -name 'AGENTS.md.bak.*')"
[ -n "$bak" ] && [ "$(cat "$bak")" = precious ] &&
  [ "$(readlink "$HOME/.codex/AGENTS.md")" = "$src" ] &&
  grep -q "moved $HOME/.codex/AGENTS.md to $bak" <<<"$out"
check "a real file moves to .bak.<epoch>, content kept, move printed" $?
[ "$(readlink "$HOME/.gemini/config/AGENTS.md")" = "$src" ] &&
  grep -q "was -> /elsewhere" <<<"$out"
check "a foreign symlink is replaced and its old target printed" $?
! find "$HOME/.gemini" -name '*.bak.*' | grep -q .
check "a foreign symlink gets no backup" $?

# 4. dry run
newhome
mkdir -p "$HOME/.codex"
echo precious >"$HOME/.codex/AGENTS.md"
before="$(snapshot)"
out="$(run --dry-run)"
check "dry run exits 0" $?
[ "$before" = "$(snapshot)" ]
check "dry run leaves HOME untouched" $?
! grep -qE '^(codex|agy) mcp (add|remove)' "$FAKE_LOG"
check "dry run runs no mutating CLI call" $?
grep -q "would move $HOME/.codex/AGENTS.md to $HOME/.codex/AGENTS.md.bak\." <<<"$out" &&
  grep -q "would link $HOME/.gemini/config/AGENTS.md -> $src" <<<"$out" &&
  grep -q 'would add codex mcp qmd' <<<"$out" &&
  grep -q 'would add agy mcp pw' <<<"$out"
check "dry run prints every planned change with its backup path" $?

# 5. changed server
newhome
run >/dev/null
jq '.args = ["other"]' "$FAKE_CODEX_STATE/qmd.json" >"$TMP/q" && mv "$TMP/q" "$FAKE_CODEX_STATE/qmd.json"
f="$HOME/.gemini/config/mcp_config.json"
jq '.mcpServers.qmd.args = ["other"]' "$f" >"$TMP/q" && mv "$TMP/q" "$f"
: >"$FAKE_LOG"
run >/dev/null
[ "$(grep -v '^codex mcp get' "$FAKE_LOG" | grep '^codex')" = "codex mcp remove qmd
codex mcp add qmd --env K=a=b -- qmd mcp" ] &&
  [ "$(grep -c '^codex mcp add' "$FAKE_LOG")" = 1 ] &&
  [ "$(grep -c '^agy mcp add' "$FAKE_LOG")" = 1 ] &&
  ! grep -q '^agy mcp remove' "$FAKE_LOG"
check "changed server: codex remove then add, agy add, others untouched" $?

# 6. agent-manager entry
newhome
mkdir -p "$HOME/.gemini/config"
echo '{"mcpServers":{"agent-manager":{"command":"am","args":[]}}}' >"$HOME/.gemini/config/mcp_config.json"
run agy >/dev/null
[ "$(jq -r '.mcpServers | keys | join(",")' "$HOME/.gemini/config/mcp_config.json")" = "agent-manager,pw,qmd" ]
check "an agent-manager entry survives a run" $?
newhome
jq '.mcpServers["agent-manager"] = {command: "am"}' "$TMP/mcp.json" >"$TMP/mcp-am.json"
before="$(snapshot)"
AGENTS_MCP_JSON="$TMP/mcp-am.json" run >/dev/null
rc=$?
[ "$rc" = 1 ] && [ "$before" = "$(snapshot)" ] && [ ! -s "$FAKE_LOG" ]
check "mcp.json naming agent-manager exits 1 and changes nothing" $?

# 7. empty and unparseable mcp_config.json
newhome
mkdir -p "$HOME/.gemini/config"
: >"$HOME/.gemini/config/mcp_config.json"
out="$(run agy)"
[ "$(jq -r '.mcpServers | keys | join(",")' "$HOME/.gemini/config/mcp_config.json")" = "pw,qmd" ]
check "a 0-byte mcp_config.json is handled" $?
newhome
mkdir -p "$HOME/.gemini/config"
printf '{\n  // mine\n  "mcpServers": {"other": {"command": "x",},},\n}\n' >"$HOME/.gemini/config/mcp_config.json"
out="$(run agy)"
grep -q 'not plain JSON' <<<"$out" && [ "$(grep -c '^agy mcp add' "$FAKE_LOG")" = 2 ]
check "an unparseable mcp_config.json falls back to add for every server" $?

# 8. CLIs missing
newhome
mkdir -p "$TMP/nocli"
ln -sf "$(command -v jq)" "$TMP/nocli/jq"
out="$(PATH="$TMP/nocli:/usr/bin:/bin" bash "$LINK" 2>&1)"
rc=$?
[ "$rc" = 0 ] && grep -q 'codex not on PATH' <<<"$out" && grep -q 'agy not on PATH' <<<"$out" &&
  [ -L "$HOME/.codex/AGENTS.md" ] && [ -L "$HOME/.gemini/config/skills/wiki" ]
check "missing codex and agy: warns, still links, exits 0" $?

# 9. targets
newhome
run codex >/dev/null
[ ! -e "$HOME/.pi" ] && [ ! -e "$HOME/.agents" ] && [ ! -e "$HOME/.gemini" ] &&
  [ -L "$HOME/.codex/AGENTS.md" ] && ! grep -q '^agy' "$FAKE_LOG"
check "target codex touches no pi or agy path" $?
run bogus >/dev/null
[ "$?" = 2 ]
check "a bad argument exits 2" $?
run --help | grep -q '^usage'
check "--help prints usage" $?

echo
if [ "$FAILS" -gt 0 ]; then
  echo "$FAILS failed"
  exit 1
fi
echo "all passed"
