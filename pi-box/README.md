# pi-box

Runs [pi](https://github.com/earendil-works/pi) entirely inside a Docker
container that can't reach the host's repos, secrets, services or browser.
The host `pi` stays as it is.

## Setup

```
./pi-box/link     # creates ~/pi-box, copies agent templates + config.env
./link-bin        # puts pi-box on PATH
pi-box build      # builds the image (pi 1.5.2 + the crawl/scrape toolkit)
pi-box doctor     # offline toolkit check in a throwaway container
```

## Use

```
mkdir -p ~/pi-box/work/<task-id> && cd ~/pi-box/work/<task-id>
pi-box --provider <p> -m <model>   # any pi args pass through unchanged
pi-box exec bash                   # a shell in the same box, no pi
pi-box doctor                      # needs no workspace and no network
PIBOX_DRY_RUN=1 pi-box             # print the docker command only
```

The wrapper exits 2 unless the cwd is under `~/pi-box/work/<task-id>/`, and 3
when a container for that task id is already running.

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

## Boundary

| What | How |
|---|---|
| Files | Only `work/<task-id>` (rw, same absolute path), `in/` (ro), and `~/pi-box/agent` at `/home/pi/.pi/agent`. Symlinked `pi-box`, `work`, `agent` or `in` dirs are refused. |
| Env | Allowlist only: `VENICE_API_KEY`, `TERM`, `COLORTERM`, `LANG`, plus the crawl and proxy keys from `~/pi-box/config.env` (host env overrides). Passed by name, so values never show in `ps`. |
| User | The entrypoint starts as root with only `NET_ADMIN`, `SETUID`, `SETGID`, installs the egress rules, then runs the command as the host uid with every capability dropped and `no_new_privs`. `--memory 2g`, no docker socket. |
| Network | `REJECT` for 10/8, 172.16/12, 192.168/16 (the Docker Desktop host gateway included), 169.254/16, 100.64/10, multicast and 240/4, and IPv6 ULA/link-local/multicast. DNS to the container resolver stays open. A proxy on a private address is therefore unreachable. |

## Toolkit

Pinned in the image: pi, bun, uv, and in `/opt/venv` the Python set from
`requirements.in` (crawl4ai, trafilatura, httpx, playwright, readability-lxml,
pandas, yt-dlp; `requirements.txt` is the `uv pip compile` lock). CLI: curl,
wget, jq, htmlq, rg, fd, ffmpeg, sqlite3, pandoc, git (no credentials).
Chromium is Playwright's headless shell only; there is no headed browser.
`pi-box-cookies2state` turns a Netscape `cookies.txt` into Playwright
`storageState`.

`./pi-box/link` copies `skills/*/SKILL.md` (fetch, crawl, browser, extract,
media) to `~/pi-box/agent/skills`, and creates `~/pi-box/in/cookies/`. Export a
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
caps, private ranges and host gateway refused, `example.com` loads.
