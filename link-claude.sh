#!/usr/bin/env bash

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOTFILES_CLAUDE="$SCRIPT_DIR/../.claude"
TARGET_DIR="$HOME/.claude"

# Settings
rm -f "$TARGET_DIR/settings.json"
ln -s "$DOTFILES_CLAUDE/settings.json" "$TARGET_DIR/settings.json"
echo "linked settings.json"

# Skills
mkdir -p "$TARGET_DIR/skills"

# Bruno skill
rm -rf "$TARGET_DIR/skills/bruno"
ln -s "$DOTFILES_CLAUDE/skills/bruno" "$TARGET_DIR/skills/bruno"
echo "linked skills/bruno"

echo "done"
