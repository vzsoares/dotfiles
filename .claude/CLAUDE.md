# User: zenha

- Editor: neovim
- Shell: zsh (oh-my-zsh)
- OS: Manjaro Linux (i3) on one machine, macOS (Apple Silicon) on the other
- Git: vzsoares

# Quality checks

- Always run lint, format, type check, and tests before delivering a change
- Fix any issues found before presenting the result
- Use the Playwright MCP to visually test frontend changes in the browser before delivering
- Avoid type coercions (`as`) and the `any` type — use proper typing instead

# Terminal commands I will paste (zsh)

Commands you hand me are run by pasting into an interactive zsh prompt. They
must work on the first try:

- No nested-quote tricks (`'"'"'`, `tr -d \"\'`, `"$(... '...' ...)"`). If
  stripping quotes is needed, use `sed -n 's/^KEY=//p'` or a separate step.
- Never `source` a `.env`: values are often unquoted (`&`, `?`, spaces) and
  zsh aborts the whole file. Extract one var:
  `export KEY=$(sed -n 's/^KEY=//p' path/.env)`.
- Add a verification step with the expected output (`echo "${#KEY} chars"`,
  `curl -w "%{http_code}"`) so a silent empty variable is caught immediately.
- Secrets never pass through chat: have me export them in my terminal and
  reference `$VAR` in later commands.

# Code comments

- Comments are objective and minimal: state only what the code cannot show
  (a non-obvious constraint, invariant, or gotcha), in one or two lines
- Never narrate change history in comments ("previously this did X",
  "changed to fix Y") — that belongs in git, not the code
- No essays justifying that code exists or explaining what the next line does
- When editing commented code, trim stale or prolix comments instead of
  appending to them

# Workflow tooling

Two personal CLI commands are on PATH in my environments. Prefer them over
hand-rolling commits / releases.

- **`zen-commit`** — Conventional-Commit helper: stage (or `--all`), scan for
  secrets, AI-generate the message, commit. Headless: `zen-commit --all --yes`
  (AI message) or `zen-commit --all -m "feat: …"`; aborts on secret findings.
- **`zen-release`** — release orchestrator (version bump, tag, changelog, publish,
  GitHub release). `zen-release` (full) or `zen-release --dev` (quick `-dev.N`).
  Headless: add `--yes --bump <patch|minor|major>`.

Both are gum-driven; without the headless flags they prompt, so run those forms
in a real terminal.

# Task workflow — `/zen-flow` (always use)

All my tasks (Approva work and personal) live on the Notion **Main Board**. The
`zen-flow` skill holds its IDs, statuses, and operations — invoke it before any
board read or write; never guess IDs or status names.

Use it proactively, without being asked:

- **Session start / "what now?"** — when I ask what to do, plan the day, or start
  work without a clear target, run `next` and recommend one task.
- **Starting work** — when I begin something that matches a board task, set it to
  `In Progress`. If it matches nothing, offer to `add` it.
- **Finishing work** — after a commit or release (`zen-commit`, `zen-release`) that
  completes a task, mark it `✅ Done` with Conclusion Date.
- **New ideas mid-session** — when I mention a follow-up, bug, or "we should also…",
  offer to `add` it instead of letting it get lost in the chat.
- **Scope creep** — if I drift to something unrelated, name the current In Progress
  task and ask whether to park it or switch.

Keep the board truthful: nothing I finished should stay open. The skill grows with my routine — when a new habit or
tool becomes part of how I work, add a section to it rather than to this file.

# Public dotfiles

This file, `~/.claude/skills/`, and `~/.claude/output-styles/` are symlinks into a
**public** repo (`~/code/personal/dotfiles`). Never write secrets, private resource IDs,
or personal details into them — store those in the private vault
(`<vault>/config/*.json`) and reference it. See the dotfiles `CLAUDE.md` for the full list.

# Package managers

- Python: use `uv`
- Node: use `bun`

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
