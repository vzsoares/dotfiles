#!/bin/bash
set -e

if ! command -v gum &>/dev/null; then
    echo "Error: gum is required. Install it: https://github.com/charmbracelet/gum"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

SCRIPTS=$(find "$SCRIPT_DIR" -maxdepth 1 -name "*.sh" -not -name "$(basename "$0")" -exec basename {} \; | sort)

if [ -z "$SCRIPTS" ]; then
    gum style --foreground 196 "No scripts found in $SCRIPT_DIR"
    exit 1
fi

CHOICE=$(echo "$SCRIPTS" | gum choose --header "Run a script:")

gum style --faint "Running $CHOICE..."
exec "$SCRIPT_DIR/$CHOICE" "$@"
