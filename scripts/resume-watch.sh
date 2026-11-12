#!/usr/bin/env bash
set -u

# zen-resume-watch [--once]
# Revives chief when its process dies and resumes sessions a Claude usage limit
# stopped. A screen match alone resumes nothing: a haiku probe must fail first.

usage() {
	cat <<'USAGE'
usage: zen-resume-watch [--once]

  every minute   a session named chief that is not archived and not running
                 gets `agent-manager revive` (at most once per 5 min)
  every 5 min    running claude sessions whose screen tail shows a usage limit
                 are resumed once a `claude -p --model haiku` probe passes
                 (re-probed every 10 min)

  --once         one pass of both checks, then exit
  -h, --help     show this help

Env: ZEN_RESUME_PROBE_WAIT  seconds between probes (default 600)
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
SEEN=$(mktemp "${TMPDIR:-/tmp}/zen-resume-watch.XXXXXX")
trap 'rm -f "$SEEN"' EXIT
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

scan_limits() {
	local limited=() id tail_text key
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
		if agent-manager send "$id" "The Claude usage limit has reset. Continue exactly where you left off." >/dev/null 2>&1; then
			echo "$(stamp) resumed $id"
		else
			echo "$(stamp) could not message $id"
		fi
	done
}

if [ "${1:-}" = "--once" ]; then
	revive_chief
	scan_limits
	exit 0
fi

echo "$(date '+%F %H:%M') resume-watch started: chief revive check every minute, limit scan every 5"
tick=0
while true; do
	revive_chief
	[ $((tick % 5)) -eq 0 ] && scan_limits
	tick=$((tick + 1))
	sleep 60
done
