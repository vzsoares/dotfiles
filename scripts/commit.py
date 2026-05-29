#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "typer>=0.12",
# ]
# ///
#
# ==============================================================================
#
#       ____ ___  __  __ __  __ ___ _____
#      / ___/ _ \|  \/  |  \/  |_ _|_   _|      z e n - c o m m i t
#     | |  | | | | |\/| | |\/| || |  | |
#     | |__| |_| | |  | | |  | || |  | |
#      \____\___/|_|  |_|_|  |_|___| |_|
#
# ------------------------------------------------------------------------------
#
#  NAME
#      commit.py  --  interactive Conventional-Commit helper
#
#  SYNOPSIS
#      zen-commit [options]
#
#  OPTIONS
#      -a, --all          Stage all changes (git add -A) before committing.
#      -m, --message MSG  Use MSG verbatim (skips AI generation).
#      -y, --yes          Headless: accept the generated message, no prompts.
#          --force        Headless: commit even if the secret guardrail fires.
#
#  DESCRIPTION
#      Stages files (interactive picker, or --all), scans the staged diff for
#      secrets / sensitive files, generates a Conventional Commit message with
#      Claude (haiku), then lets you commit / edit / regenerate. Shells out to
#      `gum` for the UI and `claude` for the message.
#
#      Runs HEADLESS (no gum prompts) when there's no TTY, or when --yes / -m is
#      given — so it works inside non-interactive tools. Headless requires staged
#      changes or --all, and the guardrail aborts on findings unless --force.
#
#  REQUIRES
#      git, gum, and (unless -m) claude.
#
#  AUTHOR
#      vzsoares.
#
# ==============================================================================
"""Interactive Conventional-Commit helper (Python port of commit.sh)."""

from __future__ import annotations

import re
import shutil
import subprocess
import sys

import typer

# --------------------------------------------------------------------------- #
# Secret / sensitive-content guardrail                                         #
# --------------------------------------------------------------------------- #

CRYPTO_EXTS = (".pem", ".key", ".pfx", ".p12", ".keystore", ".jks")
SSH_KEY_MARKERS = ("id_rsa", "id_ed25519", "id_ecdsa", "id_dsa")
ENV_OK_SUFFIXES = (".example", ".sample", ".template")
CRED_BASENAMES = ("token.json", "credentials.json", "auth.json")

# Content patterns scanned against ADDED diff lines: (regex, label).
CONTENT_PATTERNS: tuple[tuple[str, str], ...] = (
    (r"AKIA[0-9A-Z]{16}", "AWS Access Key ID"),
    (r"ASIA[0-9A-Z]{16}", "AWS temporary credentials"),
    (r"gh[pousr]_[A-Za-z0-9]{36}", "GitHub PAT"),
    (r"github_pat_[A-Za-z0-9_]{80,}", "GitHub fine-grained PAT"),
    (r"glpat-[A-Za-z0-9_-]{20}", "GitLab PAT"),
    (r"sk-ant-(api03|admin01)-[A-Za-z0-9_-]{80,}", "Anthropic API key"),
    (r"sk-(proj-)?[A-Za-z0-9]{40,}", "OpenAI API key"),
    (r"xox[baprs]-[A-Za-z0-9-]{10,}", "Slack token"),
    (r"BEGIN (RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY", "Private key block"),
)

# Lock / generated files: list names but omit their (noisy) diff body.
NOISY_BASENAMES = frozenset(
    (
        "package-lock.json",
        "yarn.lock",
        "pnpm-lock.yaml",
        "bun.lock",
        "bun.lockb",
        "Cargo.lock",
        "Gemfile.lock",
        "poetry.lock",
        "composer.lock",
        "Pipfile.lock",
        "go.sum",
        "mix.lock",
        "flake.lock",
        "Podfile.lock",
        "uv.lock",
    )
)
NOISY_SUFFIXES = (".min.js", ".min.css", ".map", ".snap")


