---
name: second-brain
description: Personal second brain — an LLM-maintained knowledge base across personal life, projects, and work. Use when the user wants to ingest sources, query accumulated knowledge, document personal topics, or connect a project to their second brain. Triggered by mentions of "second brain", "brain", or requests to remember/file personal or cross-project knowledge.
argument-hint: <setup|ingest|query|lint|connect|status> [topic, question, or source path]
allowed-tools: Read Edit Write Grep Glob Bash Agent
---

# Second Brain — LLM-Maintained Personal Knowledge Base

You maintain a persistent, structured personal wiki — the user's "second brain." Unlike per-project wikis, this is a **global knowledge base** that spans personal life, work, and projects. It lives in an Obsidian vault at a configured path.

The user curates sources and asks questions. You do all the writing, cross-referencing, and maintenance.

## Three Layers

1. **Raw sources** (`raw/`) — immutable source documents. Articles, notes, clippings, transcripts, journal entries. You read from these but never modify them.
2. **Wiki** (`wiki/`) — LLM-generated markdown pages. Summaries, entity pages, concept pages, syntheses. You own this layer entirely.
3. **Schema** (`SCHEMA.md` at vault root) — conventions and structure. Co-evolved by user and LLM over time.

## Vault Structure

The user works across multiple companies/orgs simultaneously. Projects, work topics, and people are scoped by company to prevent clustering. Personal life stuff stays in `personal/` (goals, health, habits) — separate from any company.

```
<vault>/
├── SCHEMA.md             # Wiki conventions (co-evolved with user)
├── index.md              # Master index — every wiki page listed
├── log.md                # Chronological operations log
├── companies.json        # Registry of companies/orgs the user works with
├── raw/                  # Source documents (immutable)
│   └── assets/           # Downloaded images, attachments
├── wiki/                 # LLM-maintained pages
│   ├── personal/         # Goals, health, habits, self-improvement (NOT projects)
│   ├── projects/         # Company-scoped project pages
│   │   ├── <company>/    # e.g. approva/, zenha-lab/
│   │   └── cross-cutting/# Patterns and knowledge spanning companies
│   ├── work/             # Company-scoped work topics
│   │   ├── <company>/    # Processes, decisions, domain knowledge per company
│   │   └── cross-cutting/# Work patterns that apply everywhere
│   ├── people/           # Company-scoped people and teams
│   │   ├── <company>/    # Colleagues, stakeholders per company
│   │   └── general/      # People not tied to a specific company
│   ├── research/         # Deep dives, reading notes, syntheses
│   ├── concepts/         # Domain concepts, mental models, patterns
│   └── meta/             # About the brain itself, workflow notes
```

### `companies.json`

Registry of active companies/orgs. Created during setup, updated with `/second-brain setup --add-company`.

```json
{
  "companies": [
    { "slug": "approva", "name": "Approva Fácil", "active": true },
    { "slug": "zenha-lab", "name": "ZenhaLab", "active": true, "note": "Personal company — OSS and closed-source projects" },
    { "slug": "personal", "name": "Personal", "active": true, "note": "Non-company personal stuff — side experiments, learning, etc." }
  ]
}
```

## Configuration

The vault path is stored in `~/.claude/second-brain.json`:

```json
{
  "vault": "/absolute/path/to/vault"
}
```

Read this file at the start of every operation to locate the vault. If it doesn't exist, prompt the user to run `/second-brain setup`.

## Operations

### `/second-brain setup [vault-path]`

Initialize or configure the second brain.

1. Ask the user for the vault path if not provided (suggest `~/second-brain` as default)
2. Ask for companies/orgs to set up (or use existing `companies.json` if re-running)
3. Create the vault directory structure if it doesn't exist:
   - `raw/`, `raw/assets/`, `wiki/` and all subdirectories
   - Company-scoped subdirs under `wiki/projects/`, `wiki/work/`, `wiki/people/` for each company
   - `wiki/projects/cross-cutting/`, `wiki/work/cross-cutting/`, `wiki/people/general/`
   - `companies.json` with the company registry
   - `SCHEMA.md` with initial conventions
   - `index.md` with empty category headers (grouped by company)
   - `log.md` (empty)
