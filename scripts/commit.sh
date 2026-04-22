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

generate_message() {
    git diff --cached | claude -p "Write a concise Conventional Commit message for these changes. Return only the message — no co-author, no attribution, no trailers, no code fences."
}

while true; do
    MESSAGE=$(gum spin --show-output --title "Generating commit message..." -- bash -c "$(declare -f generate_message); generate_message")

    if [ -z "$MESSAGE" ]; then
        gum style --foreground 196 "Claude returned an empty message."
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
