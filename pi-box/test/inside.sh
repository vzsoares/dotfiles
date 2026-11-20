#!/usr/bin/env bash
# Runs inside the box (via boundary.sh).
# Args: real host home, host listener port, the box in/ dir, and the mode:
#   plain (default)  a run without the browser
#   browser          the run that holds the bridge port; waits for ./done
#   second           a run started while another box holds the port

HOST_HOME="$1"
PORT="$2"
IN_DIR="$3"
MODE="${4:-plain}"
FAILS=0
ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
t() { # name, cmd...
  local name="$1"
  shift
  if "$@"; then ok "$name"; else bad "$name"; fi
}
# shellcheck disable=SC2329 # called through t
rc_is() { # want, cmd...
  local want="$1" rc=0
  shift
  "$@" >/dev/null 2>&1 || rc=$?
  [ "$rc" = "$want" ]
}
# shellcheck disable=SC2329
fails() { ! "$@" >/dev/null 2>&1; }

# shellcheck disable=SC2329
browser_is() { # state, tool count
  pi mcp list --json 2>/dev/null |
    jq -e --arg s "$1" --argjson n "$2" \
      '.servers[] | select(.name == "browser") | .state == $s and (.tools | length) == $n' \
      >/dev/null
}

if [ "$MODE" != browser ]; then
  t "no pairing token without the browser" test ! -e /run/abb/pair.token
  t "cannot plant a pairing token" fails touch /run/abb/pair.token
  t "abb-mcp exits 1 without the token" rc_is 1 abb-mcp
fi
if [ "$MODE" = second ]; then
  t "pi mcp list fails on the browser server" rc_is 1 pi mcp list --json
  t "browser server failed, no tools" browser_is failed 0
  exit $((FAILS > 0))
fi

for p in "$HOST_HOME"/{.aws,.ssh,.gnupg,.claude,.zshrc.local,.config,.pi,code} \
  /home/pi/{.aws,.ssh,.gnupg,.claude,.zshrc.local} /home/pi/.pi/agent/auth.json \
  /var/run/docker.sock /run/docker.sock; do
  t "absent: $p" test ! -e "$p"
done

allowed=" COLORTERM HOME HOSTNAME LANG NODE_VERSION OLDPWD PATH PWD SHLVL TERM USER
  YARN_VERSION _ VIRTUAL_ENV PLAYWRIGHT_BROWSERS_PATH UV_LINK_MODE
  VENICE_API_KEY CRAWL_DELAY_MS
  CRAWL_CONCURRENCY CRAWL_MAX_PAGES CRAWL_RESPECT_ROBOTS CRAWL_USER_AGENT
  HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY http_proxy https_proxy all_proxy
  no_proxy "
allowed="$(echo "$allowed" | tr -s ' \n' '  ')"
extra=""
while IFS= read -r name; do
  case " $allowed " in *" $name "*) ;; *) extra="$extra $name" ;; esac
done < <(env -0 | tr '\0' '\n' | sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p')
t "env holds only allowlisted names${extra:+ (extra:$extra)}" test -z "$extra"

t "not root" test "$(id -u)" != 0
t "no effective capabilities" grep -q '^CapEff:[[:space:]]*0*$' /proc/self/status
t "no_new_privs set" grep -q '^NoNewPrivs:[[:space:]]*1$' /proc/self/status
t "cannot flush the egress rules" fails iptables -F

for ip in 10.255.255.1 172.31.255.1 192.168.255.1 169.254.169.254 100.64.0.1; do
  t "private $ip rejected" rc_is 7 curl -sS -m 3 -o /dev/null "http://$ip/"
done
if getent hosts host.docker.internal >/dev/null; then
  t "host.docker.internal:$PORT refused" \
    fails curl -sS -m 3 -o /dev/null "http://host.docker.internal:$PORT/"
fi
gw="$(awk '$2 == "00000000" { print $3 }' /proc/net/route | head -n 1)"
gw_ip="$(printf '%d.%d.%d.%d' "0x${gw:6:2}" "0x${gw:4:2}" "0x${gw:2:2}" "0x${gw:0:2}")"
t "default gateway $gw_ip:$PORT refused" \
  fails curl -sS -m 3 -o /dev/null "http://$gw_ip:$PORT/"
t "https://example.com loads" \
  bash -c "curl -fsS -m 15 https://example.com | grep -q 'Example Domain'"

t "workspace writable" touch "$PWD/.w"
rm -f "$PWD/.w"
t "agent dir writable" touch /home/pi/.pi/agent/.w
rm -f /home/pi/.pi/agent/.w
t "in/ read-only" fails touch "$IN_DIR/.w"
t "pi 1.5.2 present" test "$(pi --version)" = 1.5.2

if [ "$MODE" = browser ]; then
  t "pairing token is 0400, owned by the pi uid" \
    test "$(stat -c '%a %u' /run/abb/pair.token)" = "400 $(id -u)"
  t "token dir is read-only" fails touch /run/abb/x
  # Only replies leave from the bridge port; the bridge is not up yet, so
  # curl can bind it.
  t "new connection from source port 9242 rejected" \
    rc_is 7 curl -sS -m 3 --local-port 9242 -o /dev/null "http://10.255.255.1/"
  t "gateway refused from source port 9242" \
    rc_is 7 curl -sS -m 3 --local-port 9242 -o /dev/null "http://$gw_ip:$PORT/"
  t "pi lists browser connected with 11 tools" browser_is connected 11

  tail -f /dev/null | abb-mcp 2>"$PWD/abb.log" &
  for _ in $(seq 40); do
    curl -fs -m 2 -o /dev/null http://127.0.0.1:9242/ && break
    sleep 0.5
  done
  t "bridge listens in the box" curl -fsS -m 2 -o /dev/null http://127.0.0.1:9242/
  touch "$PWD/ready"
  for _ in $(seq 360); do
    [ -e "$PWD/done" ] && break
    sleep 0.5
  done
  t "host checks finished" test -e "$PWD/done"
fi

exit $((FAILS > 0))
