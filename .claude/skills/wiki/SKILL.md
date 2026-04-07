---
name: wiki
description: Build and maintain a project knowledge base (docs/wiki/). Use when documenting architecture, features, modules, conventions, decisions, or answering questions about the project. Automatically triggered when the user asks to document something, explain how something works for future reference, or when significant features are completed.
argument-hint: <ingest|query|lint|status> [topic or question]
allowed-tools: Read Edit Write Grep Glob Bash Agent
---

# Project Wiki — LLM-Maintained Knowledge Base

You maintain a persistent, structured wiki for the current project at `docs/wiki/`. The wiki is a collection of interlinked markdown files that compound over time. You write and update all wiki pages — the user curates, directs, and reviews.

## Directory Structure

```
docs/wiki/
├── index.md          # Master index — every page listed with one-line summary
├── log.md            # Chronological log of wiki operations
├── overview.md       # High-level project overview (updated as project evolves)
├── architecture/     # System architecture, tech stack, data flow
├── modules/          # Per-module pages
├── features/         # Feature-specific pages
├── concepts/         # Domain concepts, patterns, conventions
├── decisions/        # Architecture Decision Records (ADRs)
└── api/              # API endpoint documentation
```

## Operations

### `/wiki ingest <topic>`
Read the relevant source code and create/update wiki pages about the topic.

1. Read relevant source files (use Grep/Glob to find them)
2. Write or update the topic page in the appropriate directory
3. Update cross-references in related pages (add `See also:` links)
4. Update `index.md` with the new/updated page
5. Append an entry to `log.md`

### `/wiki query <question>`
Answer a question using the wiki, then optionally file the answer as a new page.

1. Read `index.md` to find relevant pages
2. Read those pages
3. Synthesize an answer with `[[wiki-links]]` citations
4. Ask the user if the answer should be filed as a new wiki page

### `/wiki lint`
Health-check the wiki for quality issues.

1. Read `index.md` and check all linked pages exist
2. Find orphan pages (exist but not in index)
3. Check for stale content (pages that reference code that has changed)
4. Suggest missing pages for undocumented features
5. Check for broken `[[wiki-links]]`
6. Report findings and offer to fix

### `/wiki status`
Show wiki stats: page count, last updated, recent log entries, coverage gaps.

## Page Format

Every wiki page uses this template:

```markdown
---
title: Page Title
category: architecture|module|feature|concept|decision|api
updated: YYYY-MM-DD
related: [other-page, another-page]
---

# Page Title

Brief summary paragraph.

## Content sections...

Detailed content with `code references` and [[wiki-links]] to other pages.

## See Also

- [[related-page]] — why it's related
- [[another-page]] — connection
```

## Conventions

- **Wiki links**: Use `[[page-name]]` syntax (Obsidian-compatible). The page-name matches the filename without `.md`.
- **Code references**: Use `path/to/file.ts:42` format for specific line references, or just `path/to/file.ts` for the whole file.
- **Keep pages focused**: One topic per page. Split large pages into sub-pages.
- **Cross-reference aggressively**: Every page should link to 2-5 related pages.
- **Update, don't append**: When new info supersedes old, update in place. Don't just add notes at the bottom.
- **Frontmatter**: Always include title, category, updated date, and related pages.
- **Index**: Every page must be listed in `index.md` under its category.
- **Log**: Every ingest/lint operation gets a timestamped entry in `log.md`.

## Log Format

```markdown
## [YYYY-MM-DD] operation | Topic
Brief description of what was done.
Pages created/updated: page1.md, page2.md
```

## Index Format

```markdown
# Wiki Index

## Architecture
- [[overview]] — High-level project overview
- [[tech-stack]] — Framework, runtime, database, styling

## Modules
- [[module-name]] — Brief description
...
```

## Bootstrap

If `docs/wiki/` doesn't exist yet, create it with:
- `index.md` (empty index with category headers)
- `log.md` (empty log)
- `overview.md` (read CLAUDE.md, SPEC.md, README.md and any project config to write the initial overview)

Then suggest the user run `/wiki ingest` for each major area of the codebase.

## Important

- Read the source code before writing wiki pages — don't guess or use stale knowledge
- When the user completes a significant feature, suggest running `/wiki ingest` to document it
- The wiki is for **future sessions** — write as if explaining to someone who hasn't seen the codebase
- Prefer concrete examples and file paths over abstract descriptions
- Keep pages scannable — use headers, bullet points, code blocks
- The wiki works across all projects — adapt the structure to fit each project's needs

Current request: $ARGUMENTS
