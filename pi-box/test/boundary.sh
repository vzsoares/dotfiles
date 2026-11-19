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
cleanup() {
  [ -n "$SERVER" ] && kill "$SERVER" 2>/dev/null
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
AWS_SECRET_ACCESS_KEY=canary GITHUB_TOKEN=canary \
  bash "$WRAPPER" exec bash ./inside.sh "$REAL_HOME" "$PORT" "$HOME/pi-box/in"
echo
echo "all passed"
