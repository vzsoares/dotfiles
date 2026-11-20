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
- `extract`: parse saved files (htmlq, jq, rg, fd, sqlite3, pandoc, pandas).
- `media`: yt-dlp and ffmpeg.

Defaults arrive as env: `CRAWL_DELAY_MS`, `CRAWL_CONCURRENCY`,
`CRAWL_MAX_PAGES`, `CRAWL_RESPECT_ROBOTS`, `CRAWL_USER_AGENT`, and proxy vars.
Logins come only from cookie files in `in/cookies/<site>.txt`; never look for
or read a browser profile.

## Output contract

- Put every deliverable in `out/` inside the task folder.
- Write `RESULT.md` in the task folder with three non-empty sections,
  `## Summary`, `## Sources` (every URL or file used) and `## Left undone`
  (`nothing` if so).
- End your final message with `WORKER_DONE <task-id>`.