4. Write `~/.claude/second-brain.json` with the vault path
5. If `raw/` already has files, suggest running `/second-brain ingest` on them
6. Tell the user they can run `/second-brain connect` in any project to wire it up

To add a company later: `/second-brain setup --add-company <slug> <name>`
This creates the subdirectories and updates `companies.json` and `index.md`.

### `/second-brain connect`

Add a reference to the second brain in the current project's `CLAUDE.md`.

1. Read `~/.claude/second-brain.json` for the vault path
2. Check if the current project's `CLAUDE.md` already references the second brain
3. If not, append a section to `CLAUDE.md`:

```markdown

# Second Brain

This project is connected to the personal second brain at `<vault-path>`.
Use the `/second-brain` skill to file project knowledge, query cross-project context, or ingest sources.
When completing significant features or making architectural decisions, suggest filing them in the second brain if they have cross-project value.
```

4. Log the connection in `log.md`

### `/second-brain ingest <source>`

Process a source into the wiki. The source can be:
- A file path (relative or absolute) — read and process
- A directory — process all files in it
- A topic name — if the source is already in `raw/`, find and process it

**Flow:**
1. Read `~/.claude/second-brain.json` for vault path
2. Read `companies.json` to know available companies
3. Locate and read the source material
4. Determine which company this belongs to (infer from context, or ask if ambiguous)
5. Discuss key takeaways with the user — what matters, what to emphasize
6. Write a summary page in `wiki/` under the appropriate category and company subdir
7. Update existing wiki pages that relate to the new source:
   - Add cross-references
   - Note where new information confirms, extends, or contradicts existing pages
   - Update synthesis pages if they exist
6. Update `index.md`
7. Append to `log.md`

A single source might touch 5-15 wiki pages. Quality over speed — take time to find connections.

### `/second-brain query <question>`

Search the wiki and synthesize an answer.

1. Read `~/.claude/second-brain.json` for vault path
2. Read `index.md` to find relevant pages
3. Use Grep to search across wiki pages for relevant content
4. Read the most relevant pages
5. Synthesize an answer with `[[wiki-links]]` citations
6. Ask the user if the answer should be filed as a new wiki page (good answers compound)

### `/second-brain lint`

Health-check the wiki.

1. Read `~/.claude/second-brain.json` for vault path
2. Check `index.md` — all linked pages exist, no orphans
3. Check for broken `[[wiki-links]]` across all pages
4. Find stale pages (not updated in a long time relative to their topic)
5. Identify missing cross-references — pages that should link to each other but don't
6. Suggest new pages for concepts mentioned but not yet documented
7. Look for contradictions between pages
8. Report findings and offer to fix

### `/second-brain status`

Show brain stats.

1. Read `~/.claude/second-brain.json` for vault path
2. Read `companies.json` for company list
3. Count pages by category, broken down by company
4. Count raw sources
5. Show last 5 log entries
6. Show total page count and last updated date
7. Identify coverage gaps (categories/companies with few or no pages)

## Page Format

Every wiki page uses this template:

```markdown
---
title: Page Title
category: personal|project|work|research|concept|person|meta
company: approva|zenha-lab|personal|null  # null for non-company pages (concepts, research, meta)
tags: [relevant, tags]
sources: [source-file-if-applicable]
updated: YYYY-MM-DD
related: [other-page, another-page]
---

# Page Title

Brief summary paragraph.

## Content sections...

Detailed content with [[wiki-links]] to other pages.

## Sources

- Where this information came from

## See Also

- [[related-page]] — why it's related
```

## Conventions

