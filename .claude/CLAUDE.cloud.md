# User: zenha

Cloud-session variant of my personal CLAUDE.md (Claude Code web). The cloud VM
has no `zen-commit`, `zen-release`, Playwright MCP, second-brain vault or local
dotfiles — this file omits those. Full version: `.claude/CLAUDE.md` in the same repo.

- Editor: neovim
- Shell: zsh (oh-my-zsh) on my machines; the VM shell is bash
- Git: vzsoares

# Quality checks

- Always run lint, format, type check, and tests before delivering a change
- Fix any issues found before presenting the result
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

# Commits and releases

`zen-commit` / `zen-release` are not available in the cloud. Use Conventional
Commits by hand (`feat:`, `fix:`, `chore:` …), scan the diff for secrets before
committing, and never create release tags from a cloud session.

# Task workflow — `/zen-flow`

All my tasks live on the Notion **Main Board** (`/zen-flow` skill). In the cloud
the skill's private config (`<vault>/config/zen-flow.json`) is unavailable, so
board IDs can't be resolved: do not guess IDs or status names. If a board write
is needed, tell me what to mark and I'll do it locally.

# Public dotfiles

My personal Claude config is a **public** repo. Never write secrets, private
resource IDs, or personal details into `~/.claude/` files.

# Package managers

- Python: use `uv`
- Node: use `bun`

# Automated memory rules

- After making an error, log a reference memory documenting what went wrong and the correct behavior, so it's not repeated in future conversations

# Knowledge bases

- **`/wiki`** — per-project wiki at `docs/wiki/`. Use it for project architecture,
  modules, features, conventions, and ADRs. Before answering project questions,
  check the project wiki first. After a significant feature or decision, suggest
  `/wiki ingest`.
- `/second-brain` (Obsidian vault) is local-only; not reachable from the cloud.