def scan_filenames(files: list[str]) -> list[str]:
    """Flag sensitive file paths (pure — no git/IO)."""
    out: list[str] = []
    for f in files:
        base = f.rsplit("/", 1)[-1]
        is_pub = f.endswith(".pub") or ".pub." in f
        if f.endswith(CRYPTO_EXTS) and not is_pub:
            out.append(f"{f} — sensitive crypto extension")
        if any(m in f for m in SSH_KEY_MARKERS) and not f.endswith(".pub"):
            out.append(f"{f} — private SSH key")
        is_env = (
            base == ".env"
            or base.startswith(".env.")
            or f.endswith(".env")
            or ".env." in f
        )
        if is_env and not f.endswith(ENV_OK_SUFFIXES):
            out.append(f"{f} — env file")
        if base in CRED_BASENAMES or (
            base.startswith("service-account") and base.endswith(".json")
        ):
            out.append(f"{f} — looks like a credentials file")
    return out


def scan_content(added: str) -> list[str]:
    """Flag secret-looking content in ADDED diff lines (pure)."""
    return [
        f"content matched: {label}"
        for regex, label in CONTENT_PATTERNS
        if re.search(regex, added)
    ]


def scan_secrets() -> list[str]:
    """Full staged-content guardrail (reads git state)."""
    files = staged_files()
    violations = scan_filenames(files)
    for f in files:
        if f.rsplit("/", 1)[-1] == ".npmrc":
            content = git("show", f":{f}").stdout
            if "_authToken" in content:
                violations.append(f"{f} — contains _authToken")
    # Added lines, minus any carrying a `gitleaks:allow` marker (honored by
    # gitleaks too) so intentional fixtures/examples don't trip the guardrail.
    added = "\n".join(
        line
        for line in git("diff", "--cached", "-U0").stdout.splitlines()
        if re.match(r"^\+[^+]", line) and "gitleaks:allow" not in line
    )
    violations += scan_content(added)
    return violations


def is_noisy_file(name: str) -> bool:
    base = name.rsplit("/", 1)[-1]
    return base in NOISY_BASENAMES or name.endswith(NOISY_SUFFIXES)


# --------------------------------------------------------------------------- #
# Message cleanup (defensive post-processing of the model output)             #
# --------------------------------------------------------------------------- #

_TAG_ONLY = re.compile(r"^\s*</?[A-Za-z][^>]*/?>\s*$")


def clean_message(raw: str) -> str:
    """Strip code fences, fake tool/XML tags, and surrounding blank lines."""
    lines = raw.splitlines()
    # If fenced blocks exist, keep the LAST block's contents.
    if any(line.startswith("```") for line in lines):
        blocks: list[list[str]] = []
        current: list[str] | None = None
        for line in lines:
            if line.startswith("```"):
                if current is None:
                    current = []
                else:
                    blocks.append(current)
                    current = None
                continue
            if current is not None:
                current.append(line)
        if blocks:
            lines = blocks[-1]
    lines = [line for line in lines if not _TAG_ONLY.match(line)]
    return "\n".join(lines).strip()


def looks_like_error(msg: str) -> bool:
    """Heuristic: model output is actually a CLI error (login prompt, TUI box)."""
    return bool(re.search(r"(?m)^\s*[┌└│├]|Please run /login|Not logged in", msg))


# --------------------------------------------------------------------------- #
# gum / git wrappers                                                           #
# --------------------------------------------------------------------------- #


def _gum(*args: str) -> str:
    # Capture stdout only; gum draws its UI on stderr and reads the tty.
    result = subprocess.run(
        ["gum", *args], text=True, stdout=subprocess.PIPE, check=False
    )
    if result.returncode != 0:
        raise typer.Abort()
    return result.stdout.strip()


def gum_style(text: str, *flags: str) -> None:
    subprocess.run(["gum", "style", *flags, text], check=False)


def info(text: str) -> None:
    gum_style(text, "--faint")


def good(text: str) -> None:
    gum_style(text, "--foreground", "82")


