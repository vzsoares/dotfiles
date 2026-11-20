#!/usr/bin/env bash
# Wrapper tests with a fake docker on PATH; no container starts.
# Run with:  bash pi-box/test/wrapper.sh

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WRAPPER="$HERE/../wrapper"
TMP="$(cd "$(mktemp -d)" && pwd -P)"
FAILS=0
LISTENER=""
cleanup() {
  [ -n "$LISTENER" ] && kill "$LISTENER" 2>/dev/null
  rm -rf "$TMP"
}
trap cleanup EXIT

ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
check() { if [ "$2" = 0 ]; then ok "$1"; else bad "$1"; fi; }
expect_rc() { # want, name; reads the previous exit code
  local rc=$?
  if [ "$rc" = "$1" ]; then ok "$2"; else bad "$2 (got $rc)"; fi
}

mkdir -p "$TMP/bin" "$TMP/home/pi-box/work/t1/sub" "$TMP/home/pi-box/agent" \
  "$TMP/home/pi-box/in" "$TMP/outside"
cat >"$TMP/bin/docker" <<'FAKE'
#!/usr/bin/env bash
if [ "$1" = ps ] && [ "$2" = --format ]; then
  [ -n "$FAKE_HOLDER" ] && echo "$FAKE_HOLDER"
  exit 0
fi
if [ "$1" = ps ]; then
  printf '%s\n' "$@" >"$FAKE_PS"
  [ -n "$FAKE_RUNNING" ] && echo abc123
  exit 0
fi
if [ "$1" = stop ]; then
  echo "$*" >>"$FAKE_STOP"
  exit 0
fi
[ -n "$FAKE_SLEEP" ] && sleep "$FAKE_SLEEP"
[ -n "$FAKE_APPEND" ] && printf '%s\n' "$FAKE_APPEND" >>"$FAKE_SESSION"
printf '%s\n' "$@" >"$FAKE_LOG"
echo "CRAWL_DELAY_MS=${CRAWL_DELAY_MS-unset}" >>"$FAKE_LOG"
echo "VENICE_API_KEY=${VENICE_API_KEY-unset}" >>"$FAKE_LOG"
echo "PAIR_TOKEN_SET=${AGENT_BROWSER_PAIR_TOKEN:+yes}" >>"$FAKE_LOG"
echo run >>"$FAKE_RUNS"
if [ -n "$FAKE_BIND_FAIL" ] && printf '%s\n' "$@" | grep -qx -- -p; then
  exit 125
fi
exit "${FAKE_RC:-0}"
FAKE
chmod +x "$TMP/bin/docker"
export PATH="$TMP/bin:$PATH" HOME="$TMP/home" FAKE_LOG="$TMP/log" FAKE_PS="$TMP/ps" FAKE_STOP="$TMP/stop"
export FAKE_RUNS="$TMP/runs"
unset VENICE_API_KEY CRAWL_DELAY_MS FAKE_RUNNING FAKE_SLEEP FAKE_APPEND
unset FAKE_HOLDER FAKE_BIND_FAIL FAKE_RC AGENT_BROWSER_PAIR_TOKEN PIBOX_BRIDGE_SRC
# The browser section below turns it on, against a port this test owns.
export PIBOX_BROWSER=0
W="$HOME/pi-box/work"

run_in() { # dir, args... -> exit code; stdout in $TMP/out
  (cd "$1" && shift && bash "$WRAPPER" "$@") >"$TMP/out" 2>"$TMP/err"
}

# cwd guard
run_in "$TMP/outside"
expect_rc 2 "refuses a cwd outside work/ (exit 2)"
run_in "$W"
expect_rc 2 "refuses work/ itself (exit 2)"
ln -s "$TMP/outside" "$W/link"
run_in "$W/link"
expect_rc 2 "refuses a symlinked task dir (exit 2)"
mkdir -p "$W/.hidden"
run_in "$W/.hidden"
expect_rc 2 "refuses a task id starting with a dot (exit 2)"
mv "$HOME/pi-box/agent" "$HOME/pi-box/agent.real"
ln -s "$HOME/pi-box/agent.real" "$HOME/pi-box/agent"
run_in "$W/t1"
expect_rc 2 "refuses a symlinked agent dir (exit 2)"
rm "$HOME/pi-box/agent"
mv "$HOME/pi-box/agent.real" "$HOME/pi-box/agent"
mv "$HOME/pi-box/in" "$HOME/pi-box/in.real"
ln -s "$HOME/pi-box/in.real" "$HOME/pi-box/in"
run_in "$W/t1"
expect_rc 2 "refuses a symlinked in dir (exit 2)"
rm "$HOME/pi-box/in"
mv "$HOME/pi-box/in.real" "$HOME/pi-box/in"

