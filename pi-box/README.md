# pi-box

Runs [pi](https://github.com/earendil-works/pi) entirely inside a Docker
container that can't reach the host's repos, secrets, services or browser.
The host `pi` stays as it is.

## Setup

```
./pi-box/link     # creates ~/pi-box, copies agent templates + config.env
./link-bin        # puts pi-box on PATH
pi-box build      # builds the image (node 22 slim + pi 1.5.2)
```

## Use

```
mkdir -p ~/pi-box/work/<task-id> && cd ~/pi-box/work/<task-id>
pi-box --provider <p> -m <model>   # any pi args pass through unchanged
pi-box exec bash                   # a shell in the same box, no pi
PIBOX_DRY_RUN=1 pi-box             # print the docker command only
```

The wrapper exits 2 unless the cwd is under `~/pi-box/work/<task-id>/`, and 3
when a container for that task id is already running.

## Boundary

| What | How |
|---|---|
| Files | Only `work/<task-id>` (rw, same absolute path), `in/` (ro), and `~/pi-box/agent` at `/home/pi/.pi/agent`. Symlinked `pi-box`, `work`, `agent` or `in` dirs are refused. |
| Env | Allowlist only: `PIBOX_VENICE_API_KEY`, `PIBOX_ABLITERATION_API_KEY`, `TERM`, `COLORTERM`, `LANG`, plus the crawl and proxy keys from `~/pi-box/config.env` (host env overrides). Passed by name, so values never show in `ps`. |
| User | The entrypoint starts as root with only `NET_ADMIN`, `SETUID`, `SETGID`, installs the egress rules, then runs the command as the host uid with every capability dropped and `no_new_privs`. `--memory 2g`, no docker socket. |
| Network | `REJECT` for 10/8, 172.16/12, 192.168/16 (the Docker Desktop host gateway included), 169.254/16, 100.64/10, multicast and 240/4, and IPv6 ULA/link-local/multicast. DNS to the container resolver stays open. A proxy on a private address is therefore unreachable. |

## Tests

```
bash pi-box/test/wrapper.sh                           # fake docker, no container
zen-lock core-heavy -- bash pi-box/test/boundary.sh   # real image, no model call
```

`boundary.sh` starts a host listener, proves a plain container reaches it,
then runs `test/inside.sh` in the box: absent host paths, env allowlist, no
caps, private ranges and host gateway refused, `example.com` loads.
