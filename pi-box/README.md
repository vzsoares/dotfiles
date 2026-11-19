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

## Tests

```
bash pi-box/test/wrapper.sh                           # fake docker, no container
zen-lock core-heavy -- bash pi-box/test/boundary.sh   # real image, no model call
zen-lock core-heavy -- pi-box doctor                  # toolkit, offline
```

`boundary.sh` starts a host listener, proves a plain container reaches it,
then runs `test/inside.sh` in the box: absent host paths, env allowlist, no
caps, private ranges and host gateway refused, `example.com` loads.
