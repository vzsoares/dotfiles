#!/bin/bash
# Claude Code web "Setup script": installs personal CLAUDE.md + output style in the VM.
# Paste into claude.ai/code → Environments → <env> → Setup script.
set -euo pipefail
RAW=https://raw.githubusercontent.com/vzsoares/dotfiles/master/.claude
mkdir -p ~/.claude/output-styles
curl -fsSL "$RAW/CLAUDE.cloud.md" -o ~/.claude/CLAUDE.md
curl -fsSL "$RAW/output-styles/adhd.md" -o ~/.claude/output-styles/adhd.md
echo "claude cloud setup: $(wc -c < ~/.claude/CLAUDE.md) bytes CLAUDE.md, $(wc -c < ~/.claude/output-styles/adhd.md) bytes adhd.md"