# pass-through and mounts
PIBOX_DRY_RUN=1 run_in "$W/t1/sub" --provider venice -m glm --thinking high --session 's 1'
check "dry run exits 0" $?
grep -q -- "pi-box pi --provider venice -m glm --thinking high --session s\\\\ 1 \$" "$TMP/out"
check "pi args pass through unchanged, last" $?
grep -q -- "-v $W/t1:$W/t1 " "$TMP/out"
check "mounts only the task dir, at its own path" $?
grep -q -- "-w $W/t1/sub " "$TMP/out"
check "workdir is the cwd" $?
grep -q -- "-v $HOME/pi-box/in:$HOME/pi-box/in:ro " "$TMP/out"
check "in/ is read-only" $?
grep -q -- "--name pi-box-t1 " "$TMP/out" && grep -q -- "--cap-drop ALL " "$TMP/out"
check "named per task, caps dropped" $?
PIBOX_DRY_RUN=1 run_in "$W/t1" exec bash -c 'echo hi'
grep -q -- "pi-box bash -c echo\\\\ hi \$" "$TMP/out"
check "exec runs the command instead of pi" $?

# agent-manager launch: its host-side session hook is dropped, other -e kept
hook="/Users/x/Library/Application Support/agent-manager/hooks/agent-manager-pi.ts"
PIBOX_DRY_RUN=1 run_in "$W/t1" --session-id abc -e "$hook" --extension ./mine.ts
grep -q -- "pi-box pi --session-id abc --extension ./mine.ts \$" "$TMP/out"
check "drops agent-manager's pi hook, keeps other extensions" $?
PIBOX_DRY_RUN=1 run_in "$W/t1" "--extension=$hook" --continue
grep -q -- "pi-box pi --continue \$" "$TMP/out"
check "drops agent-manager's pi hook given as --extension=" $?
mkdir -p "$TMP/shim"
ln -s "$WRAPPER" "$TMP/shim/pi"
(cd "$W/t1" && PIBOX_DRY_RUN=1 PATH="$TMP/shim:$PATH" pi --continue) >"$TMP/out" 2>"$TMP/err"
grep -q -- "pi-box pi --continue \$" "$TMP/out"
check "the shim named pi on PATH runs the wrapper" $?

# env allowlist
printf 'CRAWL_DELAY_MS="1500"\nSECRET_TOKEN=x\n' >"$HOME/pi-box/config.env"
SECRET_TOKEN=leak AWS_PROFILE=prod VENICE_API_KEY=k1 run_in "$W/t1" --version
check "runs docker" $?
! grep -Eq 'SECRET_TOKEN|AWS_PROFILE|leak' "$TMP/log" &&
  ! grep -vx 'VENICE_API_KEY=k1' "$TMP/log" | grep -q k1
check "unlisted names and values stay out of the docker args" $?
grep -qx 'VENICE_API_KEY' "$TMP/log" && grep -qx 'VENICE_API_KEY=k1' "$TMP/log"
check "a set key passes by name only" $?
grep -qx 'CRAWL_DELAY_MS=1500' "$TMP/log"
check "config.env value passes, quotes stripped" $?
CRAWL_DELAY_MS=10 run_in "$W/t1"
grep -qx 'CRAWL_DELAY_MS=10' "$TMP/log"
check "host env overrides config.env" $?
run_in "$W/t1"
! grep -qx 'VENICE_API_KEY' "$TMP/log"
check "an unset key is not passed" $?

# doctor: offline, from any cwd, no workspace mounts
PIBOX_DRY_RUN=1 run_in "$TMP/outside" doctor
check "doctor runs outside work/" $?
grep -q -- "--network none " "$TMP/out" && grep -q -- "--cap-drop ALL " "$TMP/out" &&
  grep -q -- "--entrypoint /opt/pi-box/doctor/doctor.sh pi-box \$" "$TMP/out" &&
  ! grep -q -- " -v " "$TMP/out"
