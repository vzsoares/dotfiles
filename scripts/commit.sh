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

# --- Guardrail: scan staged content for secrets / sensitive files ---

VIOLATIONS=""

STAGED_FILES=$(git diff --cached --name-only)
while IFS= read -r f; do
    [ -z "$f" ] && continue
    case "$f" in
        *.pem|*.key|*.pfx|*.p12|*.keystore|*.jks)
            case "$f" in *.pub|*.pub.*) ;; *) VIOLATIONS+="  • $f — sensitive crypto extension"$'\n' ;; esac
            ;;
        *id_rsa*|*id_ed25519*|*id_ecdsa*|*id_dsa*)
            case "$f" in *.pub) ;; *) VIOLATIONS+="  • $f — private SSH key"$'\n' ;; esac
            ;;
        *.env|*.env.*|.env|.env.*)
            case "$f" in *.example|*.sample|*.template) ;; *) VIOLATIONS+="  • $f — env file"$'\n' ;; esac
            ;;
        token.json|*/token.json|credentials.json|*/credentials.json|auth.json|*/auth.json|service-account*.json|*/service-account*.json)
            VIOLATIONS+="  • $f — looks like a credentials file"$'\n'
            ;;
        .npmrc|*/.npmrc)
            if git show ":$f" 2>/dev/null | grep -q '_authToken'; then
                VIOLATIONS+="  • $f — contains _authToken"$'\n'
            fi
            ;;
    esac
done <<<"$STAGED_FILES"

# Content scan — only ADDED lines (^+ not ^+++) of the staged diff
ADDED=$(git diff --cached -U0 | grep -E '^\+[^+]' || true)

# Pattern format: "regex|label" — split at the LAST '|'.
PATTERNS=(
    'AKIA[0-9A-Z]{16}|AWS Access Key ID'
    'ASIA[0-9A-Z]{16}|AWS temporary credentials'
    'gh[pousr]_[A-Za-z0-9]{36}|GitHub PAT'
    'github_pat_[A-Za-z0-9_]{80,}|GitHub fine-grained PAT'
    'glpat-[A-Za-z0-9_-]{20}|GitLab PAT'
    'sk-ant-(api03|admin01)-[A-Za-z0-9_-]{80,}|Anthropic API key'
    'sk-(proj-)?[A-Za-z0-9]{40,}|OpenAI API key'
    'xox[baprs]-[A-Za-z0-9-]{10,}|Slack token'
    'BEGIN (RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY|Private key block'
)

for p in "${PATTERNS[@]}"; do
    regex="${p%|*}"
    label="${p##*|}"
    if printf '%s' "$ADDED" | grep -qE "$regex"; then
        VIOLATIONS+="  • content matched: $label"$'\n'
    fi
done

if [ -n "$VIOLATIONS" ]; then
    gum style --bold --border thick --padding "0 2" --border-foreground 196 --foreground 196 \
        "Guardrail: possible secret / sensitive content"
    printf "%b" "$VIOLATIONS"
    echo

    DECISION=$(gum choose --header "How to proceed?" "abort" "override (commit anyway)")
    case "$DECISION" in
        abort)
            gum style --faint "Aborted. Unstage with: git reset HEAD <file>"
            exit 1
            ;;
        "override (commit anyway)")
            gum confirm --default=false "Really commit flagged content?" \
                || { gum style --faint "Aborted."; exit 1; }
            gum style --foreground 214 "Override accepted — proceeding."
            ;;
    esac
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
