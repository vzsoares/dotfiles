# User: zenha

- Editor: neovim
- Shell: zsh (oh-my-zsh)
- OS: Manjaro Linux (i3)
- Git: vzsoares

# Quality checks

- Always run lint, format, type check, and tests before delivering a change
- Fix any issues found before presenting the result
- Use the Playwright MCP to visually test frontend changes in the browser before delivering
- Avoid type coercions (`as`) and the `any` type — use proper typing instead

# Automated memory rules

- After making an error, log a reference memory documenting what went wrong and the correct behavior, so it's not repeated in future conversations

# Second Brain

This project is connected to the personal second brain. Config at `~/.claude/second-brain.json`.

## Usage

- `/second-brain setup [path]` — initialize vault
- `/second-brain ingest <source>` — process source into wiki pages with cross-references
- `/second-brain query <question>` — search wiki, synthesize answer with [[wiki-links]]
- `/second-brain lint` — health-check (orphans, broken links, stale content, contradictions)
- `/second-brain connect` — wire a project's CLAUDE.md to the brain
- `/second-brain status` — stats, recent log, coverage gaps

## When to use proactively

- After completing significant features or architectural decisions
- When the user mentions interesting sources, articles, or learnings
- When knowledge has cross-project value worth persisting
