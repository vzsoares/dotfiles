#!/usr/bin/env bash
set -e

for dep in fzf gum; do
    if ! command -v "$dep" &>/dev/null; then
        echo "Error: $dep is required."
        exit 1
    fi
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Aliases: short name -> "script.ext baked-in args". They show up in the picker
# alongside real scripts and fuzzy-match like them; forwarded args are appended
# after the baked-in ones.
declare -A ALIASES=(
    ["release-dev"]="release.py --dev"
)

# Scripts you reach for most. They sit at the top of the list in this order,
# regardless of alphabetical sorting or any scripts added later.
PINNED=(
    "commit.py"
    "release-dev"
    "release.py"
)

# Helpers that other things call rather than ones you pick by hand. They stay
# selectable (and fuzzy-matchable) but sort to the bottom, so the scripts you
# actually reach for keep the top of the list.
DEMOTED=(
    "bell.sh"
    "cava.sh"
    "aerospace-toggle.sh"
)

SCRIPTS=$(find "$SCRIPT_DIR" -maxdepth 1 \( -name "*.sh" -o -name "*.py" \) -not -name "$(basename "$0")" -not -name "test_*.py" -exec basename {} \; | sort)

if [ -z "$SCRIPTS" ]; then
    gum style --foreground 196 "No scripts found in $SCRIPT_DIR"
    exit 1
fi

# Combined, selectable list: PINNED first (in that order), then alias names +
# script files sorted together, with anything in DEMOTED moved to the end.
ALL=$(printf '%s\n' "${!ALIASES[@]}" "$SCRIPTS" | sort)
in_list() {
    local needle="$1" item
    shift
    for item in "$@"; do [ "$item" = "$needle" ] && return 0; done
    return 1
}
CHOICES=$(
    for name in "${PINNED[@]}"; do
        grep -qxF "$name" <<<"$ALL" && echo "$name"
    done
    while IFS= read -r name; do
        [ -z "$name" ] && continue
        in_list "$name" "${PINNED[@]}" "${DEMOTED[@]}" || echo "$name"
    done <<<"$ALL"
    for name in "${DEMOTED[@]}"; do
        grep -qxF "$name" <<<"$ALL" && echo "$name"
    done
)

# If the first arg isn't a flag, use it to fuzzy-match a choice; rest forwarded.
CANDIDATES="$CHOICES"
if [ "$#" -gt 0 ] && [[ "$1" != -* ]]; then
    QUERY="$1"
    shift
    # fzf -f filters and ranks silently (no UI) so gum keeps the visible styling.
    CANDIDATES=$(echo "$CHOICES" | fzf -f "$QUERY" || true)
    if [ -z "$CANDIDATES" ]; then
        gum style --foreground 196 "No script matching '$QUERY' in $SCRIPT_DIR"
        exit 1
    fi
fi

# Auto-run a single match; otherwise let gum pick from the candidates.
if [ "$(echo "$CANDIDATES" | wc -l)" -eq 1 ]; then
    CHOICE="$CANDIDATES"
else
    CHOICE=$(echo "$CANDIDATES" | gum choose --header "Run a script:")
fi

# Resolve an alias to its script + baked-in args (prepended to forwarded args).
if [ -n "${ALIASES[$CHOICE]:-}" ]; then
    read -ra PARTS <<<"${ALIASES[$CHOICE]}"
    CHOICE="${PARTS[0]}"
    set -- "${PARTS[@]:1}" "$@"
fi

gum style --faint "Running $CHOICE $*..."
exec "$SCRIPT_DIR/$CHOICE" "$@"