check "doctor has no network, no caps, no mounts" $?
PIBOX_DRY_RUN=1 run_in "$W/t1"
grep -q -- "--shm-size 512m " "$TMP/out"
check "pi gets a shm size for chromium" $?

# spend cap: fake ledger, fake docker, no model
L="$HOME/pi-box/ledger.jsonl"
now_ms=$(($(date +%s) * 1000))
rm -f "$L" "$FAKE_LOG"
printf '{"kind":"begin","run":"a","ts":%s,"reserve":0.1}\n' "$now_ms" >"$L"
printf '{"kind":"end","run":"a","models":{"glm":9.95}}\n' >>"$L"
run_in "$W/t1" --version
expect_rc 4 "refuses to start when spend + reserve crosses the cap (exit 4)"
[ ! -e "$FAKE_LOG" ]
check "docker is not started over the cap" $?
PIBOX_DRY_RUN=1 run_in "$W/t1"
expect_rc 4 "a dry run reports the refusal too (exit 4)"
printf '{"kind":"end","run":"a","models":{"glm":8.5}}\n' >>"$L"
run_in "$W/t1" --version
check "starts under the cap" $?
grep -q "warning" "$TMP/err"
check "warns at 80% of the cap" $?
grep -q '"task":"t1"' "$L" && grep -q '"kind":"end"' "$L"
check "a start is recorded and settled in the ledger" $?
grep -q '"pid":[0-9]' "$L"
check "a start records the wrapper pid" $?
mkdir -p "$HOME/pi-box/agent/sessions/--x--"
export FAKE_SESSION="$HOME/pi-box/agent/sessions/--x--/s.jsonl"
printf '{"type":"session","cwd":"%s"}\n' "$W/t1" >"$FAKE_SESSION"
FAKE_APPEND='{"type":"message","message":{"role":"assistant","model":"m","usage":{"input":1}}}' \
  run_in "$W/t1" --version
grep -q '"unknown_messages":1' "$L"
check "a message with unknown cost is settled at the reserve" $?
run_in "$W/t1" cost
check "pi-box cost runs" $?
grep -q 'total' "$TMP/out" && grep -q 'cap' "$TMP/out"
check "pi-box cost prints the total and the cap" $?
rm -f "$L"
rm -rf "$HOME/pi-box/agent/sessions"

# a killed wrapper stops its container and settles the run
(cd "$W/t1" && FAKE_SLEEP=30 exec bash "$WRAPPER" >/dev/null 2>&1) &
wpid=$!
sleep 1
kill -TERM "$wpid"
wait "$wpid" 2>/dev/null || true
for _ in 1 2 3 4 5 6 7 8; do [ -s "$TMP/stop" ] && break; sleep 1; done
grep -qx 'stop -t 10 pi-box-t1' "$TMP/stop"
check "killing the wrapper stops the container" $?
pkill -f "sleep 30" || true
rm -f "$L"

# one live container per task
FAKE_RUNNING=1 run_in "$W/t1"
expect_rc 3 "refuses a second container on the same task (exit 3)"
mkdir -p "$W/a.b"
FAKE_RUNNING=1 run_in "$W/a.b"
grep -q 'name=^pi-box-a\\.b\$' "$TMP/ps"
check "ps filter escapes dots in the task id" $?

# image build: the pinned bridge by default, a local checkout on request
run_in "$TMP/outside" build
! grep -q -- build-context "$FAKE_LOG" && grep -qx -- "$(cd "$HERE/.." && pwd -P)" "$FAKE_LOG"
check "build uses the pinned bridge by default" $?
PIBOX_BRIDGE_SRC=/src/abb run_in "$TMP/outside" build
grep -qx -- --build-context "$FAKE_LOG" && grep -qx 'bridge-src=/src/abb' "$FAKE_LOG"
check "PIBOX_BRIDGE_SRC builds from a local bridge checkout" $?

