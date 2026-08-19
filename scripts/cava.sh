#!/usr/bin/env bash
# zen-cava — pick a cava preset and run it.
#
#   zen-cava            gum-pick a preset
#   zen-cava rounded    run that preset directly
#   zen-cava --list     print preset names
#
# Presets live in cava/presets/ in the dotfiles repo.

set -e

# Resolve through the ~/.local/bin symlink back to the repo, then up to the
# repo root — this lives in scripts/ (so `run` lists it) but the presets live
# in cava/ alongside the rest of the cava config.
DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
PRESETS="$(dirname "$DIR")/cava/presets"

[ -d "$PRESETS" ] || { echo "zen-cava: no presets dir at $PRESETS" >&2; exit 1; }

list() { find "$PRESETS" -maxdepth 1 -type f -exec basename {} \; | sort; }

case "${1:-}" in
    --list | -l) list; exit 0 ;;
    -h | --help) sed -n '2,9p' "$0" | sed 's/^# \?//'; exit 0 ;;
esac

choice="${1:-}"
if [ -z "$choice" ]; then
    if command -v gum >/dev/null 2>&1; then
        choice=$(list | gum choose --header "cava preset:")
    else
        echo "zen-cava: gum not found; pass a preset name" >&2
        list >&2
        exit 2
    fi
fi
[ -n "$choice" ] || exit 0 # picker cancelled

config="$PRESETS/$choice"
if [ ! -f "$config" ]; then
    echo "zen-cava: no preset '$choice'" >&2
    list >&2
    exit 1
fi

exec cava -p "$config"
