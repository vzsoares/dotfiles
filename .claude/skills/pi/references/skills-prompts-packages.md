# Skills, prompt templates and packages

Source: pi 1.6.0 docs, scraped 2026-11-20. Re-check with scripts/refresh.py.

## Skills: format

pi implements the open Agent Skills spec, so a Claude Code style skill
directory works unchanged:

```
my-skill/
├── SKILL.md        # frontmatter + instructions
├── scripts/  references/  assets/
```

Frontmatter fields: `name`, `description` (both effectively required),
`license`, `compatibility`, `metadata`, `allowed-tools` (experimental),
`disable-model-invocation`.

- `name`: lowercase letters, digits, hyphens; no leading/trailing/double
  hyphen; max 64 chars. `description`: max 1024 chars; say what AND when.
- pi does not require `name` to match the folder name (other runtimes may,
  so keep them equal for portability).
- Missing description or malformed `SKILL.md` -> skill is not loaded. Most
  other invalid fields only warn.
- Name collision: first discovered wins, with a warning.
- Refer to bundled files with paths relative to the skill dir; pi tells the
  model where the skill lives.

Source: https://docs.pi.dev/latest/skills

## Skills: discovery locations

| Scope | Path |
|-------|------|
| User | `~/.pi/agent/skills/` (`<agent-dir>/skills/`; agent dir moves with `PI_CODING_AGENT_DIR`) |
| User (Agent Skills std) | `~/.agents/skills/` |
| Project | `.pi/skills/` |
| Project (Agent Skills std) | `.agents/skills/` in cwd and each ancestor, stopping at the repo root |
| Settings | `skills` array in `settings.json` (files or dirs, globs, `!x`, `+x`, `-x`) |
| Packages | `skills/` dir or `pi.skills` manifest key |
| CLI | `--skill <path>` (repeatable) |

- Directories containing `SKILL.md` are found recursively. Standalone `.md`
  skills are partly accepted; the directory form is the portable one.
- `.claude/skills/` is not among the documented locations; point the
  `skills` setting or `--skill` at it, or link into `~/.agents/skills/`.
- Project skill dirs (`.pi/skills`, `.agents/skills`) are gated by project
  trust (see security.md).
- `-ns` / `--no-skills` disables discovered/configured skills; explicit
  `--skill` paths still load.

Source: https://docs.pi.dev/latest/skills, https://docs.pi.dev/latest/configuration, https://docs.pi.dev/latest/cli

## Skills: loading and invocation

- Startup: only name, description and path go into the system prompt.
  The model reads the full `SKILL.md` on demand when the task matches
  (progressive disclosure, like Claude Code).
- Force a skill: `/skill:<name> [args]`; args are appended as a user
  request, e.g. `/skill:pdf-tools extract report.pdf`.
- `disable-model-invocation: true` hides it from auto-selection; only the
  explicit command loads it.
- `enableSkillCommands` (default `true`) controls whether `/skill:*` shows in
  command completion; typed commands work regardless.
- After editing a skill mid-session run `/reload`. Check startup diagnostics
  for warnings.

Difference from Claude Code: skill command is `/skill:name`, not `/name`.

Source: https://docs.pi.dev/latest/skills

## Prompt templates

Markdown files that become `/` commands; the filename is the command name.

```
---
description: Review staged git changes
argument-hint: "[focus]"
---
Review the staged changes. Focus on ${1:-correctness, security, and error handling}.
```

Saved as `~/.pi/agent/prompts/review.md` -> `/review`, `/review concurrency`.

- `description` shows in completion (defaults to first non-empty line).
  `argument-hint`: `<required>`, `[optional]`.
- Locations: `~/.pi/agent/prompts/`, `.pi/prompts/` (trust-gated),
  `prompts` setting, packages, `--prompt-template <path>`. Conventional dirs
  load only direct `.md` children (no recursion); settings and packages can
  select nested files.
- `-np` / `--no-prompt-templates` disables discovered/configured ones.
- Run `/reload` after changes.

Substitutions:

