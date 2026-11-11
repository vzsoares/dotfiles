#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "pytest>=8.0",
# ]
# ///
"""Tests for git/hooks (the global pre-push gate, its secret scan, and the hook
dispatchers).

Run with:  uv run scripts/test_prepush.py
"""

from __future__ import annotations

import os
import re
import shutil
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path

import pytest

GIT_DIR = Path(__file__).resolve().parent.parent / "git"
HOOKS = GIT_DIR / "hooks"
GLOBAL_CFG = GIT_DIR / "gitleaks.toml"
ZERO = "0" * 40
REAL_GITLEAKS = shutil.which("gitleaks")

FAKE_INBOX = """#!/bin/bash
echo "$*" >> "$FAKE_LOG"
fail_branch=${FAKE_FAIL_BRANCH:-}
if [ -n "$fail_branch" ] && [[ " $* " == *" --branch $fail_branch "* ]]; then
	echo "no approved unused ask for push x $fail_branch@000000000000" >&2
	exit 1
fi
case $2 in
check) [ "${FAKE_CHECK:-0}" = 0 ] || echo "no approved unused ask (check)" >&2; exit "${FAKE_CHECK:-0}" ;;
use) echo "used 703.1 by test"; exit "${FAKE_USE:-0}" ;;
used) [ -n "${FAKE_USED_SHA:-}" ] && echo "$FAKE_USED_SHA"; exit "${FAKE_USED:-1}" ;;
esac
exit 9
"""

FAKE_GUM = """#!/bin/bash
echo "$*" >> "$FAKE_GUM_LOG"
exit "${FAKE_GUM:-1}"
"""

# `ps -o ppid=,args= -p <pid>`. FAKE_PS_TREE holds "pid ppid args" lines; the
# hook's own pid is not in it and maps to 100, so a tree starts at pid 100.
FAKE_PS = """#!/bin/bash
[ "${FAKE_PS:-}" = fail ] && exit 1
pid=${*: -1}
if [ -n "${FAKE_PS_TREE:-}" ]; then
	line=$(awk -v p="$pid" '$1 == p { $1 = ""; print; exit }' "$FAKE_PS_TREE")
	echo "${line:- 100 /bin/bash}"
	exit 0
fi
echo "    1 /bin/bash"
"""

FAKE_GITLEAKS = """#!/bin/bash
echo "$*" >> "$FAKE_GITLEAKS_LOG"
while [ $# -gt 0 ]; do
	[ "$1" = --config ] && cat "$2" > "$FAKE_GITLEAKS_LOG.cfg"
	shift
done
case ${FAKE_GITLEAKS:-0} in
leak)
	printf 'Finding: token = REDACTED\\nRuleID: generic-api-key\\nFile: a.txt\\nLine: 1\\n'
	printf 'Commit: abc\\nFingerprint: abc:a.txt:generic-api-key:1\\n'
	echo "WRN leaks found: 1" >&2
	exit 10
	;;
*) exit "${FAKE_GITLEAKS:-0}" ;;
esac
"""

FAKE_GGSHIELD = """#!/bin/bash
echo "$*" >> "$FAKE_GGSHIELD_LOG"
cat > "$FAKE_GGSHIELD_LOG.stdin"
pwd > "$FAKE_GGSHIELD_LOG.pwd"
case ${FAKE_GGSHIELD:-0} in
hang) exec sleep 30 ;;
1) echo "ggshield: 1 incident found"; exit 1 ;;
3) echo "Error: A GitGuardian API key is needed to use ggshield." >&2; exit 3 ;;
esac
exit "${FAKE_GGSHIELD:-0}"
"""


@dataclass
class Env:
    tmp: Path
    repo: Path
    remote: Path
    bin: Path
    log: Path
    gum_log: Path
    gitleaks_log: Path
    ggshield_log: Path
    vars: dict[str, str] = field(default_factory=dict)

    def run(
        self, *args: str, cwd: Path | None = None, check: bool = False
    ) -> subprocess.CompletedProcess[str]:
        env = {
            "PATH": f"{self.bin}:/usr/bin:/bin",
            "HOME": str(self.tmp),
            "GIT_CONFIG_GLOBAL": str(self.tmp / "gitconfig"),
            "GIT_CONFIG_NOSYSTEM": "1",
            "FAKE_LOG": str(self.log),
            "FAKE_GUM_LOG": str(self.gum_log),
            "FAKE_GITLEAKS_LOG": str(self.gitleaks_log),
            "FAKE_GGSHIELD_LOG": str(self.ggshield_log),
            "ZEN_PREPUSH_TTY": str(self.tmp / "no-tty"),
            "AGENT_MANAGER_SESSION_ID": "w1",
            **self.vars,
        }
        env = {k: v for k, v in env.items() if v != ""}
        return subprocess.run(
            ["git", *args],
            cwd=cwd or self.repo,
            env=env,
            capture_output=True,
            text=True,
            check=check,
        )

    def git(self, *args: str, cwd: Path | None = None) -> str:
        return self.run(*args, cwd=cwd, check=True).stdout.strip()

    def commit(self, msg: str, cwd: Path | None = None) -> str:
        where = cwd or self.repo
        (where / f"{msg}.txt").write_text(msg)
        self.git("add", ".", cwd=where)
        self.git("commit", "-q", "-m", msg, cwd=where)
        return self.git("rev-parse", "HEAD", cwd=where)

    def calls(self, log: Path | None = None) -> list[str]:
        log = log or self.log
        return log.read_text().splitlines() if log.exists() else []

    def remote_sha(self, branch: str) -> str:
        r = self.run(
            "rev-parse", "--verify", "-q", f"refs/heads/{branch}", cwd=self.remote
        )
        return r.stdout.strip()

    def fake(self, name: str, body: str) -> None:
        path = self.bin / name
        path.write_text(body)
        path.chmod(0o755)

    def no_fake(self, name: str) -> None:
        (self.bin / name).unlink()


