#!/bin/bash
set -e

# Require gum
if ! command -v gum &>/dev/null; then
    echo "Error: gum is required. Install it: https://github.com/charmbracelet/gum"
    exit 1
fi

# Ensure we are in a clean state
if [ -n "$(git status --porcelain)" ]; then
    gum style --foreground 196 "Error: Working directory is not clean. Commit or stash changes first."
    exit 1
fi

gum style --bold --border double --padding "0 2" --border-foreground 212 "Release"

# Portable in-place sed (BSD on macOS requires an explicit backup extension)
if sed --version >/dev/null 2>&1; then
    sed_inplace() { sed -i "$@"; }
else
    sed_inplace() { sed -i '' "$@"; }
fi

# --- Branch selection ---

BRANCHES=$(git branch --format='%(refname:short)')
CURRENT_BRANCH=$(git branch --show-current)

SOURCE_BRANCH=$(gum input --placeholder "Source branch (empty = release current)" --value "dev" --header "Merge FROM which branch? (empty to release current branch)")

if [ -z "$SOURCE_BRANCH" ]; then
    NO_MERGE=1
    TARGET_BRANCH="$CURRENT_BRANCH"
    gum style --faint "Releasing current branch: $TARGET_BRANCH (no merge)"
else
    NO_MERGE=0
    if ! echo "$BRANCHES" | grep -qx "$SOURCE_BRANCH"; then
        gum style --foreground 196 "Branch '$SOURCE_BRANCH' does not exist."
        exit 1
    fi

    TARGET_BRANCH=$(gum input --placeholder "Target branch" --value "prod" --header "Merge INTO which branch?")
    if ! echo "$BRANCHES" | grep -qx "$TARGET_BRANCH"; then
        gum style --foreground 196 "Branch '$TARGET_BRANCH' does not exist."
        exit 1
    fi

    gum style --faint "$SOURCE_BRANCH -> $TARGET_BRANCH"
fi

# --- Detect current version ---

detect_version() {
    # 1. pyproject.toml
    if [ -f "pyproject.toml" ]; then
        ver=$(sed -n 's/^version = "\([^"]*\)".*/\1/p' pyproject.toml 2>/dev/null | head -1 || true)
        if [ -n "$ver" ]; then
            echo "$ver"
            return
        fi
    fi

    # 2. package.json
    if [ -f "package.json" ]; then
        ver=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' package.json 2>/dev/null | head -1 || true)
        if [ -n "$ver" ]; then
            echo "$ver"
            return
        fi
    fi

    # 3. Latest git tag
    ver=$(git tag --list 'v*' --sort=-v:refname 2>/dev/null | head -1 | sed 's/^v//' || true)
    if [ -n "$ver" ]; then
        echo "$ver"
        return
    fi

    echo "0.0.0"
}

CURRENT_VERSION=$(detect_version)

# Parse semver, optionally with -dev.N suffix
if [[ "$CURRENT_VERSION" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)(-dev\.([0-9]+))?$ ]]; then
    MAJOR="${BASH_REMATCH[1]}"
    MINOR="${BASH_REMATCH[2]}"
    PATCH="${BASH_REMATCH[3]}"
    DEV_NUM="${BASH_REMATCH[5]}"
else
    gum style --foreground 196 "Could not parse version '$CURRENT_VERSION'. Expected X.Y.Z or X.Y.Z-dev.N."
    exit 1
fi

gum style --faint "Current version: $CURRENT_VERSION"

FINALIZE_VER="$MAJOR.$MINOR.$PATCH"
PATCH_VER="$MAJOR.$MINOR.$((PATCH + 1))"
MINOR_VER="$MAJOR.$((MINOR + 1)).0"
MAJOR_VER="$((MAJOR + 1)).0.0"

if [ -n "$DEV_NUM" ]; then
    BUMP=$(gum choose --header "Version bump?" \
        "finalize  ($FINALIZE_VER)" \
        "patch     ($PATCH_VER)" \
        "minor     ($MINOR_VER)" \
        "major     ($MAJOR_VER)")
else
    BUMP=$(gum choose --header "Version bump?" \
        "patch  ($PATCH_VER)" \
        "minor  ($MINOR_VER)" \
        "major  ($MAJOR_VER)")
fi

