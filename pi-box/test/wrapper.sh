#!/usr/bin/env bash
# Wrapper tests with a fake docker on PATH; no container starts.
# Run with:  bash pi-box/test/wrapper.sh

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WRAPPER="$HERE/../wrapper"
TMP="$(cd "$(mktemp -d)" && pwd -P)"
FAILS=0
trap 'rm -rf "$TMP"' EXIT

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
if [ "$1" = ps ]; then
  printf '%s\n' "$@" >"$FAKE_PS"
  [ -n "$FAKE_RUNNING" ] && echo abc123
  exit 0
fi
printf '%s\n' "$@" >"$FAKE_LOG"
echo "CRAWL_DELAY_MS=${CRAWL_DELAY_MS-unset}" >>"$FAKE_LOG"
echo "VENICE_API_KEY=${VENICE_API_KEY-unset}" >>"$FAKE_LOG"
FAKE
chmod +x "$TMP/bin/docker"
export PATH="$TMP/bin:$PATH" HOME="$TMP/home" FAKE_LOG="$TMP/log" FAKE_PS="$TMP/ps"
unset VENICE_API_KEY CRAWL_DELAY_MS FAKE_RUNNING
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

# one live container per task
FAKE_RUNNING=1 run_in "$W/t1"
expect_rc 3 "refuses a second container on the same task (exit 3)"
mkdir -p "$W/a.b"
FAKE_RUNNING=1 run_in "$W/a.b"
grep -q 'name=^pi-box-a\\.b\$' "$TMP/ps"
check "ps filter escapes dots in the task id" $?

echo
if [ "$FAILS" -gt 0 ]; then
  echo "$FAILS failed"
  exit 1
fi
echo "all passed"
