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

gum style --bold --border double --padding "0 2" --border-foreground 212 "Dev Release"

CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
gum style --faint "Branch: $CURRENT_BRANCH"

# --- Detect current version ---

detect_version() {
    # 1. pyproject.toml
    if [ -f "pyproject.toml" ]; then
        ver=$(grep -Po '(?<=^version = ")[^"]+' pyproject.toml 2>/dev/null || true)
        if [ -n "$ver" ]; then
            echo "$ver"
            return
        fi
    fi

    # 2. package.json
    if [ -f "package.json" ]; then
        ver=$(grep -Po '(?<="version": ")[^"]+' package.json 2>/dev/null | head -1 || true)
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

# --- Pick next version ---

PATCH_VER="$MAJOR.$MINOR.$((PATCH + 1))-dev.1"
MINOR_VER="$MAJOR.$((MINOR + 1)).0-dev.1"
MAJOR_VER="$((MAJOR + 1)).0.0-dev.1"

if [ -n "$DEV_NUM" ]; then
    NEXT_DEV=$((DEV_NUM + 1))
    CONTINUE_VER="$MAJOR.$MINOR.$PATCH-dev.$NEXT_DEV"

    BUMP=$(gum choose --header "Version bump?" \
        "continue  ($CONTINUE_VER)" \
        "patch     ($PATCH_VER)" \
        "minor     ($MINOR_VER)" \
        "major     ($MAJOR_VER)" \
        "custom")
else
    BUMP=$(gum choose --header "Version bump?" \
        "patch     ($PATCH_VER)" \
        "minor     ($MINOR_VER)" \
        "major     ($MAJOR_VER)" \
        "custom")
fi

case "$BUMP" in
    continue*) VERSION="$CONTINUE_VER" ;;
    patch*)    VERSION="$PATCH_VER" ;;
    minor*)    VERSION="$MINOR_VER" ;;
    major*)    VERSION="$MAJOR_VER" ;;
    custom)
        VERSION=$(gum input --placeholder "X.Y.Z-dev.N" --header "Custom version")
        if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-dev\.[0-9]+)?$ ]]; then
            gum style --foreground 196 "Invalid version '$VERSION'. Expected X.Y.Z or X.Y.Z-dev.N."
            exit 1
        fi
        ;;
esac

gum style --bold --foreground 212 "Releasing v$VERSION"

# --- Update version in files ---

UPDATED_FILES=()

if [ -f "package.json" ]; then
    sed -i "s/\"version\": \"[^\"]*\"/\"version\": \"$VERSION\"/" package.json
    UPDATED_FILES+=("package.json")
fi

if [ -f "pyproject.toml" ]; then
    sed -i "s/^version = \"[^\"]*\"/version = \"$VERSION\"/" pyproject.toml
    UPDATED_FILES+=("pyproject.toml")
fi

CONFIG_FILES=$(find . -maxdepth 4 \( -name "config.py" -o -name "settings.py" \) \
    -not -path "*/.*" -not -path "*/venv/*" \
    -not -path "*/node_modules/*" -not -path "*/target/*" 2>/dev/null || true)

for f in $CONFIG_FILES; do
    if grep -qE "^[[:blank:]]*VERSION: str =" "$f"; then
        sed -i "s/^\([[:blank:]]*\)VERSION: str = \"[^\"]*\"/\1VERSION: str = \"$VERSION\"/" "$f"
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

git tag -a "v$VERSION" -m "Dev release v$VERSION"

gum style --bold --foreground 82 "Tagged v$VERSION on $CURRENT_BRANCH"

# --- Push ---

if gum confirm "Push $CURRENT_BRANCH and tags to origin?"; then
    gum spin --title "Pushing $CURRENT_BRANCH..." -- git push origin "$CURRENT_BRANCH" --follow-tags
    gum style --foreground 82 "Pushed $CURRENT_BRANCH"
else
    gum style --faint "Skipped push. Don't forget to push later."
fi

gum style --bold --border double --padding "0 2" --border-foreground 82 "Dev release v$VERSION complete"