case "$BUMP" in
    finalize*) VERSION="$FINALIZE_VER" ;;
    patch*)    VERSION="$PATCH_VER" ;;
    minor*)    VERSION="$MINOR_VER" ;;
    major*)    VERSION="$MAJOR_VER" ;;
esac

gum style --bold --foreground 212 "Releasing v$VERSION"

# --- Merge ---

if [ "$NO_MERGE" -eq 0 ]; then
    gum spin --show-error --title "Switching to $TARGET_BRANCH..." -- git checkout "$TARGET_BRANCH"
    gum spin --show-error --title "Pulling $TARGET_BRANCH..." -- git pull origin "$TARGET_BRANCH"
    gum spin --show-error --title "Merging $SOURCE_BRANCH into $TARGET_BRANCH..." -- \
        git merge "$SOURCE_BRANCH" --no-ff -m "Release version $VERSION"
fi

# --- Update version in files ---

UPDATED_FILES=()

if [ -f "package.json" ]; then
    sed_inplace "s/\"version\": \"[^\"]*\"/\"version\": \"$VERSION\"/" package.json
    UPDATED_FILES+=("package.json")
fi

if [ -f "pyproject.toml" ]; then
    sed_inplace "s/^version = \"[^\"]*\"/version = \"$VERSION\"/" pyproject.toml
    UPDATED_FILES+=("pyproject.toml")
fi

CONFIG_FILES=$(find . -maxdepth 4 \( -name "config.py" -o -name "settings.py" \) \
    -not -path "*/.*" -not -path "*/venv/*" \
    -not -path "*/node_modules/*" -not -path "*/target/*" 2>/dev/null || true)

for f in $CONFIG_FILES; do
    if grep -qE "^[[:blank:]]*VERSION: str =" "$f"; then
        sed_inplace "s/^\([[:blank:]]*\)VERSION: str = \"[^\"]*\"/\1VERSION: str = \"$VERSION\"/" "$f"
        UPDATED_FILES+=("$f")
    fi
done

if [ ${#UPDATED_FILES[@]} -gt 0 ]; then
    gum style --faint "Updated: ${UPDATED_FILES[*]}"
    git add "${UPDATED_FILES[@]}"
    git commit -m "chore: bump version to $VERSION"
else
    gum style --faint "No version files found to update."
fi

# --- Tag ---

git tag -a "v$VERSION" -m "Release v$VERSION"

gum style --bold --foreground 82 "Merged and tagged v$VERSION on $TARGET_BRANCH"

# --- Push ---

if gum confirm "Push $TARGET_BRANCH and tags to origin?"; then
    if gum spin --show-error --title "Pushing $TARGET_BRANCH..." -- git push origin "$TARGET_BRANCH" --follow-tags; then
        gum style --foreground 82 "Pushed $TARGET_BRANCH"
    else
        gum style --foreground 196 "Push failed. Resolve the issue and push manually:"
        gum style --faint "  git push origin $TARGET_BRANCH --follow-tags"
        exit 1
    fi
else
    gum style --faint "Skipped push. Don't forget to push later."
fi

# --- Rebase source branch ---

if [ "$NO_MERGE" -eq 0 ]; then
    if gum confirm "Switch back to $SOURCE_BRANCH and rebase with $TARGET_BRANCH?"; then
        gum spin --show-error --title "Switching to $SOURCE_BRANCH..." -- git checkout "$SOURCE_BRANCH"
        gum spin --show-error --title "Rebasing $SOURCE_BRANCH with $TARGET_BRANCH..." -- git rebase "$TARGET_BRANCH"

        if gum confirm "Push $SOURCE_BRANCH to origin?"; then
            if gum spin --show-error --title "Pushing $SOURCE_BRANCH..." -- git push origin "$SOURCE_BRANCH"; then
                gum style --foreground 82 "Pushed $SOURCE_BRANCH"
            else
                gum style --foreground 196 "Push failed. Resolve the issue and push manually:"
                gum style --faint "  git push origin $SOURCE_BRANCH"
                exit 1
            fi
        else
            gum style --faint "Skipped push of $SOURCE_BRANCH."
        fi
    else
        gum style --faint "Stayed on $TARGET_BRANCH. Rebase manually when ready."
    fi
fi

gum style --bold --border double --padding "0 2" --border-foreground 82 "Release v$VERSION complete"