@pytest.fixture
def env(tmp_path: Path) -> Env:
    e = Env(
        tmp=tmp_path,
        repo=tmp_path / "myrepo",
        remote=tmp_path / "remote.git",
        bin=tmp_path / "bin",
        log=tmp_path / "inbox.log",
        gum_log=tmp_path / "gum.log",
        gitleaks_log=tmp_path / "gitleaks.log",
        ggshield_log=tmp_path / "ggshield.log",
    )
    (tmp_path / "gitconfig").write_text(
        f"[core]\n\thooksPath = {HOOKS}\n"
        "[user]\n\tname = tester\n\temail = t@t.dev\n"
        "[init]\n\tdefaultBranch = master\n"
    )
    e.bin.mkdir()
    e.fake("zen-inbox", FAKE_INBOX)
    e.fake("gum", FAKE_GUM)
    e.fake("ps", FAKE_PS)
    e.fake("gitleaks", FAKE_GITLEAKS)
    e.fake("ggshield", FAKE_GGSHIELD)
    e.repo.mkdir()
    e.git("init", "-q")
    e.git("init", "-q", "--bare", str(e.remote))
    e.git("remote", "add", "origin", str(e.remote))
    e.commit("init")
    return e


def seed(env: Env, *branches: str) -> None:
    for b in branches:
        env.git("push", "-q", "--no-verify", "origin", f"HEAD:refs/heads/{b}")


def assert_refused(r: subprocess.CompletedProcess[str], *needles: str) -> None:
    assert r.returncode != 0, r.stderr
    for n in needles:
        assert n in r.stderr, r.stderr


# --------------------------------------------------------------------------- #
# Gate                                                                         #
# --------------------------------------------------------------------------- #


def test_non_gated_refs_skip_inbox(env: Env) -> None:
    head = env.git("rev-parse", "HEAD")
    env.git("tag", "v1")
    r = env.run("push", "origin", "HEAD:refs/heads/dev", "v1")
    assert r.returncode == 0, r.stderr
    assert env.remote_sha("dev") == head
    assert env.calls() == []


@pytest.mark.parametrize("branch", ["master", "main", "prod"])
def test_gated_push_approved(env: Env, branch: str) -> None:
    head = env.git("rev-parse", "HEAD")
    r = env.run("push", "origin", f"HEAD:refs/heads/{branch}")
    assert r.returncode == 0, r.stderr
    assert env.remote_sha(branch) == head
    fields = f"--match --action push --repo myrepo --branch {branch} --sha {head}"
    assert env.calls() == [f"approval check {fields}", f"approval use {fields}"]


def test_gated_push_rejected_prints_ask(env: Env) -> None:
    seed(env, "master")
    before = env.remote_sha("master")
    head = env.commit("next")
    env.vars["FAKE_CHECK"] = "1"
    r = env.run("push", "origin", "master")
    assert_refused(
        r,
        "refused update of master: no approved unused ask (check)",
        f"zen-inbox ask <task> --prod push --repo myrepo --branch master --sha {head}",
    )
    assert env.remote_sha("master") == before
    assert not any(c.startswith("approval use") for c in env.calls())


@pytest.mark.parametrize("marker", ["CLAUDECODE", "AGENT_MANAGER_SESSION_ID"])
def test_agent_marker_enforces_even_with_tty(env: Env, marker: str) -> None:
    env.vars.update(
        {
            "ZEN_PREPUSH_TTY": "/dev/null",
            "AGENT_MANAGER_SESSION_ID": "",
            "FAKE_CHECK": "1",
        }
    )
    env.vars[marker] = "1"
    assert_refused(env.run("push", "origin", "master"), "refused create of master")
    assert env.gum_log.exists() is False


def test_worktree_uses_main_checkout_name(env: Env) -> None:
    wt = env.tmp / "wt-feature"
    env.git("worktree", "add", "-q", "-b", "feat", str(wt))
    head = env.commit("feat", cwd=wt)
    r = env.run("push", "origin", "HEAD:refs/heads/main", cwd=wt)
    assert r.returncode == 0, r.stderr
    assert f"--repo myrepo --branch main --sha {head}" in env.calls()[-1]


@pytest.mark.parametrize("pair", [("dev", "master"), ("main", "master")])
def test_multi_ref_checks_all_before_use(env: Env, pair: tuple[str, str]) -> None:
    env.vars["FAKE_FAIL_BRANCH"] = "master"
    refs = [f"HEAD:refs/heads/{b}" for b in pair]
    assert_refused(env.run("push", "origin", *refs), "refused create of master")
    calls = env.calls()
    assert not any(c.startswith("approval use") for c in calls)
    assert all("--branch dev" not in c for c in calls)
    assert env.remote_sha("dev") == "" and env.remote_sha("master") == ""


