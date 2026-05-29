#!/usr/bin/env bash
# Claude Code status line — mirrors the zenha zsh theme (Rosé Pine palette)
# Colors: cyan=#9ccfd8 blue=#31748f red=#eb6f92 gold=#f6c177 text=#e0def4

input=$(cat)

JQ=$(which jq 2>/dev/null || echo /home/zizmackrok/.local/share/mise/installs/jq/latest/jq)

# Directory (basename, like %c)
cwd=$(echo "$input" | "$JQ" -r '.cwd // .workspace.current_dir')
dir=$(basename "$cwd")

# Git branch + dirty flag (skip optional locks to avoid blocking)
branch=$(git -C "$cwd" symbolic-ref --short HEAD 2>/dev/null \
  || git -C "$cwd" rev-parse --short HEAD 2>/dev/null)
dirty=""
if [ -n "$branch" ]; then
  if ! git -C "$cwd" diff --quiet 2>/dev/null || ! git -C "$cwd" diff --cached --quiet 2>/dev/null; then
    dirty=" \033[1;38;2;246;193;119m✗\033[0m"
  fi
fi

# Model display name
model=$(echo "$input" | "$JQ" -r '.model.display_name // empty')

# Context remaining
remaining=$(echo "$input" | "$JQ" -r '.context_window.remaining_percentage // empty')

# Assemble output
# cyan dir
printf "\033[1;38;2;156;207;216m%s\033[0m" "$dir"

# git branch
if [ -n "$branch" ]; then
  printf " \033[1;38;2;49;116;143mgit:\033[1;38;2;235;111;146m%s\033[0m" "$branch"
  printf "%b" "$dirty"
fi

# model (dimmed gold)
if [ -n "$model" ]; then
  printf " \033[2;38;2;246;193;119m%s\033[0m" "$model"
fi

# context remaining
if [ -n "$remaining" ]; then
  used_int=$(printf "%.0f" "$(echo "100 - $remaining" | bc)")
  printf " \033[2;38;2;224;222;244mctx:%s%%\033[0m" "$used_int"
fi

printf "\n"
