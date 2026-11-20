#!/usr/bin/env bash
# Boundary tests against the built image; no pi session, no model call.
# Run with:  zen-lock core-heavy -- bash pi-box/test/boundary.sh
# Needs the image (pi-box build) and network access for example.com.

set -e

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WRAPPER="$HERE/../wrapper"
REAL_HOME="$HOME"
TMP="$(cd "$(mktemp -d)" && pwd -P)"
SERVER=""
BOX=""
cleanup() {
  [ -n "$SERVER" ] && kill "$SERVER" 2>/dev/null
  [ -n "$BOX" ] && kill "$BOX" 2>/dev/null
  rm -rf "$TMP"
}
trap cleanup EXIT

# A host listener the box must not reach; the control container proves it is up.
if [ "$(uname)" = Darwin ]; then
  bind=127.0.0.1
else
  bind="$(ip -4 -o addr show docker0 | awk '{ sub("/.*", "", $4); print $4 }')"
fi
PORT=$((20000 + RANDOM % 20000))
mkdir "$TMP/www"
python3 -m http.server "$PORT" --bind "$bind" --directory "$TMP/www" >/dev/null 2>&1 &
SERVER=$!
sleep 1
docker run --rm --add-host host.docker.internal:host-gateway --entrypoint curl \
  pi-box -fsS -m 5 -o /dev/null "http://host.docker.internal:$PORT/"
echo "ok   control container reaches the host listener"

export DOCKER_CONFIG="${DOCKER_CONFIG:-$REAL_HOME/.docker}"
export HOME="$TMP/home"
ws="$HOME/pi-box/work/boundary"
mkdir -p "$ws" "$HOME/pi-box/agent" "$HOME/pi-box/in"
cp "$HERE/inside.sh" "$ws/inside.sh"
cd "$ws"
AWS_SECRET_ACCESS_KEY=canary GITHUB_TOKEN=canary PIBOX_BROWSER=0 \
  bash "$WRAPPER" exec bash ./inside.sh "$REAL_HOME" "$PORT" "$HOME/pi-box/in"

# Browser run, on a host port this test owns: the user's extension dials 9242
# and never sees it. The pairing token is created in the temp HOME.
PIBOX_BROWSER_PORT="$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
export PIBOX_BROWSER_PORT
url="http://127.0.0.1:$PIBOX_BROWSER_PORT"
AWS_SECRET_ACCESS_KEY=canary GITHUB_TOKEN=canary \
  bash "$WRAPPER" exec bash ./inside.sh "$REAL_HOME" "$PORT" "$HOME/pi-box/in" browser &
BOX=$!
for _ in $(seq 180); do
  [ -e "$ws/ready" ] && break
  kill -0 "$BOX" 2>/dev/null || break
  sleep 1
done
[ -e "$ws/ready" ] || { echo "FAIL the browser box never got ready"; exit 1; }

code() { curl -s -m 5 -o /dev/null -w '%{http_code}' "$@"; }
upgrade=(-H 'Connection: Upgrade' -H 'Upgrade: websocket'
  -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: AAAAAAAAAAAAAAAAAAAAAA==')
curl -fsS -m 5 "$url/" | grep -q 'Agent Browser Bridge'
echo "ok   host loopback reaches the bridge through the firewall"
[ "$(code "${upgrade[@]}" "$url/")" = 403 ]
[ "$(code "${upgrade[@]}" -H 'Origin: http://localhost' "$url/")" = 403 ]
echo "ok   upgrade without the extension Origin refused (403)"
[ "$(code "${upgrade[@]}" "$url/client")" = 404 ]
echo "ok   /client is off (404)"
[ "$(code "${upgrade[@]}" \
  -H 'Origin: chrome-extension://jjlgogdanhhkgglgckdpigepfniilinm' "$url/")" = 101 ]
echo "ok   the pinned extension Origin upgrades (101)"

ws2="$HOME/pi-box/work/boundary2"
mkdir -p "$ws2"
cp "$HERE/inside.sh" "$ws2/inside.sh"
cd "$ws2"
bash "$WRAPPER" exec bash ./inside.sh "$REAL_HOME" "$PORT" "$HOME/pi-box/in" second \
  2>"$TMP/second.err"
grep -q 'browser held by pi-box-boundary; this run has no browser' "$TMP/second.err"
echo "ok   a second concurrent box is told who holds the browser"
dry="$(PIBOX_DRY_RUN=1 bash "$WRAPPER" exec true 2>/dev/null)"
case "$dry" in *" -p "* | *AGENT_BROWSER*) echo "FAIL second box publishes"; exit 1 ;; esac
echo "ok   a second concurrent box gets no publish and no token"

touch "$ws/done"
wait "$BOX"
BOX=""
echo
echo "all passed"
