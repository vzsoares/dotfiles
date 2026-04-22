#!/bin/bash
set -e

# Require gum
if ! command -v gum &>/dev/null; then
    echo "Error: gum is required. Install it: https://github.com/charmbracelet/gum"
    exit 1
fi

# Require claude
if ! command -v claude &>/dev/null; then
    gum style --foreground 196 "Error: claude CLI is required."
    exit 1
fi

# Must be in a git repo
if ! git rev-parse --git-dir &>/dev/null; then
    gum style --foreground 196 "Error: Not inside a git repository."
    exit 1
fi

gum style --bold --border double --padding "0 2" --border-foreground 212 "Commit"

# --- Stage files if nothing is staged ---

if git diff --cached --quiet; then
    UNSTAGED=$(git status --porcelain | awk '{print $2}')
    if [ -z "$UNSTAGED" ]; then
        gum style --foreground 196 "Nothing to commit. Working tree clean."
        exit 1
    fi

    gum style --faint "No staged changes. Select files to stage:"
    SELECTED=$(echo "$UNSTAGED" | gum choose --no-limit --header "Stage which files? (space to select, enter to confirm)")

    if [ -z "$SELECTED" ]; then
        gum style --foreground 196 "No files selected."
        exit 1
    fi

    echo "$SELECTED" | xargs git add
    gum style --faint "Staged: $(echo "$SELECTED" | tr '\n' ' ')"
fi

# --- Generate commit message ---

DIFF_FILE=$(mktemp)
trap 'rm -f "$DIFF_FILE"' EXIT

CONTEXT=""
PROMPT_BASE="Write a concise Conventional Commit message for these changes. Return only the message — no co-author, no attribution, no trailers, no code fences."

while true; do
    git diff --cached >"$DIFF_FILE"

    if [ -n "$CONTEXT" ]; then
        FULL_PROMPT="$PROMPT_BASE

Additional guidance from the user: $CONTEXT"
    else
        FULL_PROMPT="$PROMPT_BASE"
    fi

    MESSAGE=$(gum spin --show-output --title "Generating commit message..." -- \
        bash -c 'claude -p --tools "" --strict-mcp-config --no-session-persistence --disable-slash-commands --model haiku "$2" <"$1"' _ "$DIFF_FILE" "$FULL_PROMPT") || true

    if [ -z "$MESSAGE" ]; then
        gum style --foreground 196 "Claude returned an empty message."
        exit 1
    fi

    # Catch CLI errors that slip through as output (login prompts, box-drawing UI, etc.)
    if echo "$MESSAGE" | grep -qE '^[[:space:]]*[┌└│├]|Please run /login|Not logged in'; then
        gum style --foreground 196 "Claude CLI returned an error instead of a message:"
        echo "$MESSAGE"
        exit 1
    fi

    gum style --border normal --padding "0 1" --border-foreground 212 "$MESSAGE"

    ACTION=$(gum choose --header "What now?" "commit" "edit" "regenerate" "cancel")

    case "$ACTION" in
        commit)
            break
            ;;
        edit)
            MESSAGE=$(gum write --value "$MESSAGE" --header "Edit commit message (ctrl+d to save)")
            if [ -z "$MESSAGE" ]; then
                gum style --foreground 196 "Empty message. Aborting."
                exit 1
            fi
            break
            ;;
        regenerate)
            CONTEXT=$(gum write --placeholder "e.g. 'this is a fix, not a feat' or 'mention the bug id' — empty to just retry" --header "Extra guidance (ctrl+d to submit)")
            continue
            ;;
        cancel)
            gum style --faint "Aborted."
            exit 0
            ;;
    esac
done

# --- Commit ---

git commit -m "$MESSAGE"
gum style --bold --foreground 82 "Committed"
