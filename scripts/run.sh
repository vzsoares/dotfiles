#!/bin/bash
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

SCRIPTS=$(find "$SCRIPT_DIR" -maxdepth 1 \( -name "*.sh" -o -name "*.py" \) -not -name "$(basename "$0")" -not -name "test_*.py" -exec basename {} \; | sort)

if [ -z "$SCRIPTS" ]; then
    gum style --foreground 196 "No scripts found in $SCRIPT_DIR"
    exit 1
fi

# Combined, selectable list: alias names first, then the script files.
CHOICES=$(printf '%s\n' "${!ALIASES[@]}" "$SCRIPTS")

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
