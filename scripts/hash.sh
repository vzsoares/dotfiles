#!/usr/bin/env bash
set -e

# If files are passed directly, hash them and exit
if [ "$#" -gt 0 ] && [ -f "$1" ]; then
    sha256sum "$@" | sort
    exit 0
fi

if ! command -v gum &>/dev/null; then
    echo "Usage: zen-hash [file ...]" >&2
    echo "       zen-hash [dir]      # interactive file picker (requires gum)" >&2
    exit 1
fi

DIR="${1:-.}"

FILES=$(find "$DIR" -type f | sort)

if [ -z "$FILES" ]; then
    gum style --foreground 196 "No files found in $DIR"
    exit 1
fi

SELECTED=$(echo "$FILES" | gum choose --no-limit --header "Pick files to hash:")

if [ -z "$SELECTED" ]; then
    gum style --foreground 196 "No files selected."
    exit 0
fi

echo "$SELECTED" | xargs sha256sum | sort