def test_multi_ref_all_approved_uses_each(env: Env) -> None:
    r = env.run("push", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/master")
    assert r.returncode == 0, r.stderr
    verbs = [c.split()[1] for c in env.calls()]
    assert verbs == ["check", "check", "use", "use"]


def test_delete_needs_approved_ask(env: Env) -> None:
    seed(env, "prod")
    before = env.remote_sha("prod")
    env.vars["FAKE_CHECK"] = "1"
    r = env.run("push", "origin", ":prod")
    assert_refused(
        r,
        "refused delete of prod",
        f'--branch prod --sha {ZERO} --context "delete prod at {before[:12]}"',
    )
    assert env.remote_sha("prod") == before
    assert env.calls() == [
        f"approval check --match --action push --repo myrepo --branch prod --sha {ZERO}"
    ]


def test_delete_with_approved_ask_passes(env: Env) -> None:
    seed(env, "prod")
    r = env.run("push", "origin", ":prod")
    assert r.returncode == 0, r.stderr
    assert env.remote_sha("prod") == ""
    assert [c.split()[1] for c in env.calls()] == ["check", "use"]


def orphan(env: Env) -> str:
    env.git("checkout", "-q", "--orphan", "other")
    return env.commit("orphan")


def test_force_non_ancestor_needs_approved_ask(env: Env) -> None:
    seed(env, "master")
    before = env.remote_sha("master")
    head = orphan(env)
    env.vars["FAKE_CHECK"] = "1"
    r = env.run("push", "--force", "origin", "HEAD:refs/heads/master")
    assert_refused(
        r,
        "refused force of master",
        f'--sha {head} --context "force push over {before[:12]}"',
    )
    assert env.remote_sha("master") == before


def test_force_with_approved_ask_passes(env: Env) -> None:
    seed(env, "master")
    head = orphan(env)
    r = env.run("push", "--force", "origin", "HEAD:refs/heads/master")
    assert r.returncode == 0, r.stderr
    assert env.remote_sha("master") == head


def test_force_unknown_remote_sha_needs_approved_ask(env: Env) -> None:
    seed(env, "master")
    other = env.tmp / "other"
    env.git("clone", "-q", str(env.remote), str(other), cwd=env.tmp)
    env.commit("theirs", cwd=other)
    env.git("push", "-q", "--no-verify", "origin", "master", cwd=other)
    theirs = env.remote_sha("master")
    env.commit("mine")
    env.vars["FAKE_CHECK"] = "1"
    r = env.run("push", "--force", "origin", "master")
    assert_refused(r, "refused force of master", "force push over")
    assert env.remote_sha("master") == theirs


def test_fast_forward_is_update_not_force(env: Env) -> None:
    seed(env, "master")
    env.commit("next")
    env.vars["FAKE_CHECK"] = "1"
    r = env.run("push", "origin", "master")
    assert_refused(r, "refused update of master")
    assert "--context" not in r.stderr


def test_create_is_classified(env: Env) -> None:
    env.vars["FAKE_CHECK"] = "1"
    assert_refused(env.run("push", "origin", "master"), "refused create of master")


# --------------------------------------------------------------------------- #
# Agent detection without env markers                                          #
# --------------------------------------------------------------------------- #


def tree(env: Env, *lines: str) -> None:
    path = env.tmp / "ps-tree"
    path.write_text("".join(f"{ln}\n" for ln in lines))
    env.vars["FAKE_PS_TREE"] = str(path)


@pytest.fixture
def unmarked(env: Env) -> Env:
    """An agent that unset its env markers, on a usable tty."""
    env.vars.update(
        {
            "AGENT_MANAGER_SESSION_ID": "",
            "ZEN_PREPUSH_TTY": "/dev/null",
            "FAKE_GUM": "0",
        }
    )
    return env


@pytest.mark.parametrize(
    "ancestor",
    [
        "/Users/x/.local/share/claude/versions/2.1.0 --model opus",
        "claude --resume",
        "/usr/bin/node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js",
        "codex exec",
        "/usr/local/bin/node /usr/lib/node_modules/@openai/codex/bin/codex.js",
        "/opt/homebrew/bin/tmux -L agentmgr new-session",
        "tmux -Lagentmgr attach",
    ],
)
def test_agent_ancestor_enforces_with_env_unset(unmarked: Env, ancestor: str) -> None:
    tree(unmarked, "100 200 /bin/zsh -c git push", f"200 1 {ancestor}")
    unmarked.vars["FAKE_CHECK"] = "1"
    r = unmarked.run("push", "origin", "master")
    assert_refused(r, "agent push: inside", "refused create of master")
    assert unmarked.gum_log.exists() is False


def test_agent_ancestor_with_approved_ask_passes(unmarked: Env) -> None:
    tree(unmarked, "100 1 claude")
    head = unmarked.git("rev-parse", "HEAD")
    r = unmarked.run("push", "origin", "master")
    assert r.returncode == 0, r.stderr
    assert unmarked.remote_sha("master") == head
    assert [c.split()[1] for c in unmarked.calls()] == ["check", "use"]


@pytest.mark.parametrize(
    "ancestor",
    [
        "/bin/zsh -l",
        "/opt/homebrew/bin/tmux -L default new-session",
        "/usr/bin/node /home/x/claudette/cli.js",
        "/usr/bin/node /home/x/code/claude/tools/x.js",
        "/usr/bin/vim notes/claude/plan.md",
    ],
)
def test_human_ancestors_confirm_with_gum(unmarked: Env, ancestor: str) -> None:
    tree(unmarked, f"100 1 {ancestor}")
    r = unmarked.run("push", "origin", "master")
    assert r.returncode == 0, r.stderr
    assert unmarked.calls() == []
    assert unmarked.gum_log.exists()


def test_unreadable_process_tree_fails_closed(unmarked: Env) -> None:
    unmarked.vars.update({"FAKE_PS": "fail", "FAKE_CHECK": "1"})
    r = unmarked.run("push", "origin", "master")
    assert_refused(r, "process tree unreadable", "refused create of master")
    assert unmarked.gum_log.exists() is False


def test_agent_manager_pid_marker_enforces(unmarked: Env) -> None:
    unmarked.vars.update({"AGENT_MANAGER_AGENT_PID": "42", "FAKE_CHECK": "1"})
    r = unmarked.run("push", "origin", "master")
    assert_refused(r, "AGENT_MANAGER_AGENT_PID is set")


# --------------------------------------------------------------------------- #
# Interactive path                                                             #
# --------------------------------------------------------------------------- #


@pytest.fixture
def human(env: Env) -> Env:
    env.vars.update({"AGENT_MANAGER_SESSION_ID": "", "ZEN_PREPUSH_TTY": "/dev/null"})
    return env


def test_interactive_confirm_pushes(human: Env) -> None:
    human.vars["FAKE_GUM"] = "0"
    head = human.git("rev-parse", "HEAD")
    r = human.run("push", "origin", "master")
    assert r.returncode == 0, r.stderr
    assert human.remote_sha("master") == head
    assert human.calls() == []
    gum = human.gum_log.read_text()
    assert gum.startswith("confirm --default=false Push myrepo master@" + head[:12])


def test_interactive_decline_refuses(human: Env) -> None:
    human.vars["FAKE_GUM"] = "1"
    assert_refused(human.run("push", "origin", "master"), "push to master cancelled")
    assert human.remote_sha("master") == ""


def test_interactive_force_needs_approved_ask(human: Env) -> None:
    seed(human, "master")
    before = human.remote_sha("master")
    orphan(human)
    human.vars.update({"FAKE_GUM": "0", "FAKE_CHECK": "1"})
    r = human.run("push", "--force", "origin", "HEAD:refs/heads/master")
    assert_refused(r, "refused force of master")
    assert human.remote_sha("master") == before
    assert human.gum_log.exists() is False


def test_interactive_delete_with_approved_ask_passes(human: Env) -> None:
    seed(human, "prod")
    r = human.run("push", "origin", ":prod")
    assert r.returncode == 0, r.stderr
    assert human.remote_sha("prod") == ""
    assert [c.split()[1] for c in human.calls()] == ["check", "use"]
    assert human.gum_log.exists() is False


def test_interactive_without_gum_refuses(human: Env) -> None:
    human.no_fake("gum")
    assert_refused(human.run("push", "origin", "master"), "gum not found")
    assert human.remote_sha("master") == ""


# --------------------------------------------------------------------------- #
# Fail closed                                                                  #
# --------------------------------------------------------------------------- #


def test_inbox_missing_refuses(env: Env) -> None:
    env.no_fake("zen-inbox")
    r = env.run("push", "origin", "master")
    assert_refused(r, "zen-inbox not found", "git push --no-verify")
    assert env.remote_sha("master") == ""


@pytest.mark.parametrize("rc", ["3", "4"])
def test_inbox_error_refuses(env: Env, rc: str) -> None:
    env.vars["FAKE_CHECK"] = rc
    r = env.run("push", "origin", "master")
    assert_refused(r, f"zen-inbox exited {rc}", "git push --no-verify")
    assert env.remote_sha("master") == ""


def test_use_failure_refuses(env: Env) -> None:
    env.vars["FAKE_USE"] = "1"
    assert_refused(env.run("push", "origin", "master"), "refused create of master")
    assert env.remote_sha("master") == ""


# --------------------------------------------------------------------------- #
# zen-release token                                                            #
# --------------------------------------------------------------------------- #


def release_token(env: Env, line: str) -> Path:
    token = env.repo / ".git" / "zen-release-token"
    token.write_text(line + "\n")
    return token


def ledger(env: Env) -> Path:
    return env.repo / ".git" / "zen-release-ledger"


def test_env_flag_without_approval_is_refused(env: Env) -> None:
    env.vars.update({"ZEN_PROD_RELEASE": "680.1", "FAKE_USED": "0", "FAKE_CHECK": "1"})
    env.vars["FAKE_USED_SHA"] = env.git("rev-parse", "HEAD")
    assert_refused(env.run("push", "origin", "master"), "refused create of master")
    assert [c.split()[1] for c in env.calls()] == ["check"]
    assert env.remote_sha("master") == ""


def test_release_token_passes_and_is_consumed(env: Env) -> None:
    approved = env.git("rev-parse", "HEAD")
    head = env.commit("bump")
    token = release_token(env, f"680.1 master {head}")
    env.vars.update({"FAKE_USED": "0", "FAKE_USED_SHA": approved})
    r = env.run("push", "origin", "master")
    assert r.returncode == 0, r.stderr
    assert env.remote_sha("master") == head
    assert env.calls() == [
        "approval used 680.1 --action release --repo myrepo --branch master"
    ]
    assert not token.exists()
    assert ledger(env).read_text() == f"680.1 {head}\n"


def test_release_sha_after_launcher_stderr(env: Env) -> None:
    approved = env.git("rev-parse", "HEAD")
    head = env.commit("bump")
    release_token(env, f"680.1 master {head}")
    env.vars.update(
        {"FAKE_USED": "0", "FAKE_USED_SHA": f"go: downloading x\n{approved}"}
    )
    r = env.run("push", "origin", "master")
    assert r.returncode == 0, r.stderr
    assert [c.split()[1] for c in env.calls()] == ["used"]


def test_release_token_not_bound_when_repo_hook_fails(env: Env) -> None:
    approved = env.git("rev-parse", "HEAD")
    head = env.commit("bump")
    release_token(env, f"680.1 master {head}")
    repo_hook(env, "pre-push", "exit 1\n")
    env.vars.update({"FAKE_USED": "0", "FAKE_USED_SHA": approved})
    assert env.run("push", "origin", "master").returncode != 0
    assert not ledger(env).exists()


def test_release_token_replayed_for_new_sha_falls_back(env: Env) -> None:
    approved = env.git("rev-parse", "HEAD")
    ledger(env).write_text(f"680.1 {approved}\n")
    head = env.commit("later")
    release_token(env, f"680.1 master {head}")
    env.vars.update({"FAKE_USED": "0", "FAKE_USED_SHA": approved, "FAKE_CHECK": "1"})
    r = env.run("push", "origin", "master")
    assert_refused(r, "release token 680.1 already pushed", "refused create of master")
    assert [c.split()[1] for c in env.calls()] == ["check"]


def test_release_token_repush_of_same_sha_passes(env: Env) -> None:
    head = env.git("rev-parse", "HEAD")
    ledger(env).write_text(f"680.1 {head}\n")
    release_token(env, f"680.1 master {head}")
    env.vars.update({"FAKE_USED": "0", "FAKE_USED_SHA": head})
    r = env.run("push", "origin", "master")
    assert r.returncode == 0, r.stderr
    assert ledger(env).read_text() == f"680.1 {head}\n"


@pytest.mark.parametrize("field", ["branch", "sha"])
def test_release_token_for_other_ref_falls_back(env: Env, field: str) -> None:
    head = env.git("rev-parse", "HEAD")
    line = f"680.1 main {head}" if field == "branch" else f"680.1 master {ZERO}"
    token = release_token(env, line)
    env.vars.update({"FAKE_USED": "0", "FAKE_USED_SHA": head, "FAKE_CHECK": "1"})
    assert_refused(env.run("push", "origin", "master"), "refused create of master")
    assert [c.split()[1] for c in env.calls()] == ["check"]
    assert not token.exists()


def test_release_token_never_covers_force(env: Env) -> None:
    seed(env, "master")
    head = orphan(env)
    release_token(env, f"680.1 master {head}")
    env.vars.update({"FAKE_USED": "0", "FAKE_USED_SHA": head, "FAKE_CHECK": "1"})
    r = env.run("push", "--force", "origin", "HEAD:refs/heads/master")
    assert_refused(r, "refused force of master")
    assert [c.split()[1] for c in env.calls()] == ["check"]


def test_release_non_ancestor_falls_back(env: Env) -> None:
    env.git("checkout", "-q", "-b", "side")
    side = env.commit("side")
    env.git("checkout", "-q", "master")
    head = env.commit("main-line")
    release_token(env, f"680.1 master {head}")
    env.vars.update({"FAKE_USED": "0", "FAKE_USED_SHA": side})
    r = env.run("push", "origin", "master")
    assert r.returncode == 0, r.stderr
    assert [c.split()[1] for c in env.calls()] == ["used", "check", "use"]
    assert not ledger(env).exists()


def test_release_unused_falls_back_and_refuses(env: Env) -> None:
    head = env.git("rev-parse", "HEAD")
    release_token(env, f"680.1 master {head}")
    env.vars.update({"FAKE_USED": "1", "FAKE_CHECK": "1"})
    assert_refused(env.run("push", "origin", "master"), "refused create of master")
    assert [c.split()[1] for c in env.calls()] == ["used", "check"]


# --------------------------------------------------------------------------- #
# Chaining and dispatch                                                        #
# --------------------------------------------------------------------------- #


def repo_hook(env: Env, name: str, body: str) -> None:
    path = env.repo / ".git" / "hooks" / name
    path.write_text("#!/bin/bash\n" + body)
    path.chmod(0o755)


def test_repo_pre_push_failure_blocks(env: Env) -> None:
    repo_hook(env, "pre-push", "exit 1\n")
    assert env.run("push", "origin", "master").returncode != 0
    assert env.remote_sha("master") == ""
    assert [c.split()[1] for c in env.calls()] == ["check"]


def test_repo_pre_push_gets_same_stdin_and_args(env: Env) -> None:
    marker = env.tmp / "marker"
    repo_hook(env, "pre-push", f'cat > "{marker}"\necho "$@" >> "{marker}.args"\n')
    head = env.git("rev-parse", "HEAD")
    r = env.run("push", "origin", "HEAD:refs/heads/dev", "HEAD:refs/heads/feat")
    assert r.returncode == 0, r.stderr
    lines = sorted(marker.read_text().splitlines())
    assert marker.read_text().endswith("\n")
    assert lines == [f"HEAD {head} refs/heads/{b} {ZERO}" for b in ("dev", "feat")]
    assert (env.tmp / "marker.args").read_text() == f"origin {env.remote}\n"


def test_dispatcher_runs_repo_hooks(env: Env) -> None:
    marker = env.tmp / "dispatched"
    for name in ("pre-commit", "commit-msg", "post-commit", "post-merge"):
        repo_hook(env, name, f'echo "{name} $#" >> "{marker}"\n')
    env.commit("one")
    env.git("checkout", "-q", "-b", "topic")
    env.commit("two")
    env.git("checkout", "-q", "master")
    env.commit("three")
    env.git("merge", "-q", "--no-ff", "-m", "merge topic", "topic")
    seen = marker.read_text().splitlines()
    assert "pre-commit 0" in seen
    assert "commit-msg 1" in seen
    assert "post-commit 0" in seen
    assert "post-merge 1" in seen


def test_dispatcher_in_linked_worktree(env: Env) -> None:
    marker = env.tmp / "wt-marker"
    repo_hook(env, "pre-commit", f'echo "$PWD" >> "{marker}"\n')
    wt = env.tmp / "wt"
    env.git("worktree", "add", "-q", "-b", "wt", str(wt))
    env.commit("in-wt", cwd=wt)
    assert marker.read_text().splitlines()[-1].endswith("/wt")


def test_dispatcher_with_relative_gitdir_file(env: Env) -> None:
    marker = env.tmp / "sub-marker"
    outer = env.tmp / "outer"
    (outer / "modules").mkdir(parents=True)
    sub = outer / "sub"
    env.git(
        "init", "-q", "--separate-git-dir", str(outer / "modules" / "sub"), str(sub)
    )
    (sub / ".git").write_text("gitdir: ../modules/sub\n")
    hook = outer / "modules" / "sub" / "hooks" / "pre-commit"
    hook.write_text(f'#!/bin/bash\necho ran >> "{marker}"\n')
    hook.chmod(0o755)
    env.commit("sub", cwd=sub)
    assert marker.read_text() == "ran\n"


def test_dispatcher_runs_bare_remote_hook(env: Env) -> None:
    marker = env.tmp / "recv-marker"
    hook = env.remote / "hooks" / "post-receive"
    hook.write_text(f'#!/bin/bash\ncat >> "{marker}"\n')
    hook.chmod(0o755)
    head = env.git("rev-parse", "HEAD")
    r = env.run("push", "origin", "HEAD:refs/heads/dev")
    assert r.returncode == 0, r.stderr
    assert marker.read_text() == f"{ZERO} {head} refs/heads/dev\n"


def test_dispatcher_keeps_exit_code(env: Env) -> None:
    repo_hook(env, "pre-commit", "echo blocked >&2\nexit 3\n")
    (env.repo / "x.txt").write_text("x")
    env.git("add", ".")
    r = env.run("commit", "-q", "-m", "x")
    assert r.returncode != 0
    assert "blocked" in r.stderr


def test_dispatcher_without_repo_hook_passes(env: Env) -> None:
    env.commit("plain")
    env.git("rebase", "-q", "HEAD~1")


def test_push_to_checkout_keeps_update_instead(env: Env) -> None:
    target = env.tmp / "target"
    env.git("clone", "-q", str(env.repo), str(target), cwd=env.tmp)
    env.git("config", "receive.denyCurrentBranch", "updateInstead", cwd=target)
    env.git("checkout", "-q", "-b", "dev", cwd=target)
    env.commit("deployed")
    env.git("remote", "add", "target", str(target))
    r = env.run("push", "target", "master:dev")
    assert r.returncode == 0, r.stderr
    assert (target / "deployed.txt").read_text() == "deployed"


def test_hooks_use_bin_bash(env: Env) -> None:
    for hook in HOOKS.iterdir():
        assert hook.read_text().splitlines()[0] == "#!/bin/bash", hook
        assert os.access(hook, os.X_OK), hook
    for script in (HOOKS / "pre-push", GIT_DIR / "secret-scan.sh"):
        r = subprocess.run(["/bin/bash", "-n", str(script)], check=False)
        assert r.returncode == 0, script


# --------------------------------------------------------------------------- #
# Secret scan                                                                  #
# --------------------------------------------------------------------------- #


def owner_remote(env: Env) -> str:
    """Moves origin to a path the owner pattern matches; git rewrites the $2
    a hook sees for insteadOf URLs, so a real github URL can't be faked."""
    url = env.tmp / "github.com" / "vzsoares" / "x.git"
    url.parent.mkdir(parents=True)
    env.remote.rename(url)
    env.remote = url
    env.git("remote", "set-url", "origin", str(url))
    return str(url)


def owner_match(url: str) -> bool:
    script = f'. "{GIT_DIR}/secret-scan.sh"; owner_remote_url "$1"'
    r = subprocess.run(["/bin/bash", "-c", script, "_", url], check=False)
    return r.returncode == 0


def scans(env: Env) -> list[str]:
    return env.calls(env.gitleaks_log)


def assert_log_opts(call: str, log_opts: str) -> None:
    assert f"--log-opts {log_opts} --config " in call, call


def test_scan_new_branch_and_update(env: Env) -> None:
    head = env.git("rev-parse", "HEAD")
    r = env.run("push", "origin", "HEAD:refs/heads/feat")
    assert r.returncode == 0, r.stderr
    (call,) = scans(env)
    assert_log_opts(call, f"{head} --not --remotes")
    assert f"--config {GLOBAL_CFG} " in call
    assert re.search(
        rf"--exit-code 10 -i \S+/ignore {re.escape(str(env.repo))}/\.git$", call
    )
    nxt = env.commit("next")
    r = env.run("push", "origin", "HEAD:refs/heads/feat")
    assert r.returncode == 0, r.stderr
    assert_log_opts(scans(env)[-1], f"{nxt} --not {head} --remotes")
    assert env.calls(env.ggshield_log) == []


def test_scan_skips_commits_already_on_a_remote(env: Env) -> None:
    seed(env, "feat")
    old = env.git("rev-parse", "HEAD")
    env.git("checkout", "-q", "-b", "side")
    upstream = env.commit("upstream")
    seed(env, "side")
    env.git("fetch", "-q", "origin")
    env.git("checkout", "-q", "-B", "feat", old)
    env.git("merge", "-q", "--no-ff", "-m", "merge side", upstream)
    merge = env.git("rev-parse", "HEAD")
    assert env.git("rev-list", merge, "--not", old, "--remotes") == merge
    assert env.run("push", "origin", "HEAD:refs/heads/feat").returncode == 0
    (call,) = scans(env)
    assert_log_opts(call, f"{merge} --not {old} --remotes")


def test_scan_excludes_every_pushed_ref_remote_tip(env: Env) -> None:
    base = env.git("rev-parse", "HEAD")
    seed(env, "a")
    env.git("update-ref", "-d", "refs/remotes/origin/a")
    new = env.commit("new")
    tip = env.commit("tip")
    r = env.run("push", "origin", f"{new}:refs/heads/b", "HEAD:refs/heads/a")
    assert r.returncode == 0, r.stderr
    calls = " | ".join(scans(env))
    for sha in (new, tip):
        assert f"--log-opts {sha} --not {base} --remotes --config " in calls


def test_scan_owner_remote_runs_ggshield(env: Env) -> None:
    url = owner_remote(env)
    head = env.git("rev-parse", "HEAD")
    r = env.run("push", "origin", "HEAD:refs/heads/feat")
    assert r.returncode == 0, r.stderr
    assert env.remote_sha("feat") == head
    assert env.calls(env.ggshield_log) == [f"secret scan pre-push origin {url}"]
    assert "Terminated" not in r.stderr, r.stderr
    stdin = (env.tmp / "ggshield.log.stdin").read_text()
    assert stdin == f"HEAD {head} refs/heads/feat {ZERO}\n"
    pwd = (env.tmp / "ggshield.log.pwd").read_text().strip()
    assert Path(pwd).resolve() == env.repo.resolve()


@pytest.mark.parametrize(
    ("url", "owned"),
    [
        ("git@github.com:vzsoares/x.git", True),
        ("https://github.com/vzsoares/x.git", True),
        ("ssh://git@github.com/vzsoares/x", True),
        ("https://token@github.com/vzsoares/x", True),
        ("git@github.com:someone/x.git", False),
        ("git@gitlab.com:vzsoares/x.git", False),
        ("https://github.com/vzsoares-fork/x", False),
    ],
)
def test_owner_remote_match(url: str, owned: bool) -> None:
    assert owner_match(url) is owned


def test_scan_other_remote_skips_ggshield(env: Env) -> None:
    assert env.run("push", "origin", "HEAD:refs/heads/feat").returncode == 0
    assert len(scans(env)) == 1
    assert env.calls(env.ggshield_log) == []


def test_gitleaks_finding_refuses(env: Env) -> None:
    owner_remote(env)
    env.vars["FAKE_GITLEAKS"] = "leak"
    r = env.run("push", "origin", "HEAD:refs/heads/feat")
    assert_refused(
        r,
        "Fingerprint: abc:a.txt:generic-api-key:1",
        "secret scan refused this push",
        "Rotate it",
        ".gitleaksignore",
        "ggshield secret ignore --last-found",
    )
    assert env.remote_sha("feat") == ""
    assert env.calls(env.ggshield_log) == []


def test_ggshield_finding_refuses(env: Env) -> None:
    owner_remote(env)
    env.vars["FAKE_GGSHIELD"] = "1"
    r = env.run("push", "origin", "HEAD:refs/heads/feat")
    assert_refused(r, "1 incident found", "secret scan refused this push")
    assert env.remote_sha("feat") == ""


@pytest.mark.parametrize(
    ("mode", "cause"),
    [
        ("missing", "ggshield skipped: not installed"),
        ("3", "ggshield skipped: exit 3: A GitGuardian API key is needed"),
        ("128", "ggshield skipped: exit 128"),
        ("hang", "ggshield skipped: no answer within 1s"),
    ],
)
def test_ggshield_unavailable_skips(env: Env, mode: str, cause: str) -> None:
    owner_remote(env)
    if mode == "missing":
        env.no_fake("ggshield")
    env.vars.update({"FAKE_GGSHIELD": mode, "ZEN_GGSHIELD_TIMEOUT": "1"})
    head = env.git("rev-parse", "HEAD")
    r = env.run("push", "origin", "HEAD:refs/heads/feat")
    assert r.returncode == 0, r.stderr
    assert env.remote_sha("feat") == head
    lines = [ln for ln in r.stderr.splitlines() if "ggshield skipped" in ln]
    assert len(lines) == 1, r.stderr
    assert lines[0].startswith("pre-push: " + cause), r.stderr
    assert "Terminated" not in r.stderr, r.stderr


def test_gitleaks_missing_refuses(env: Env) -> None:
    env.no_fake("gitleaks")
    r = env.run("push", "origin", "HEAD:refs/heads/feat")
    assert_refused(r, "gitleaks not found", "brew install gitleaks")
    assert env.remote_sha("feat") == ""


@pytest.mark.parametrize("rc", ["1", "2"])
def test_gitleaks_error_refuses(env: Env, rc: str) -> None:
    env.vars["FAKE_GITLEAKS"] = rc
    r = env.run("push", "origin", "HEAD:refs/heads/feat")
    assert_refused(r, f"gitleaks exited {rc}", "brew install gitleaks")
    assert env.remote_sha("feat") == ""


def test_gated_finding_never_calls_inbox(env: Env) -> None:
    env.vars["FAKE_GITLEAKS"] = "leak"
    assert_refused(env.run("push", "origin", "master"), "Fingerprint:")
    assert env.calls() == []
    assert env.remote_sha("master") == ""


def test_gated_clean_scan_keeps_gate(env: Env) -> None:
    head = env.git("rev-parse", "HEAD")
    r = env.run("push", "origin", "master")
    assert r.returncode == 0, r.stderr
    assert len(scans(env)) == 1
    fields = f"--match --action push --repo myrepo --branch master --sha {head}"
    assert env.calls() == [f"approval check {fields}", f"approval use {fields}"]


def test_no_new_commits_skips_scanners(env: Env) -> None:
    owner_remote(env)
    seed(env, "feat")
    env.git("tag", "v1")
    env.git("tag", "-a", "-m", "annotated", "v2")
    for refs in (["HEAD:refs/heads/feat"], ["v1", "v2"], [":feat"]):
        r = env.run("push", "origin", *refs)
        assert r.returncode == 0, r.stderr
    assert scans(env) == []
    assert env.calls(env.ggshield_log) == []


def test_annotated_tag_with_new_commit_is_peeled(env: Env) -> None:
    head = env.commit("tagged")
    env.git("tag", "-a", "-m", "annotated", "v3")
    assert env.run("push", "origin", "v3").returncode == 0
    (call,) = scans(env)
    assert_log_opts(call, f"{head} --not --remotes")


def test_multi_ref_one_finding_refuses_all(env: Env) -> None:
    seed(env, "a")
    base = env.git("rev-parse", "HEAD")
    bad = env.commit("bad")
    env.vars["FAKE_GITLEAKS"] = "leak"
    r = env.run("push", "origin", "HEAD:refs/heads/a", "HEAD:refs/heads/b")
    assert_refused(r, "Fingerprint:")
    (call,) = scans(env)
    assert_log_opts(call, f"{bad} --not {base} --remotes")
    assert env.remote_sha("a") == base and env.remote_sha("b") == ""


def test_multi_ref_scans_each_tip_with_its_own_config(env: Env) -> None:
    env.git("checkout", "-q", "-b", "fixtures")
    commit_file(env, ".gitleaks.toml", "[[allowlists]]\npaths = ['fixtures/']\n")
    allowed = env.git("rev-parse", "HEAD")
    env.git("checkout", "-q", "master")
    plain = env.commit("plain")
    append_cfg = FAKE_GITLEAKS.replace(
        'cat "$2" > "$FAKE_GITLEAKS_LOG.cfg"', 'cat "$2" >> "$FAKE_GITLEAKS_LOG.cfg"'
    )
    env.fake("gitleaks", append_cfg)
    r = env.run("push", "origin", "HEAD:refs/heads/a", "fixtures:refs/heads/b")
    assert r.returncode == 0, r.stderr
    plain_call, allowed_call = scans(env)
    assert_log_opts(plain_call, f"{plain} --not --remotes")
    assert f"--config {GLOBAL_CFG} " in plain_call
    assert_log_opts(allowed_call, f"{allowed} --not --remotes")
    assert f"--config {GLOBAL_CFG} " not in allowed_call
    assert "paths = ['fixtures/']" in (env.tmp / "gitleaks.log.cfg").read_text()


def test_multi_ref_finding_in_second_tip_refuses_all(env: Env) -> None:
    env.git("checkout", "-q", "-b", "two")
    two = env.commit("two")
    env.git("checkout", "-q", "master")
    leak_on_two = FAKE_GITLEAKS.replace(
        'echo "$*" >>', 'all="$*"; echo "$all" >>'
    ).replace(
        "case ${FAKE_GITLEAKS:-0} in",
        f'case $([[ $all == *"{two} --not"* ]] && echo leak) in',
    )
    env.fake("gitleaks", leak_on_two)
    r = env.run("push", "origin", "HEAD:refs/heads/a", "two:refs/heads/b")
    assert_refused(r, "Fingerprint:")
    assert len(scans(env)) == 2
    assert env.remote_sha("a") == "" and env.remote_sha("b") == ""


def test_repo_allowlist_is_layered_on_global(env: Env) -> None:
    allow = "[[allowlists]]\npaths = ['fixtures/']\n"
    commit_file(env, ".gitleaks.toml", allow)
    assert env.run("push", "origin", "HEAD:refs/heads/feat").returncode == 0
    (call,) = scans(env)
    assert f"--config {GLOBAL_CFG} " not in call
    merged = (env.tmp / "gitleaks.log.cfg").read_text()
    assert merged == f"{allow}\n[extend]\npath = '{GLOBAL_CFG}'\n"


def test_repo_config_with_extend_used_as_is(env: Env) -> None:
    own = "[extend]\nuseDefault = true\n"
    commit_file(env, ".gitleaks.toml", own)
    assert env.run("push", "origin", "HEAD:refs/heads/feat").returncode == 0
    assert f"--config {GLOBAL_CFG} " not in scans(env)[0]
    assert (env.tmp / "gitleaks.log.cfg").read_text() == own


def test_uncommitted_repo_config_not_read(env: Env) -> None:
    (env.repo / ".gitleaks.toml").write_text("[extend]\nuseDefault = true\n")
    assert env.run("push", "origin", "HEAD:refs/heads/feat").returncode == 0
    assert f"--config {GLOBAL_CFG} " in scans(env)[0]


def test_ggshield_cache_excluded_once(env: Env) -> None:
    owner_remote(env)
    exclude = env.repo / ".git" / "info" / "exclude"
    exclude.write_text("# no trailing newline")
    (env.repo / ".cache_ggshield").write_text("{}")
    for msg in ("one", "two"):
        (env.repo / f"{msg}.txt").write_text(msg)
        env.git("add", f"{msg}.txt")
        env.git("commit", "-q", "-m", msg)
        assert env.run("push", "origin", "HEAD:refs/heads/feat").returncode == 0
    lines = exclude.read_text().splitlines()
    assert lines == ["# no trailing newline", ".cache_ggshield"]
    assert env.git("status", "--porcelain") == ""


def test_hook_found_through_symlinked_hooks_dir(env: Env) -> None:
    link = env.tmp / "linked-hooks"
    link.symlink_to(HOOKS)
    env.git("config", "--global", "core.hooksPath", str(link))
    assert env.run("push", "origin", "HEAD:refs/heads/feat").returncode == 0
    assert f"--config {GLOBAL_CFG} " in scans(env)[0]


# --------------------------------------------------------------------------- #
# Real gitleaks smoke tests                                                    #
# --------------------------------------------------------------------------- #

real = pytest.mark.skipif(REAL_GITLEAKS is None, reason="gitleaks not installed")


@pytest.fixture
def leaks(env: Env) -> Env:
    assert REAL_GITLEAKS is not None
    env.no_fake("gitleaks")
    (env.bin / "gitleaks").symlink_to(REAL_GITLEAKS)
    return env


def synthetic(*parts: str) -> str:
    return "".join(parts)


def commit_file(env: Env, name: str, text: str) -> None:
    path = env.repo / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    env.git("add", name)
    env.git("commit", "-q", "-m", f"add {name}")


def fingerprint(stderr: str) -> str:
    for line in stderr.splitlines():
        if line.startswith("Fingerprint:"):
            return line.split(":", 1)[1].strip()
    raise AssertionError(stderr)


@real
def test_real_token_refused_then_ignored(leaks: Env) -> None:
    token = synthetic("ghp_", "Zq8wLp3vN7rTx2Kc", "9Hd4Mf6Ys1Bg5Jn0Qe7W")
    commit_file(leaks, "config.py", f'GH = "{token}"\n')
    r = leaks.run("push", "origin", "HEAD:refs/heads/feat")
    assert_refused(r, "RuleID:", "Fingerprint:")
    assert token not in r.stderr
    commit_file(leaks, ".gitleaksignore", f"# synthetic\n{fingerprint(r.stderr)}\n")
    r = leaks.run("push", "origin", "HEAD:refs/heads/feat")
    assert r.returncode == 0, r.stderr


@real
def test_real_uncommitted_ignore_not_honoured(leaks: Env) -> None:
    token = synthetic("ghp_", "Zq8wLp3vN7rTx2Kc", "9Hd4Mf6Ys1Bg5Jn0Qe7W")
    commit_file(leaks, "config.py", f'GH = "{token}"\n')
    r = leaks.run("push", "origin", "HEAD:refs/heads/feat")
    ignore = leaks.repo / ".gitleaksignore"
    ignore.write_text(f"# synthetic\n{fingerprint(r.stderr)}\n")
    assert_refused(leaks.run("push", "origin", "HEAD:refs/heads/feat"), "Fingerprint:")
    leaks.git("add", ".gitleaksignore")
    leaks.git("commit", "-q", "-m", "ignore")
    r = leaks.run("push", "origin", "HEAD:refs/heads/feat")
    assert r.returncode == 0, r.stderr


@real
def test_real_basic_auth_uri_refused(leaks: Env) -> None:
    pw = synthetic("Zq8w", "Lp3vN7rT")
    commit_file(leaks, "db.env", f"DB=postgres://app:{pw}@db.internal:5432/app\n")
    assert_refused(leaks.run("push", "origin", "HEAD:refs/heads/feat"), "zen-basic")


@real
@pytest.mark.parametrize(
    ("name", "text", "rule"),
    [
        ("pw.py", f'password = "{synthetic("XkTqWp", "LmZrHvBn")}"\n', "zen-password"),
        (
            "db.env",
            f"DB=postgres://app:{synthetic('Secret', '2024Qz')}@db.internal/x\n",
            "zen-basic",
        ),
    ],
)
def test_real_word_like_secret_refused(
    leaks: Env, name: str, text: str, rule: str
) -> None:
    commit_file(leaks, name, text)
    assert_refused(leaks.run("push", "origin", "HEAD:refs/heads/feat"), rule)


@real
def test_real_placeholder_passes(leaks: Env) -> None:
    text = 'password = "changeme"\nurl = "https://u:${PASS}@h"\n'
    commit_file(leaks, "settings.py", text)
    r = leaks.run("push", "origin", "HEAD:refs/heads/feat")
    assert r.returncode == 0, r.stderr


@real
def test_real_repo_path_allowlist_keeps_global_rules(leaks: Env) -> None:
    pw = synthetic("Zq8w", "Lp3vN7rT")
    commit_file(leaks, ".gitleaks.toml", "[[allowlists]]\npaths = ['^fixtures/']\n")
    commit_file(leaks, "fixtures/db.txt", f"postgres://app:{pw}@db.internal/x\n")
    r = leaks.run("push", "origin", "HEAD:refs/heads/feat")
    assert r.returncode == 0, r.stderr
    commit_file(leaks, "src/db.txt", f"postgres://app:{pw}@db.internal/x\n")
    r = leaks.run("push", "origin", "HEAD:refs/heads/feat")
    assert_refused(r, "zen-basic-auth-uri", "src/db.txt")


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, *sys.argv[1:]]))