# browser: bridge port, pairing token, pinned mcp.json entry
BP="$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
TOKEN="$HOME/.config/agent-browser-bridge/pair.token"
M="$HOME/pi-box/agent/mcp.json"
TEMPLATE="$HERE/../agent/mcp.json"
PUBLISH="-p 127.0.0.1:$BP:9242 -e AGENT_BROWSER_PAIR_TOKEN "
export PIBOX_BROWSER_PORT="$BP"
mcp_is() { # other-entry json; the browser entry must equal the template's
  python3 - "$M" "$TEMPLATE" "$1" <<'PY'
import json, sys
got, want = (json.load(open(p))["mcpServers"] for p in sys.argv[1:3])
other = json.loads(sys.argv[3])
sys.exit(got.pop("browser", None) != want["browser"] or got != other)
PY
}
runs() { wc -l <"$FAKE_RUNS" | tr -d ' '; }

today="docker run --rm -i --name pi-box-t1 --hostname pi-box --cap-drop ALL \
--cap-add NET_ADMIN --cap-add SETUID --cap-add SETGID --security-opt no-new-privileges \
--memory 2g --shm-size 512m -v $W/t1:$W/t1 -v $HOME/pi-box/agent:/home/pi/.pi/agent \
-w $W/t1 -e PIBOX_UID -e PIBOX_GID -v $HOME/pi-box/in:$HOME/pi-box/in:ro \
-e TERM -e CRAWL_DELAY_MS pi-box pi --version "
dry() { # the dry-run command for t1; bash always sets TERM
  (
    unset COLORTERM LANG
    cd "$W/t1" && PIBOX_DRY_RUN=1 bash "$WRAPPER" --version
  ) 2>"$TMP/err"
}
[ "$(dry)" = "$today" ]
check "PIBOX_BROWSER=0: the docker command is today's, byte for byte" $?
rm -f "$M"
run_in "$W/t1" --version
! grep -Eq -- '^-p$|AGENT_BROWSER' "$FAKE_LOG" && grep -qx 'PAIR_TOKEN_SET=' "$FAKE_LOG" &&
  [ ! -e "$TOKEN" ]
check "PIBOX_BROWSER=0: no publish, no token passed or created" $?
mcp_is '{}'
check "PIBOX_BROWSER=0: a start still writes the pinned browser entry" $?

export PIBOX_BROWSER=1
rm -f "$M"
[ "$(dry)" = "${today/pi-box pi --version/${PUBLISH}pi-box pi --version}" ]
check "port free: dry run adds the loopback publish and the token name only" $?
[ ! -e "$TOKEN" ] && [ ! -e "$M" ]
check "a dry run creates no token and leaves mcp.json alone" $?
run_in "$W/t1" --version
check "port free: runs docker" $?
grep -qx -- '-p' "$FAKE_LOG" && grep -qx "127.0.0.1:$BP:9242" "$FAKE_LOG" &&
  grep -qx 'AGENT_BROWSER_PAIR_TOKEN' "$FAKE_LOG" && grep -qx 'PAIR_TOKEN_SET=yes' "$FAKE_LOG"
check "port free: publishes to the host loopback, token by name" $?
[ "$(python3 -c 'import os, sys; print(oct(os.stat(sys.argv[1]).st_mode & 0o777))' "$TOKEN")" = 0o600 ] &&
  grep -Eqx '[0-9a-f]{64}' "$TOKEN"
check "a missing pairing token is created, 0600" $?
grep -q "paste it into the extension popup" "$TMP/err"
check "a new token tells the user to pair the extension" $?
! grep -q "$(cat "$TOKEN")" "$FAKE_LOG" "$TMP/out" "$TMP/err"
check "the token value is in no argv or output" $?
before="$(cat "$TOKEN")"
run_in "$W/t1" --version
[ "$(cat "$TOKEN")" = "$before" ] && ! grep -q "paste it" "$TMP/err"
check "an existing token is reused" $?
: >"$TOKEN"
run_in "$W/t1" --version
! grep -qx -- '-p' "$FAKE_LOG" && grep -q "is empty; this run has no browser" "$TMP/err"
check "an empty token file starts without the browser" $?
printf '%s\n' "$before" >"$TOKEN"
PIBOX_BROWSER_PORT=x run_in "$W/t1" --version
expect_rc 2 "a non-numeric PIBOX_BROWSER_PORT is refused (exit 2)"

python3 -c 'import socket, sys, time
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[1])))
s.listen()
time.sleep(600)' "$BP" &
LISTENER=$!
for _ in 1 2 3 4 5 6 7 8 9 10; do
  (exec 3<>"/dev/tcp/127.0.0.1/$BP") 2>/dev/null && break
  sleep 0.3
