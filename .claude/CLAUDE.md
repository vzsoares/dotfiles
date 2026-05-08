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

# Knowledge bases

Two LLM-maintained knowledge bases are available — invoke their skills (`/wiki`, `/second-brain`) for full operations.

- **`/wiki`** — per-project wiki at `docs/wiki/`. Run `/wiki connect` to wire a project's `CLAUDE.md`. Use it for project architecture, modules, features, conventions, and ADRs.
- **`/second-brain`** — global personal knowledge base (Obsidian vault). Config at `~/.claude/second-brain.json`. Run `/second-brain connect` in a project to wire it. Use it for cross-project knowledge, work topics, people, research, and personal life.

Proactive triggers (suggest the relevant `ingest`):
- After completing a significant feature or architectural decision
- When the user mentions interesting sources, articles, or learnings
- When knowledge has cross-session or cross-project value worth persisting

Before answering project questions, check the project wiki first; before answering cross-project questions, query the second brain.
