# shellcheck shell=bash
# shellcheck disable=SC2154 # input, common and zen_git come from the hook
# Sourced by git/hooks/pre-push: scans the commits being pushed with gitleaks
# (git/gitleaks.toml) and, on the owner's GitHub remotes, ggshield. Uses the
# hook's $input, $common, $zen_git, zero and refuse.

# ggshield retries an unreachable API for minutes; normal runs take 1-3 s.
ggshield_timeout=${ZEN_GGSHIELD_TIMEOUT:-15}

owner_remote_url() {
	case $1 in
	*github.com[:/]vzsoares/*) return 0 ;;
	esac
	return 1
}

scan_guidance() {
	cat >&2 <<'EOF'
pre-push: secret scan refused this push.
  1. If the secret could be real, stop. Rotate it, then remove it from the
     unpushed commits (git rebase -i / git commit --amend). Never ignore a
     real secret.
  2. A clear false alarm (test fixture, placeholder, doc example, hash):
     - gitleaks: add its Fingerprint to .gitleaksignore under a "# reason" line
     - ggshield: ggshield secret ignore --last-found (writes .gitguardian.yaml)
     - a file holding only synthetic fixtures may use a path allowlist instead:
       [[allowlists]] paths in .gitleaks.toml, secret.ignored_paths in
       .gitguardian.yaml
     Commit the entry with the reason in the commit body and push again.
     Agents: clear false alarms only; anything unclear is a zen-inbox ask.
  A fingerprint holds the commit sha: rebasing that commit means redoing it.
EOF
}

# One git-log range per pushed tip, none when nothing is new. Commits reachable
# from any pushed ref's remote tip or a remote-tracking ref are already on a remote.
scan_ranges() {
	local lsha rsha base tip range neg="" tips=""
	while read -r _ lsha _ rsha; do
		[ -n "$lsha" ] || continue
		zero "$lsha" && continue
		[ "$lsha" = "$rsha" ] && continue
		lsha=$(git rev-parse -q --verify "$lsha^{commit}") || continue
		case " $tips " in *" $lsha "*) ;; *) tips="$tips $lsha" ;; esac
		if ! zero "$rsha" && base=$(git rev-parse -q --verify "$rsha^{commit}"); then
			neg="$neg $base"
		fi
	done <<EOF
$input
EOF
	for tip in $tips; do
		range="$tip --not$neg --remotes"
		# shellcheck disable=SC2086 # the range is several rev-list arguments
		[ -z "$(git rev-list -n 1 $range)" ] || echo "$range"
	done
	return 0
}

# Each tip supplies its own .gitleaks.toml and .gitleaksignore, so work-tree
# edits cannot hide a finding (gitleaks scans $common: it reads ignores from
# its source dir). A repo .gitleaks.toml without [extend] only adds to the
# global config.
tip_file() {
	git cat-file -e "$tip:$1" 2>/dev/null && git show "$tip:$1" >"$2"
}

scan_config() {
	local own=$scan_tmp/own.toml
	if ! tip_file .gitleaks.toml "$own"; then
		echo "$zen_git/gitleaks.toml"
	elif grep -q '^[[:space:]]*\[extend\]' "$own"; then
		echo "$own"
	else
		{
			cat "$own"
			printf "\n[extend]\npath = '%s'\n" "$zen_git/gitleaks.toml"
		} >"$scan_tmp/gitleaks.toml"
		echo "$scan_tmp/gitleaks.toml"
	fi
}

scan_gitleaks() {
	local cfg rc=0
	command -v gitleaks >/dev/null 2>&1 ||
		refuse "gitleaks not found; install it (macOS: brew install gitleaks, Manjaro: sudo pacman -S gitleaks)"
	rm -rf "$scan_tmp/ignore" "$scan_tmp/own.toml" "$scan_tmp/gitleaks.toml"
	cfg=$(scan_config)
	mkdir "$scan_tmp/ignore"
	tip_file .gitleaksignore "$scan_tmp/ignore/.gitleaksignore" || true
	gitleaks git --log-opts "$range" --config "$cfg" --redact --verbose --no-banner \
		--log-level warn --exit-code 10 -i "$scan_tmp/ignore" "$common" >&2 || rc=$?
	case $rc in
	0) ;;
	10) return 1 ;;
	*) refuse "gitleaks exited $rc; check it runs (gitleaks version) or reinstall it (macOS: brew install gitleaks, Manjaro: sudo pacman -S gitleaks)" ;;
	esac
}

exclude_ggshield_cache() {
	local ex=$common/info/exclude
	grep -qxF .cache_ggshield "$ex" 2>/dev/null && return 0
	mkdir -p "$common/info"
	if [ -s "$ex" ] && [ -n "$(tail -c 1 "$ex")" ]; then
		echo >>"$ex"
	fi
	echo .cache_ggshield >>"$ex"
}

# $1 remote name, $2 url. Fails only on findings; any other trouble is a skip.
scan_ggshield() {
	local pid watchdog rc=0 cause
	owner_remote_url "$2" || return 0
	if ! command -v ggshield >/dev/null 2>&1; then
		zen_say "ggshield skipped: not installed"
		return 0
	fi
	exclude_ggshield_cache
	printf '%s' "$input" >"$scan_tmp/stdin"
	(cd "$top" && exec ggshield secret scan pre-push "$1" "$2") <"$scan_tmp/stdin" >"$scan_tmp/ggshield" 2>&1 &
	pid=$!
	# The trap stops the sleep too, so no orphan outlives the hook.
	(
		sleep "$ggshield_timeout" &
		s=$!
		trap 'kill "$s"; exit' TERM
		wait "$s" && kill "$pid"
	) </dev/null >/dev/null 2>&1 &
	watchdog=$!
	wait "$pid" 2>/dev/null || rc=$?
	kill "$watchdog" 2>/dev/null || true
	wait "$watchdog" 2>/dev/null || true
	case $rc in
	0) ;;
	1)
		cat "$scan_tmp/ggshield" >&2
		return 1
		;;
	143) zen_say "ggshield skipped: no answer within ${ggshield_timeout}s" ;;
	*)
		cause=$(grep -m 1 '^Error: ' "$scan_tmp/ggshield" || grep -v '^[[:space:]]*$' "$scan_tmp/ggshield" | tail -n 1 || true)
		zen_say "ggshield skipped: exit $rc: ${cause#Error: }"
		;;
	esac
}

# $1 remote name, $2 url, as git passes them to pre-push.
secret_scan() {
	local ranges found=""
	ranges=$(scan_ranges)
	[ -n "$ranges" ] || return 0
	scan_tmp=$(mktemp -d "${TMPDIR:-/tmp}/zen-prepush.XXXXXX")
	trap 'rm -rf "$scan_tmp"' EXIT
	top=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
	while read -r range; do
		tip=${range%% *}
		scan_gitleaks || found=1
	done <<EOF
$ranges
EOF
	if [ -n "$found" ] || ! scan_ggshield "$1" "$2"; then
		scan_guidance
		exit 1
	fi
}