def warn(text: str) -> None:
    gum_style(text, "--foreground", "214")


def fail(text: str) -> None:
    gum_style(text, "--foreground", "196")


def gum_choose_multi(header: str, options: list[str]) -> list[str]:
    out = _gum("choose", "--no-limit", "--header", header, *options)
    return [line for line in out.splitlines() if line.strip()]


def gum_choose(header: str, options: list[str]) -> str:
    return _gum("choose", "--header", header, *options)


def gum_write(header: str, value: str = "", placeholder: str = "") -> str:
    args = ["write", "--header", header]
    if value:
        args += ["--value", value]
    if placeholder:
        args += ["--placeholder", placeholder]
    return _gum(*args)


def gum_confirm(prompt: str, default_yes: bool = True) -> bool:
    args = ["gum", "confirm", prompt]
    if not default_yes:
        args.append("--default=false")
    return subprocess.run(args, check=False).returncode == 0


def git(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["git", *args], text=True, capture_output=True, check=False)


def staged_files() -> list[str]:
    return [f for f in git("diff", "--cached", "--name-only").stdout.splitlines() if f]


# --------------------------------------------------------------------------- #
# Message generation                                                          #
# --------------------------------------------------------------------------- #

CLAUDE_BASE = [
    "claude",
    "-p",
    "--tools",
    "",
    "--strict-mcp-config",
    "--no-session-persistence",
    "--disable-slash-commands",
    "--model",
    "haiku",
]

PROMPT_BASE = """Write a concise Conventional Commit message for the staged diff below.

The diff IS the complete change. You have no tools and cannot read files — do not attempt to investigate further or simulate tool calls.

Respond with ONLY the commit message text. No code fences, no preamble, no reasoning, no XML tags, no co-author, no attribution, no trailers."""

SYSTEM_PROMPT = (
    "You are a commit-message generator. Your entire response must be the commit "
    "message text and nothing else. Never include reasoning, planning, tool calls, "
    "XML tags, code fences, or any other wrapping. Never claim to read or "
    "investigate files. Treat the diff in the user message as the complete and "
    "only context."
)


def build_diff() -> str:
    """Staged diff for the prompt: normal files in full, noisy files by name only."""
    files = staged_files()
    normal = [f for f in files if not is_noisy_file(f)]
    noisy = [f for f in files if is_noisy_file(f)]
    parts: list[str] = []
    if normal:
        parts.append(git("diff", "--cached", "--", *normal).stdout)
    if noisy:
        parts.append("\n# Lock/generated files changed (diff omitted):")
        parts += [f"#   {f}" for f in noisy]
    return "\n".join(parts)


def run_claude(diff_text: str, full_prompt: str) -> str:
    """Invoke the Claude CLI to produce a raw commit message (monkeypatched in tests)."""
    result = subprocess.run(
        [
            "gum", "spin", "--spinner", "dot",
            "--title", "Generating commit message...", "--",
            *CLAUDE_BASE, "--append-system-prompt", SYSTEM_PROMPT, full_prompt,
        ],
        input=diff_text,
        text=True,
        stdout=subprocess.PIPE,
        check=False,
    )
    return result.stdout


def generate_message(context: str) -> str:
    full_prompt = PROMPT_BASE
    if context:
        full_prompt += f"\n\nAdditional guidance from the user: {context}"
    return clean_message(run_claude(build_diff(), full_prompt))


# --------------------------------------------------------------------------- #
# Orchestration                                                               #
# --------------------------------------------------------------------------- #


def _show_and_validate(message: str) -> None:
    if not message:
        fail("Claude returned an empty message.")
        raise typer.Exit(1)
    if looks_like_error(message):
        fail("Claude CLI returned an error instead of a message:")
        print(message)
        raise typer.Exit(1)
    gum_style(
        message, "--border", "normal", "--padding", "0 1", "--border-foreground", "212"
    )


