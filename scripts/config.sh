#!/usr/bin/env bash
#
# zen-config — personal settings for the zen-* tools and zsh, as JSON.
#
#   zen.json (dotfiles root)     tracked, both machines. Public repo: no secrets.
#   ~/.config/zen/config.json    untracked, this machine only; deep-merged on top
#
# Usage:
#   zen-config get <key>       print one value, e.g. `zen-config get bell.enabled`;
#                              arrays print one item per line, unset prints nothing
#   zen-config show            print the merged config
#   zen-config env             zsh assignments for shell-side keys (.zshrc evals it)
#   zen-config edit [--local]  open the shared (or this machine's) file in $EDITOR
#
# Precedence: env var > machine file > zen.json > the tool's built-in default.
# Scripts read keys live, so edits apply on their next run; shell-side keys
# (`env`) apply to new shells.

# shellcheck disable=SC2016  # $ in single quotes is jq, not shell
set -euo pipefail

repo="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)"
shared="$repo/zen.json"
machine="${XDG_CONFIG_HOME:-$HOME/.config}/zen/config.json"

# .zshrc evals `env` before mise activates, so mise's jq may not be on PATH yet.
jq_bin=$(command -v jq || echo "$HOME/.local/share/mise/shims/jq")

merged() {
    local files=()
    [[ -f $shared ]] && files+=("$shared")
    [[ -f $machine ]] && files+=("$machine")
    ((${#files[@]})) || { echo '{}'; return; }
    "$jq_bin" -s 'reduce .[] as $f ({}; . * $f)' "${files[@]}"
}

cmd="${1:-}"
if [[ ! -x $jq_bin && $cmd =~ ^(get|show|env)$ ]]; then
    echo "zen-config: jq not found; built-in defaults apply" >&2
    # Shell startup evals `env`, so it must not fail there.
    [[ $cmd == env ]] && exit 0 || exit 1
fi
case "$cmd" in
    get)
        [[ $# -eq 2 ]] || { echo "usage: zen-config get <key>" >&2; exit 2; }
        # `// empty` would also drop false, so test for null explicitly.
        merged | "$jq_bin" -r --arg k "$2" '
          getpath($k | split(".")) | select(. != null)
          | if type == "array" then .[] else . end'
        ;;
    show)
        merged
        ;;
    env)
        merged | "$jq_bin" -r '
          (.bell.threshold | select(. != null)
            | ": ${ZEN_BELL_THRESHOLD:=\(tostring | @sh)}"),
          (.bell.ignore | select(. != null)
            | "(( ${#ZEN_BELL_IGNORE} )) || ZEN_BELL_IGNORE=(\(map(@sh) | join(" ")))"),
          (.git.fetchIntervalMinutes | select(. != null)
            | ": ${ZEN_FETCH_INTERVAL:=\(tostring | @sh)}")'
        ;;
    edit)
        file="$shared"
        if [[ ${2:-} == --local ]]; then
            file="$machine"
            mkdir -p "$(dirname "$file")"
            [[ -f $file ]] || echo '{}' >"$file"
        fi
        exec "${EDITOR:-nvim}" "$file"
        ;;
    -h | --help | "")
        sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
        ;;
    *)
        echo "zen-config: unknown command $cmd" >&2
        exit 2
        ;;
esac