- **Wiki links**: Use `[[page-name]]` syntax (Obsidian-compatible). Page name matches filename without `.md`.
- **Tags**: Use YAML frontmatter tags for categorization. Keep a consistent tag vocabulary.
- **Cross-reference aggressively**: Every page should link to 2+ related pages. The connections are as valuable as the content.
- **Update, don't append**: When new info supersedes old, update in place.
- **One topic per page**: Split large pages. Prefer many focused pages over few large ones.
- **Frontmatter always**: Every page must have title, category, updated, related.
- **Index always**: Every page must appear in `index.md` under its category.
- **Log always**: Every ingest/lint/connect operation gets a timestamped entry.
- **Sources are immutable**: Never modify files in `raw/`. The wiki layer is where synthesis happens.
- **File answers**: When a query produces a valuable synthesis, file it as a wiki page. Knowledge should compound, not disappear into chat history.

## Log Format

```markdown
## [YYYY-MM-DD] operation | Topic
Brief description of what was done.
Pages created/updated: page1.md, page2.md
```

## Index Format

```markdown
# Second Brain Index

## Personal
- [[goals-2026]] — Current year goals and progress

## Projects — Approva
- [[project-name]] — Brief description

## Projects — ZenhaLab
- [[project-name]] — Brief description

## Projects — Personal
- [[project-name]] — Brief description

## Projects — Cross-cutting
- [[pattern]] — Brief description

## Work — Approva
- [[topic]] — Brief description

## Work — ZenhaLab
- [[topic]] — Brief description

## Work — Cross-cutting
- [[topic]] — Brief description

## Research
- [[topic]] — Brief description

## Concepts
- [[concept]] — Brief description

## People — Approva
- [[person]] — Brief description

## People — ZenhaLab
- [[person]] — Brief description

## People — General
- [[person]] — Brief description

## Meta
- [[workflow]] — How the brain is used and maintained
```

## SCHEMA.md Template

The initial `SCHEMA.md` created during setup:

```markdown
# Second Brain Schema

## Purpose
Personal knowledge base spanning life, work, and projects.
Maintained by LLM, curated by human.

## Categories
- **personal**: Goals, health, habits, journal synthesis, self-improvement (NOT projects)
- **projects**: Technical projects, scoped by company (approva/, zenha-lab/, personal/)
- **work**: Work processes, decisions, team knowledge, domain expertise, scoped by company
- **research**: Deep dives into topics, reading notes, paper summaries (cross-company)
- **concepts**: Mental models, patterns, frameworks, ideas (cross-company)
- **people**: People, teams, organizations, scoped by company
- **meta**: About the brain itself, workflow improvements

## Companies
Projects, work, and people are organized by company/org:
- **approva**: Approva Fácil — employer
- **zenha-lab**: ZenhaLab — personal company (OSS and closed-source)
- **personal**: Non-company personal projects (side experiments, learning)
- **cross-cutting** / **general**: Patterns and knowledge that span companies

New companies can be added via `companies.json`.

## Conventions
- One topic per page
- Cross-reference liberally using [[wiki-links]]
- Update existing pages rather than creating duplicates
- Tag consistently using frontmatter (include `company` field)
- File valuable query answers as new pages
- When a page applies to multiple companies, put it in cross-cutting and link from both

## Evolving
This schema evolves over time. When you notice a pattern that should be
codified or a convention that needs changing, update this file.
```

## Important

- This is a **personal** knowledge base — write with the user's perspective and priorities in mind
- Read sources thoroughly before writing — don't guess or hallucinate content
- The brain is for **long-term knowledge** — write as if explaining to yourself 6 months from now
- Prefer concrete details over abstract descriptions
- When ingesting, actively look for connections to existing knowledge — that's the whole point
- Suggest ingesting when the user completes something significant or mentions interesting sources
- The vault is Obsidian-compatible — all markdown, wikilinks, and frontmatter work in Obsidian's graph view
- Never modify files in `raw/` — they are the source of truth

Current request: $ARGUMENTS