def do_commit(stage_all: bool, message: str, yes: bool, force: bool) -> None:
    headless = (
        (not sys.stdin.isatty()) or (not sys.stdout.isatty()) or yes or bool(message)
    )

    gum_style(
        "Commit",
        "--bold",
        "--border",
        "double",
        "--padding",
        "0 2",
        "--border-foreground",
        "212",
    )

    if stage_all:
        git("add", "-A")

    if git("diff", "--cached", "--quiet").returncode == 0:
        unstaged = [
            parts[1]
            for line in git("status", "--porcelain").stdout.splitlines()
            if (parts := line.split()) and len(parts) >= 2
        ]
        if not unstaged:
            fail("Nothing to commit. Working tree clean.")
            raise typer.Exit(1)
        if headless:
            fail("Nothing staged. Stage files or pass --all.")
            raise typer.Exit(1)
        info("No staged changes. Select files to stage:")
        selected = gum_choose_multi(
            "Stage which files? (space to select, enter to confirm)", unstaged
        )
        if not selected:
            fail("No files selected.")
            raise typer.Exit(1)
        git("add", *selected)
        info(f"Staged: {' '.join(selected)}")

    violations = scan_secrets()
    if violations:
        gum_style(
            "Guardrail: possible secret / sensitive content",
            "--bold",
            "--border",
            "thick",
            "--padding",
            "0 2",
            "--border-foreground",
            "196",
            "--foreground",
            "196",
        )
        for v in violations:
            print(f"  • {v}")
        if force:
            warn("Override accepted (--force) — proceeding.")
        elif headless:
            fail(
                "Aborting. Re-run with --force to override, or unstage the flagged files."
            )
            raise typer.Exit(1)
        else:
            if (
                gum_choose("How to proceed?", ["abort", "override (commit anyway)"])
                == "abort"
            ):
                info("Aborted. Unstage with: git reset HEAD <file>")
                raise typer.Exit(1)
            if not gum_confirm("Really commit flagged content?", default_yes=False):
                info("Aborted.")
                raise typer.Exit(1)
            warn("Override accepted — proceeding.")

    if message:
        final = message
    elif headless:
        final = generate_message("")
        _show_and_validate(final)
    else:
        final = _interactive_message_loop()

    git("commit", "-m", final)
    good("Committed")


def _interactive_message_loop() -> str:
    context = ""
    while True:
        message = generate_message(context)
        _show_and_validate(message)
        action = gum_choose("What now?", ["commit", "edit", "regenerate", "cancel"])
        if action == "commit":
            return message
        if action == "edit":
            edited = gum_write("Edit commit message (ctrl+d to save)", value=message)
            if not edited:
                fail("Empty message. Aborting.")
                raise typer.Exit(1)
            return edited
        if action == "regenerate":
            context = gum_write(
                "Extra guidance (ctrl+d to submit)",
                placeholder="e.g. 'this is a fix, not a feat' — empty to just retry",
            )
            continue
        if action == "cancel":
            info("Aborted.")
            raise typer.Exit(0)


def main(
    stage_all: bool = typer.Option(
        False, "--all", "-a", help="Stage all changes before committing."
    ),
    message: str = typer.Option(
        "", "--message", "-m", help="Use this message verbatim (skip AI generation)."
    ),
    yes: bool = typer.Option(
        False,
        "--yes",
        "-y",
        help="Headless: accept the generated message without prompting.",
    ),
    force: bool = typer.Option(
        False, "--force", help="Headless: commit even if the guardrail fires."
    ),
) -> None:
    """Interactive Conventional-Commit helper."""
    if shutil.which("gum") is None:
        print(
            "Error: gum is required. Install it: https://github.com/charmbracelet/gum"
        )
        raise typer.Exit(1)
    if git("rev-parse", "--git-dir").returncode != 0:
        fail("Error: Not inside a git repository.")
        raise typer.Exit(1)
    if not message and shutil.which("claude") is None:
        fail("Error: claude CLI is required (or pass -m).")
        raise typer.Exit(1)
    do_commit(stage_all, message, yes, force)


if __name__ == "__main__":
    typer.run(main)
