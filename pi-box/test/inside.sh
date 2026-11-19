#!/usr/bin/env bash
# Runs inside the box (via boundary.sh).
# Args: real host home, host listener port, the box in/ dir.

HOST_HOME="$1"
PORT="$2"
IN_DIR="$3"
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

for p in "$HOST_HOME"/{.aws,.ssh,.gnupg,.claude,.zshrc.local,.config,.pi,code} \
  /home/pi/{.aws,.ssh,.gnupg,.claude,.zshrc.local} /home/pi/.pi/agent/auth.json \
  /var/run/docker.sock /run/docker.sock; do
  t "absent: $p" test ! -e "$p"
done

allowed=" COLORTERM HOME HOSTNAME LANG NODE_VERSION OLDPWD PATH PWD SHLVL TERM USER
  YARN_VERSION _ PIBOX_VENICE_API_KEY PIBOX_ABLITERATION_API_KEY CRAWL_DELAY_MS
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

exit $((FAILS > 0))
