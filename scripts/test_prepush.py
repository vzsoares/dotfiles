#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "pytest>=8.0",
# ]
# ///
"""Tests for git/hooks (the global pre-push gate and hook dispatchers).

Run with:  uv run scripts/test_prepush.py
"""

from __future__ import annotations

import os
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path

import pytest

HOOKS = Path(__file__).resolve().parent.parent / "git" / "hooks"
ZERO = "0" * 40

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


@dataclass
class Env:
    tmp: Path
    repo: Path
    remote: Path
    bin: Path
    log: Path
    gum_log: Path
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

    def calls(self) -> list[str]:
        return self.log.read_text().splitlines() if self.log.exists() else []

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
    )
    (tmp_path / "gitconfig").write_text(
        f"[core]\n\thooksPath = {HOOKS}\n"
        "[user]\n\tname = tester\n\temail = t@t.dev\n"
        "[init]\n\tdefaultBranch = master\n"
    )
    e.bin.mkdir()
    e.fake("zen-inbox", FAKE_INBOX)
    e.fake("gum", FAKE_GUM)
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
        "refused master: no approved unused ask (check)",
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
    assert_refused(env.run("push", "origin", "master"), "refused master")
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
    assert_refused(env.run("push", "origin", *refs), "refused master")
    calls = env.calls()
    assert not any(c.startswith("approval use") for c in calls)
    assert all("--branch dev" not in c for c in calls)
    assert env.remote_sha("dev") == "" and env.remote_sha("master") == ""


def test_multi_ref_all_approved_uses_each(env: Env) -> None:
    r = env.run("push", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/master")
    assert r.returncode == 0, r.stderr
    verbs = [c.split()[1] for c in env.calls()]
    assert verbs == ["check", "check", "use", "use"]


@pytest.mark.parametrize("release", ["", "680.1"])
def test_deletion_refused(env: Env, release: str) -> None:
    seed(env, "master")
    env.vars.update({"ZEN_PROD_RELEASE": release, "FAKE_USED": "0"})
    r = env.run("push", "origin", ":master")
    assert_refused(r, "deleting master is refused", "git push --no-verify")
    assert env.remote_sha("master") != ""
    assert env.calls() == []


@pytest.mark.parametrize("release", ["", "680.1"])
def test_force_non_ancestor_refused(env: Env, release: str) -> None:
    seed(env, "master")
    before = env.remote_sha("master")
    env.git("checkout", "-q", "--orphan", "other")
    env.commit("orphan")
    env.vars.update({"ZEN_PROD_RELEASE": release, "FAKE_USED": "0"})
    r = env.run("push", "--force", "origin", "HEAD:refs/heads/master")
    assert_refused(r, "force push to master is refused", "git push --no-verify")
    assert env.remote_sha("master") == before


def test_force_unknown_remote_sha_refused(env: Env) -> None:
    seed(env, "master")
    other = env.tmp / "other"
    env.git("clone", "-q", str(env.remote), str(other), cwd=env.tmp)
    env.commit("theirs", cwd=other)
    env.git("push", "-q", "--no-verify", "origin", "master", cwd=other)
    theirs = env.remote_sha("master")
    env.commit("mine")
    r = env.run("push", "--force", "origin", "master")
    assert_refused(r, "force push to master is refused")
    assert env.remote_sha("master") == theirs


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
    assert_refused(env.run("push", "origin", "master"), "refused master")
    assert env.remote_sha("master") == ""


# --------------------------------------------------------------------------- #
# ZEN_PROD_RELEASE                                                             #
# --------------------------------------------------------------------------- #


def test_release_ancestor_passes_without_match(env: Env) -> None:
    approved = env.git("rev-parse", "HEAD")
    head = env.commit("bump")
    env.vars.update(
        {"ZEN_PROD_RELEASE": "680.1", "FAKE_USED": "0", "FAKE_USED_SHA": approved}
    )
    r = env.run("push", "origin", "master")
    assert r.returncode == 0, r.stderr
    assert env.remote_sha("master") == head
    assert env.calls() == [
        "approval used 680.1 --action release --repo myrepo --branch master"
    ]


def test_release_non_ancestor_falls_back(env: Env) -> None:
    env.git("checkout", "-q", "-b", "side")
    side = env.commit("side")
    env.git("checkout", "-q", "master")
    env.commit("main-line")
    env.vars.update(
        {"ZEN_PROD_RELEASE": "680.1", "FAKE_USED": "0", "FAKE_USED_SHA": side}
    )
    r = env.run("push", "origin", "master")
    assert r.returncode == 0, r.stderr
    verbs = [c.split()[1] for c in env.calls()]
    assert verbs == ["used", "check", "use"]


def test_release_unused_falls_back_and_refuses(env: Env) -> None:
    env.vars.update({"ZEN_PROD_RELEASE": "680.1", "FAKE_USED": "1", "FAKE_CHECK": "1"})
    assert_refused(env.run("push", "origin", "master"), "refused master")
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
    assert [c.split()[1] for c in env.calls()] == ["check", "use"]


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
    r = subprocess.run(["/bin/bash", "-n", str(HOOKS / "pre-push")], check=False)
    assert r.returncode == 0


if __name__ == "__main__":
    sys.exit(pytest.main([__file__, *sys.argv[1:]]))