done
FAKE_RC=7 run_in "$W/t1" --version
expect_rc 7 "port busy: the run still starts and exits as pi does"
! grep -Eq -- '^-p$|AGENT_BROWSER' "$FAKE_LOG" && grep -qx 'PAIR_TOKEN_SET=' "$FAKE_LOG"
check "port busy: no publish and no token" $?
grep -qx "pi-box: browser held by another process on 127.0.0.1:$BP; this run has no browser" "$TMP/err"
check "port busy: warns that a host process holds the browser" $?
FAKE_HOLDER="pi-box-other 127.0.0.1:$BP->9242/tcp" run_in "$W/t1" --version
grep -qx "pi-box: browser held by pi-box-other; this run has no browser" "$TMP/err"
check "port busy: names the pi-box run that holds the browser" $?
[ "$(dry)" = "$today" ] && grep -q "browser held by" "$TMP/err"
check "port busy: the dry run shows no publish either" $?
kill "$LISTENER"
wait "$LISTENER" 2>/dev/null
LISTENER=""

# two starts at once: the loser's bind fails (125) and it starts without
: >"$FAKE_RUNS"
FAKE_BIND_FAIL=1 FAKE_HOLDER="pi-box-other 127.0.0.1:$BP->9242/tcp" run_in "$W/t1" --version
check "a lost bind race still starts" $?
[ "$(runs)" = 2 ] && ! grep -qx -- '-p' "$FAKE_LOG" &&
  grep -q "browser held by pi-box-other" "$TMP/err"
check "a lost bind race retries once without the browser" $?
: >"$FAKE_RUNS"
FAKE_BIND_FAIL=1 run_in "$W/t1" --version
expect_rc 125 "a docker failure with no other holder is not retried (exit 125)"
[ "$(runs)" = 1 ]
check "no second start when no other box holds the port" $?
: >"$FAKE_RUNS"
FAKE_BIND_FAIL=1 FAKE_HOLDER="pi-box-t1 127.0.0.1:$BP->9242/tcp" run_in "$W/t1" --version
[ "$(runs)" = 1 ]
check "its own container on the port is not a lost race" $?

# pinned mcp.json entry: only `browser` is rewritten
echo '{"mcpServers":{"browser":{"command":"/bin/evil","args":["x"]},"mine":{"command":"npx"}},"autoEnableCodemode":false}' >"$M"
run_in "$W/t1" --version
mcp_is '{"mine":{"command":"npx"}}' && grep -q '"autoEnableCodemode": false' "$M"
check "an edited browser entry is restored, other entries and keys kept" $?
echo '{"mcpServers":{"mine":{"command":"npx"}}}' >"$M"
run_in "$W/t1" --version
mcp_is '{"mine":{"command":"npx"}}'
check "a deleted browser entry is restored" $?
[ -z "$(find "$HOME/pi-box/agent" -name '.mcp.json.*')" ]
check "the rewrite leaves no temp file" $?
echo '{"mcpServers":{"mine":{"command":"npx"}}}' >"$M"
PIBOX_DRY_RUN=1 run_in "$W/t1" --version
! grep -q browser "$M"
check "a dry run does not rewrite mcp.json" $?
echo 'not json' >"$M"
: >"$FAKE_RUNS"
run_in "$W/t1" --version
expect_rc 2 "refuses to start on an unparsable mcp.json (exit 2)"
[ "$(cat "$M")" = 'not json' ] && [ "$(runs)" = 0 ]
check "an unparsable mcp.json is left as is, docker not started" $?
echo '{"secret":"host"}' >"$TMP/outside/host.json"
rm -f "$M"
ln -s "$TMP/outside/host.json" "$M"
run_in "$W/t1" --version
expect_rc 2 "refuses a symlinked mcp.json (exit 2)"
[ -L "$M" ] && [ "$(cat "$TMP/outside/host.json")" = '{"secret":"host"}' ]
check "a symlinked mcp.json is neither followed nor replaced" $?
rm -f "$M"

echo
if [ "$FAILS" -gt 0 ]; then
  echo "$FAILS failed"
  exit 1
fi
echo "all passed"
