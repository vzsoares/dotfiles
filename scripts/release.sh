#!/bin/bash
set -e

# Usage check
if [ -z "$1" ]; then
    echo "Usage: $0 <version>"
    echo "Example: $0 1.2.3"
    exit 1
fi

VERSION=$1
DEV_BRANCH="dev"
PROD_BRANCH="prod"

# Ensure we are in a clean state
if [ -n "$(git status --porcelain)" ]; then
    echo "Error: Working directory is not clean. Commit or stash changes first."
    exit 1
fi

echo "🚀 Starting release process for version $VERSION..."

# 1. Switch to prod
echo "Switching to $PROD_BRANCH..."
git checkout $PROD_BRANCH
git pull origin $PROD_BRANCH

# 2. Merge dev into prod
echo "Merging $DEV_BRANCH into $PROD_BRANCH..."
git merge $DEV_BRANCH --no-ff -m "Release version $VERSION"

# 3. Update version in files
echo "Updating versions..."
UPDATED_FILES=()

if [ -f "package.json" ]; then
    echo "Updating package.json..."
    sed -i "s/\"version\": \"[^\"]*\"/\"version\": \"$VERSION\"/" package.json
    UPDATED_FILES+=("package.json")
fi

# Find config.py files, excluding common ignore dirs
# We use -maxdepth 4 to avoid deep nested files and exclude common non-source dirs
CONFIG_FILES=$(find . -maxdepth 4 -name "config.py" -not -path "*/.*" -not -path "*/venv/*" -not -path "*/node_modules/*" -not -path "*/target/*")

for f in $CONFIG_FILES; do
    # Matches VERSION at start of line or after whitespace, ensuring it's not a suffix of another variable
    if grep -qE "^[[:blank:]]*VERSION: str =" "$f"; then
        echo "Updating $f..."
        sed -i "s/^\([[:blank:]]*\)VERSION: str = \"[^\"]*\"/\1VERSION: str = \"$VERSION\"/" "$f"
        UPDATED_FILES+=("$f")
    fi
done

# 4. Commit version bump
if [ ${#UPDATED_FILES[@]} -gt 0 ]; then
    git add "${UPDATED_FILES[@]}"
    git commit -m "chore: bump version to $VERSION"
else
    echo "⚠️ No version files found to update (checked package.json and config.py)."
fi

# 5. Git tag
echo "Tagging v$VERSION..."
git tag -a "v$VERSION" -m "Release v$VERSION"

# 6. Push and push tags
echo "Pushing $PROD_BRANCH and tags..."
git push origin $PROD_BRANCH --follow-tags

# 7. Change back to dev
echo "Returning to $DEV_BRANCH..."
git checkout $DEV_BRANCH

# 8. Rebase with prod
echo "Rebasing $DEV_BRANCH with $PROD_BRANCH..."
git rebase $PROD_BRANCH

# 9. Push dev
echo "Pushing $DEV_BRANCH..."
git push origin $DEV_BRANCH

echo "✅ Release $VERSION complete!"
