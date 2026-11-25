#!/usr/bin/env bash
set -u

# zen-resume-watch [--once]
# Revives chief when its process dies, resumes sessions a Claude usage limit
# stopped, runs `zen-rotate tick`, and backs up the cockpit's delivery scan. A screen match alone resumes nothing: a
# haiku probe must fail first.

usage() {
	cat <<'USAGE'
usage: zen-resume-watch [--once]

  every minute   a session named chief that is not archived and not running
                 gets `agent-manager revive` (at most once per 5 min);
                 `zen-rotate tick` runs (not while a confirmed usage limit
                 is waited out)
  every 5 min    running claude sessions whose screen tail shows a usage limit
                 are resumed once a `claude -p --model haiku` probe passes
                 (re-probed every 10 min); a failed send is retried at the
                 next 3 scans without a new probe;
                 `zen-inbox delivery scan` logs agent-manager's delivery
                 problems before its rows age out (the cockpit scans every
                 minute; this covers the cockpit being closed)

  --once         one pass of each check, then exit
  -h, --help     show this help

Env: ZEN_RESUME_PROBE_WAIT  seconds between probes (default 600)
     ZEN_RESUME_STATE       dir for the seen/pending files, kept on exit
                            (default: a temp dir removed on exit)
USAGE
}

case "${1:-}" in
-h | --help)
	usage
	exit 0
	;;
"" | --once) ;;
*)
	usage >&2
	exit 2
	;;
esac

LIMIT_RE='(usage|claude|5-hour|weekly|session) limit (reached|hit)|hit your (usage )?limit|limit will reset at|limit reached.{0,40}reset'
PROBE_WAIT="${ZEN_RESUME_PROBE_WAIT:-600}"
if [ -n "${ZEN_RESUME_STATE:-}" ]; then
	STATE="$ZEN_RESUME_STATE"
	mkdir -p "$STATE"
else
	STATE=$(mktemp -d "${TMPDIR:-/tmp}/zen-resume-watch.XXXXXX")
	trap 'rm -rf "$STATE"' EXIT
fi
SEEN="$STATE/seen"
PENDING="$STATE/pending"
touch "$SEEN" "$PENDING"
MAX_RETRIES=3
trap 'exit 0' INT TERM
last_revive=-300

stamp() { date '+%H:%M'; }

sessions_where() {
	agent-manager sessions --json 2>/dev/null | python3 -I -c '
import json, sys
mode = sys.argv[1]
data = json.load(sys.stdin)
rows = data if isinstance(data, list) else data.get("sessions", [])
for s in rows:
    if s.get("archived") or s.get("tool") != "claude":
        continue
    if mode == "running" and s.get("running"):
        print(s["id"])
    if mode == "dead-chief" and s.get("name") == "chief" and not s.get("running"):
        print(s["id"])
' "$1"
}

limit_cleared() {
	claude -p --model haiku 'Reply with the single word OK.' 2>&1 | grep -qxE 'OK\.?'
}

revive_chief() {
	local id
	id=$(sessions_where dead-chief | head -1)
	[ -n "$id" ] || return 0
	[ $((SECONDS - last_revive)) -ge 300 ] || return 0
	last_revive=$SECONDS
	if agent-manager revive "$id" >/dev/null 2>&1; then
		echo "$(stamp) chief $id was dead: revived"
	else
		echo "$(stamp) chief $id is dead and revive failed; retrying in 5 min"
	fi
}

# A failing or missing zen-rotate never stops the watcher.
rotate_tick() {
	local out rc
	out=$(zen-rotate tick 2>&1)
	rc=$?
	if [ "$rc" -ne 0 ]; then
		echo "$(stamp) zen-rotate tick failed (exit $rc): $(printf '%s' "$out" | tail -1)"
	elif [ -n "$out" ]; then
		printf '%s\n' "$out"
	fi
}

# A failing or missing zen-inbox never stops the watcher.
delivery_scan() {
	local out rc
	out=$(zen-inbox delivery scan 2>&1)
	rc=$?
	if [ "$rc" -ne 0 ]; then
		echo "$(stamp) zen-inbox delivery scan failed (exit $rc): $(printf '%s' "$out" | tail -1)"
	fi
}

send_continue() {
	agent-manager send "$1" "The Claude usage limit has reset. Continue exactly where you left off." </dev/null >/dev/null 2>&1
}

# PENDING lines are "<id> <failed retries>"; the limit was already confirmed
# cleared, so retries skip the probe.
retry_pending() {
	[ -s "$PENDING" ] || return 0
	local running id n next="$PENDING.new"
	running=$(sessions_where running)
	[ -n "$running" ] || return 0
	: >"$next"
	while read -r id n; do
		if ! printf '%s\n' "$running" | grep -qxF "$id"; then
			echo "$(stamp) dropped $id: not running"
		elif send_continue "$id"; then
			echo "$(stamp) resumed $id"
		elif [ $((n + 1)) -ge "$MAX_RETRIES" ]; then
			echo "$(stamp) giving up on $id"
		else
			echo "$id $((n + 1))" >>"$next"
		fi
	done <"$PENDING"
	mv "$next" "$PENDING"
}

scan_limits() {
	local limited=() id tail_text key
	retry_pending
	for id in $(sessions_where running); do
		tail_text=$(agent-manager read "$id" 2>/dev/null | tail -15)
		printf '%s' "$tail_text" | grep -qiE "$LIMIT_RE" || continue
		key="$id $(printf '%s' "$tail_text" | cksum | cut -d' ' -f1)"
		grep -qxF "$key" "$SEEN" && continue
		echo "$key" >>"$SEEN"
		limited+=("$id")
	done
	[ "${#limited[@]}" -gt 0 ] || return 0
	if limit_cleared; then
		echo "$(stamp) limit text in ${limited[*]}, but the probe passed: ignored"
		return 0
	fi
	echo "$(stamp) limit confirmed for ${limited[*]}; probing every ${PROBE_WAIT}s"
	until limit_cleared; do
		sleep "$PROBE_WAIT"
		revive_chief
	done
	echo "$(stamp) limit cleared"
	for id in "${limited[@]}"; do
		if send_continue "$id"; then
			echo "$(stamp) resumed $id"
		else
			echo "$(stamp) could not message $id"
			grep -q "^$id " "$PENDING" || echo "$id 0" >>"$PENDING"
		fi
	done
}

if [ "${1:-}" = "--once" ]; then
	revive_chief
	scan_limits
	rotate_tick
	delivery_scan
	exit 0
fi

echo "$(date '+%F %H:%M') resume-watch started: chief revive check every minute, limit scan and delivery scan every 5"
tick=0
while true; do
	revive_chief
	[ $((tick % 5)) -eq 0 ] && scan_limits
	rotate_tick
	[ $((tick % 5)) -eq 0 ] && delivery_scan
	tick=$((tick + 1))
	sleep 60
done