| Syntax | Meaning |
|--------|---------|
| `$1`, `$2` … | positional arg |
| `$@`, `$ARGUMENTS` | all args joined by spaces |
| `${1:-default}` | arg 1 or default |
| `${@:-default}` | all args or default |
| `${@:N}` / `${@:N:L}` | slice from N (length L) |

Quoting is shell-like: `/review "API compatibility"` is one arg. A quote only
opens quoting at the start of an arg (so `doesn't` is literal); unmatched
quotes stay literal; `\"` inside matching quotes is a literal quote.

Expansion happens before the text reaches the agent. Extensions see raw input
first via the `input` event, and an extension command with the same name
takes precedence.

Source: https://docs.pi.dev/latest/prompt-templates

## Packages: install and manage

A package bundles extensions, skills, prompts and themes for npm or git.

```
pi install npm:@example/pi-tools@1.0.0
pi install git:github.com/example/pi-tools@v1
pi install ./local-package
pi list
pi remove <source>
pi update --extensions
pi -e npm:@example/pi-tools      # try once, not saved
```

- Default writes to `~/.pi/agent/settings.json` (`packages` array);
  `--local` / `-l` writes `.pi/settings.json` (only honoured after trust).
- Sources: `npm:` (installed under pi's npm dir), `git:` (cloned, reset to
  ref), bare `https://` URL (treated as git), local path (used in place, not
  copied; relative paths resolve from the settings file containing them).
- Versioned npm specs and git tags/commits are pinned; updates do not move
  a configured ref.
- A package that fails to install at startup is warned about and skipped;
  pi retries on next start or `/reload`. `pi install` / `pi update` exit
  non-zero on failure.
- `-e` packages are cached in `~/.pi/agent/tmp/extensions/` and re-checked
  every start/`/reload` (unless exact-pinned or offline).
- `pi config` toggles discovered resources and built-ins; Tab switches
  scope, `pi config --local` starts in project scope.

Source: https://docs.pi.dev/latest/packages

## Packages: authoring

Conventional layout (no manifest needed): `package.json`, `extensions/`,
`skills/`, `prompts/`, `themes/`. Or declare paths under `pi`:

```
"pi": {
  "extensions": ["./src/extension.ts"],
  "skills": ["./resources/skills"],
  "prompts": ["./resources/prompts/*.md"],
  "themes": ["./resources/themes/*.json"]
}
```

- Paths relative to package root; globs and exclusions allowed. List
  dot-prefixed or symlinked roots explicitly (globs will not traverse them).
- `keywords: ["pi-package"]` makes it eligible for the gallery;
  `pi.image` / `pi.video` add previews.
- Changelog: `CHANGELOG.md` at root, or `pi.changelogPath` (`""` disables).
  Shown on next launch only for packages that load an extension, matched
  against `version` in `package.json`.
- Host-provided modules (`@earendil-works/pi-ai`, `pi-agent-core`,
  `pi-coding-agent`, `pi-tui`, `typebox`) go in `peerDependencies` with `"*"`,
  never `dependencies` (duplicate copies break module mapping; pi warns).
- Each package gets its own module root; do not rely on shared dependency
  instances across packages.

Source: https://docs.pi.dev/latest/packages

## Packages: filtering and identity

Object form in settings narrows what loads:

```
{ "source": "npm:@example/pi-tools",
  "extensions": ["extensions/*.ts", "!extensions/legacy.ts"],
  "skills": [],
  "prompts": ["prompts/review.md"] }
```

Omit a key = load all; `[]` = none; `!glob` exclude; `+path` include exact;
`-path` exclude exact. Filters can only narrow what the package declares.

- Same package in user and project settings: project entry replaces the user
  one; with `autoload: false` it instead acts as a filter delta on top.
- Identity: npm by package name, git by repo URL without ref, local by
  absolute path, so equivalent declarations never double-load.
- Security: packages run extension code in-process and may ship skills that
  tell the model to run programs. Review source before installing.

Source: https://docs.pi.dev/latest/packages
