# pi-box

Runs [pi](https://github.com/earendil-works/pi) entirely inside a Docker
container that can't reach the host's repos, secrets, services or browser,
except the browser through the paired extension (see Browser). The host `pi`
stays as it is.

## Setup

```
./pi-box/link     # creates ~/pi-box, copies agent templates + config.env
./link-bin        # puts pi-box on PATH
pi-box build      # builds the image (pi 1.5.2 + the crawl/scrape toolkit
                  #   + agent-browser-bridge v2.0.0 at a pinned commit)
pi-box doctor     # offline toolkit check in a throwaway container
```

`PIBOX_BRIDGE_SRC=<checkout> pi-box build` builds the bridge from a local
checkout instead of the pinned commit (development only).

## Browser

pi drives the user's everyday Chrome through
[agent-browser-bridge](https://github.com/vzsoares/agent-browser-bridge): the
bridge's MCP server runs in the box as a child of pi (`mcp.json` entry
`browser`, tools `mcp__browser__browser_*`), and the Chrome extension connects
in to `ws://localhost:9242`. Once per machine:

1. Build `chrome-extension/dist` from bridge v2.0.0 and load it unpacked in
   the everyday Chrome profile.
2. Start any pi-box run; the first one creates
   `~/.config/agent-browser-bridge/pair.token` (0600).
3. Paste that file's content into the extension popup's pairing field.

Every run (pi or `exec`) takes the browser when `127.0.0.1:9242` is free. A
run that finds it busy, by another pi-box run or a host-side bridge, prints
`pi-box: browser held by <holder>; this run has no browser` and starts
without it: its `browser` server shows as failed and no tools are declared.
`PIBOX_BROWSER=0` skips the browser (the eval and the tests use it).

pi may add its own MCP servers (`pi mcp add`) and packages (`pi install`);
they persist in `~/pi-box/agent` for every later run, browser runs included.
Only the `browser` entry is repo-owned: the wrapper rewrites that one entry on
every start and `link` merges it, both leaving the other entries alone.

## Use

```
pi-box                        # from anywhere: a new timestamped workspace
pi-box open <task> [pi args]  # open or create ~/pi-box/work/<task>
pi-box open <task> -c         # continue that task's last session
pi-box ls                     # workspaces, newest first, running marked
pi-box -m 1017                # pi args pass through; -m is fuzzy
pi-box exec bash              # a shell in the same box, no pi
pi-box help
PIBOX_DRY_RUN=1 pi-box        # print the docker command only
```

Inside `~/pi-box/work/<task>/` the wrapper uses that workspace; anywhere else
it creates `work/<YYYYMMDD-HHMMSS>` and prints its name. `VENICE_API_KEY`
comes from the env, else from the `VENICE_API_KEY=` line in `~/.zshrc.local`
(read on the host, passed by name). Exits: 2 bad task id or a symlinked
workspace, 3 that task's container is already running, 4 over the spend cap,
6 Docker down or the image missing (checked before any workspace is made).

## agent-manager

`./pi-box/link` also links `~/pi-box/shim/pi` to the wrapper and, when
agent-manager is installed, sets the `pi-box` env profile
(`PATH=~/pi-box/shim:<your PATH>`, `PI_CODING_AGENT_DIR=~/pi-box/agent`) and
creates the `pi-box` group (directory `~/pi-box/work`). A session launched
under the profile resolves `pi` to the shim, so it runs boxed:

```
agent-manager spawn --tool pi --env pi-box --group pi-box \
  --directory ~/pi-box/work/<task-id> --model <model> --prompt "…"
```

Model, effort, `--session-id`, resume (`--session`), revive (`--continue`) and
fork args pass through. The wrapper drops agent-manager's pi session hook
(`-e …/agent-manager-pi.ts`): it calls the host `agent-manager`, which the
box has no access to. The id is still known from `--session-id`. `zen-await`
reads boxed sessions from `~/pi-box/agent/sessions`.

The profile stores PATH as a literal, so re-run `./pi-box/link` after PATH
changes. A freshly spawned pane does not source `~/.zshrc.local`, so
`VENICE_API_KEY` reaches the box only when it is in the profile (or in the
pane's own shell, as on revive inside a live pane). A session outside the
profile runs host pi.

## Models

`agent/models.json` (copied to `~/pi-box/agent` by `link`) holds Venice only:
`glm-5-3-flash-uncensored` is the default (eval 29/30, $0.031) and
`deepseek-v4-1-flash-uncensored` the fallback (30/30, $0.101); Gemma 4 scored
20/30 and is out. `deepseek-v4-1-flash-1017-uncensored` (not evaluated) is
also listed, so both DeepSeek ids match the host `agents/models.json`. Switch
with `pi-box --provider venice -m <id>`. The box caps every model at a 64K
context and 4K output; the host lists their full 1M.

## Spend cap

Every start (not `build`/`doctor`) goes through `spend.py`: it refuses with exit
4 when this month's spend plus a per-call reserve would pass the cap, warns at
80%, and records the run in `~/pi-box/ledger.jsonl`. When the container exits,
the run is settled from the assistant messages appended to this task's session
files in `~/pi-box/agent/sessions` since it began (the larger of pi's
`usage.cost` and tokens x the price in the repo's `agent/models.json`, which the
box cannot edit). A session file that shrank or vanished counts at the reserve.
Killing the wrapper stops its container and settles the run. If the wrapper
itself is SIGKILLed (agent-manager closing its pane), the next `pi-box` start
or `pi-box cost` settles that run from its sessions, once its recorded pid is
gone. A message with no usable cost, a run still open and an unreadable ledger
line each count at the reserve, so the cap holds when a cost is unknown.

`~/pi-box/config.env` sets `PIBOX_CAP_USD` (10), `PIBOX_WARN_PCT` (80) and
`PIBOX_CALL_RESERVE_USD` (0.10); the host env cannot raise them. `pi-box cost
[--month [YYYY-MM]]` prints spend per model, the total and the cap.

Limits: the cap is a gate at start, not a live limit, so one long run can pass
it; `pi-box exec` is gated too (it can run pi). Sessions outside `~/pi-box/agent` (the eval's own agent dir, which has
its own $3 cap) are not in the ledger; the Venice prepaid credit is the hard
stop. Two starts at the same instant are serialised by a file lock.

## zen-flow handoff and RESULT.md

Chief files `zen-inbox ask <task> --prod run --repo dotfiles --cmd "<line>"`
with the line from:

```
mkdir -p ~/pi-box/work/<task-id>/out
python3 pi-box/contract.py command <task-id> goal.md
```

It prints the `agent-manager spawn --tool pi --env pi-box --group pi-box
--directory ~/pi-box/work/<task-id> --model venice/glm-5-3-flash-uncensored
--prompt "…"` line; the prompt is the goal plus the output contract. The session
writes deliverables to `out/` and `RESULT.md` with non-empty `## Summary`,
`## Sources` and `## Left undone` sections, and ends with `WORKER_DONE
<task-id>`. After `zen-await` returns, the orchestrator runs
`python3 pi-box/contract.py check <task-id>` (exit 1 lists the gaps), reads
`RESULT.md`, adds `pi-box cost` to its note on the task and archives the session.

## Boundary

| What | How |
|---|---|
| Files | Only `work/<task-id>` (rw, same absolute path), `in/` (ro), and `~/pi-box/agent` at `/home/pi/.pi/agent`. Symlinked `pi-box`, `work`, `agent` or `in` dirs are refused. |
| Env | Allowlist only: `VENICE_API_KEY`, `TERM`, `COLORTERM`, `LANG`, plus the crawl and proxy keys from `~/pi-box/config.env` (host env overrides). Passed by name, so values never show in `ps`. |
| User | The entrypoint starts as root with only `NET_ADMIN`, `SETUID`, `SETGID`, installs the egress rules, then runs the command as the host uid with every capability dropped and `no_new_privs`. `--memory 2g`, no docker socket. |
| Browser | The run that holds it publishes the bridge port to the host's `127.0.0.1:9242` only; the box never dials the host. One extra rule lets replies out: `--sport 9242` in conntrack state `ESTABLISHED`. The bridge refuses any Origin but the pinned extension ID, needs the pairing token as the first frame, and has `/client` off, so pi's stdio pipe is the only driver. The token enters by name and is left at `/run/abb/pair.token` (0400, pi uid), not in pi's env; all it can do is register as the extension on a bridge. |
| Self-installs | What pi installs lives in `~/pi-box/agent` and loads in later runs that hold the user's logins (accepted risk: per-run approval, the install note in `RESULT.md`, the extension's toggle and action log). The wrapper and `link` refuse a symlinked `mcp.json`. |
| Network | `REJECT` for 10/8, 172.16/12, 192.168/16 (the Docker Desktop host gateway included), 169.254/16, 100.64/10, multicast and 240/4, and IPv6 ULA/link-local/multicast. DNS to the container resolver stays open. A proxy on a private address is therefore unreachable. |

## Toolkit

Pinned in the image: pi, bun, uv, and in `/opt/venv` the Python set from
`requirements.in` (crawl4ai, trafilatura, httpx, playwright, readability-lxml,
pandas, yt-dlp; `requirements.txt` is the `uv pip compile` lock). CLI: curl,
wget, jq, htmlq, rg, fd, ffmpeg, sqlite3, pandoc, git (no credentials).
Chromium is Playwright's headless shell only; there is no headed browser.
`pi-box-cookies2state` turns a Netscape `cookies.txt` into Playwright
`storageState`.

`./pi-box/link` copies `skills/*/SKILL.md` (fetch, crawl, browser, chrome,
extract, media) and the shared `.claude/skills/pi` skill to `~/pi-box/agent/skills`, and creates `~/pi-box/in/cookies/`. Export a
site's cookies there as `<site>.txt` to let the box log in.

Update the Python lock with `uv pip compile requirements.in
--python-version 3.11 -o requirements.txt` (bump with `--upgrade`).

## Model eval

`eval/` scores the three Venice candidates on ten tool-call tasks; see
`eval/RUBRIC.md`. The dry run is the default and calls nothing:

```
uv run pi-box/eval/eval.py                                # plan + estimate
zen-lock core-heavy -- uv run pi-box/eval/eval.py --live  # paid, $3 cap
```

`--live` runs each call as `pi-box exec env PI_CODING_AGENT_DIR=… pi --mode
json` from `~/pi-box/work/eval-898`, with the eval's own `models.json`, and
writes `results.md` / `results.json` to `~/pi-box/evals/<stamp>/`.

## Tests

```
bash pi-box/test/wrapper.sh                           # fake docker, no container
uv run pi-box/eval/test_eval.py                       # fake pi-box, no Venice
bash pi-box/test/link.sh                              # temp HOME, fake agent-manager
zen-lock core-heavy -- bash pi-box/test/boundary.sh   # real image, no model call
zen-lock core-heavy -- pi-box doctor                  # toolkit, offline
```

`boundary.sh` starts a host listener, proves a plain container reaches it,
then runs `test/inside.sh` in the box: absent host paths, env allowlist, no
caps, private ranges and host gateway refused, `example.com` loads. It then
starts a box that holds the browser on a random host port (never 9242, so the
real extension is not involved): `pi mcp list` shows `browser` connected with
11 tools, the host reaches the bridge, an upgrade without the extension Origin
and `/client` are refused, egress from source port 9242 is still rejected, and
a second box started meanwhile has no publish, no token and a failed `browser`.
`wrapper.sh` covers the publish, the token file, the busy port, the lost bind
race and the pinned `mcp.json` entry with a fake docker.
