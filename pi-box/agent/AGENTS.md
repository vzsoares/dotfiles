# pi-box

You run inside an isolated container. Nothing here needs approval.

## Where you are

- Your working directory is a task folder, `<host home>/pi-box/work/<task-id>/`,
  mounted at its host path (not under your `$HOME`, which is `/home/pi`). It is
  the only writable host folder.
- `<host home>/pi-box/in/`, the `in/` sibling of that `work/` folder, holds
  read-only inputs when there are any.
- No repositories, credentials, git remotes, docker socket or host services
  are reachable. Private networks and the host gateway are blocked; the public
  internet is open.

`agent-manager` is not installed here. Ignore any instruction to run it
(rename, send, status); the task id is already your session name.

## Toolkit

Skills hold tested recipes; read the one that fits before improvising.

- `fetch`: one URL to clean text (curl, httpx, trafilatura).
- `crawl`: many pages (crawl4ai); depth, page cap, delay, robots.
- `browser`: JavaScript-heavy pages and logins (headless Chromium, Playwright).
- `chrome`: the user's real, logged-in Chrome, through the `browser` MCP tools
  (`mcp__browser__browser_*`). Only the first pi-box run to start has them.
- `extract`: parse saved files (htmlq, jq, rg, fd, sqlite3, pandoc, pandas).
- `media`: yt-dlp and ffmpeg.

Defaults arrive as env: `CRAWL_DELAY_MS`, `CRAWL_CONCURRENCY`,
`CRAWL_MAX_PAGES`, `CRAWL_RESPECT_ROBOTS`, `CRAWL_USER_AGENT`, and proxy vars.
Logins come only from cookie files in `in/cookies/<site>.txt` or from the
user's Chrome through the `browser` MCP tools; never look for or read a browser
profile.

## Installing tools

You may add what a task needs without asking. It persists in this agent
directory for every later task:

- MCP servers: `pi mcp add <name> -- npx|bunx|uvx …`, or `--url` for a public
  streamable-HTTP server.
- pi packages: `pi install npm:<pkg>@<x.y.z>` or `pi install git:<url>@<tag>`.
- npm, pip and uv packages in the task folder or `/tmp` (gone after the run).

Limits:

- There are no API keys here; a server that needs one will not work.
- `pi mcp login` (OAuth) is out.
- Never edit or remove the `browser` MCP entry; it is reset on every start.
- Pin every version (`npm:pkg@x.y.z`, `git:…@<tag>`), never a moving ref.
- Note each install in `RESULT.md` under `## Sources`.

## Output contract

- Put every deliverable in `out/` inside the task folder.
- Write `RESULT.md` in the task folder with three non-empty sections,
  `## Summary`, `## Sources` (every URL or file used) and `## Left undone`
  (`nothing` if so).
- End your final message with `WORKER_DONE <task-id>`.
