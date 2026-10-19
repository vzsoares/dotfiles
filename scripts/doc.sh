#!/usr/bin/env bash
set -e

# zen-doc — add documents (PDF, DOCX, PPTX, XLSX, HTML, EPUB, ...) to the
# second-brain vault as searchable Markdown, then refresh the qmd index.
#
#   zen-doc add <file...> [--notebook NAME] [--docling]
#   zen-doc reindex
#
# The original lands in <vault>/raw/docs/<notebook>/ with a converted
# <name>.md beside it. Vault path comes from ~/.claude/second-brain.json.

CONFIG="$HOME/.claude/second-brain.json"
COLLECTION="valt"

usage() {
    cat <<'EOF'
usage: zen-doc add <file...> [--notebook NAME] [--docling]
       zen-doc reindex

  add        copy files into <vault>/raw/docs/<notebook>/, convert to .md, reindex
  reindex    re-scan the vault and refresh embeddings (qmd update + embed)

  -n, --notebook NAME   group sources under raw/docs/NAME (default: inbox)
  -d, --docling         convert with docling (tables, multi-column, scans);
                        default is markitdown (fast)
EOF
}

vault_path() {
    [ -f "$CONFIG" ] || { echo "zen-doc: missing $CONFIG" >&2; exit 1; }
    jq -r '.vault' "$CONFIG"
}

reindex() {
    command -v qmd &>/dev/null || { echo "zen-doc: qmd not installed (bun install -g @tobilu/qmd)" >&2; exit 1; }
    qmd update
    qmd embed -c "$COLLECTION"
}

convert() {
    local src="$1" out="$2" engine="$3"
    case "$engine" in
        docling)
            # docling writes <stem>.md into --output; it cannot stream to stdout
            local tmp
            tmp="$(mktemp -d)"
            uvx docling "$src" --to md --output "$tmp" >&2
            cat "$tmp"/*.md
            rm -rf "$tmp"
            ;;
        *)
            uvx --from 'markitdown[all]' markitdown "$src"
            ;;
    esac > "$out.body"

    {
        echo "---"
        echo "source: $(basename "$src")"
        echo "converter: $engine"
        echo "converted: $(date +%Y-%m-%d)"
        echo "---"
        echo
        cat "$out.body"
    } > "$out"
    rm "$out.body"
}

cmd_add() {
    local notebook="inbox" engine="markitdown" files=()
    while [ "$#" -gt 0 ]; do
        case "$1" in
            -n|--notebook) notebook="$2"; shift 2 ;;
            -d|--docling) engine="docling"; shift ;;
            -h|--help) usage; exit 0 ;;
            *) files+=("$1"); shift ;;
        esac
    done
    [ "${#files[@]}" -gt 0 ] || { usage >&2; exit 1; }

    local dest
    dest="$(vault_path)/raw/docs/$notebook"
    mkdir -p "$dest"

    for f in "${files[@]}"; do
        [ -f "$f" ] || { echo "zen-doc: not a file: $f" >&2; exit 1; }
        local name stem
        name="$(basename "$f")"
        stem="${name%.*}"
        cp "$f" "$dest/$name"
        case "$name" in
            *.md|*.txt) echo "copied  $notebook/$name" ;;
            *)
                convert "$dest/$name" "$dest/$stem.md" "$engine"
                echo "converted  $notebook/$name -> $stem.md ($(wc -l < "$dest/$stem.md" | tr -d ' ') lines)"
                ;;
        esac
    done

    reindex
}

case "${1:-}" in
    add) shift; cmd_add "$@" ;;
    reindex) reindex ;;
    -h|--help|"") usage ;;
    *) usage >&2; exit 1 ;;
esac
