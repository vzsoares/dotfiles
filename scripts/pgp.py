#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""zen-pgp — a simple gum-driven PGP encrypt/decrypt helper.

Wraps `gpg` for the two everyday operations:

  * encrypt  — symmetric (passphrase) or asymmetric (to a recipient key)
  * decrypt  — auto-detects symmetric vs. key-based

Usage:
    zen-pgp                      # interactive menu
    zen-pgp encrypt [FILE]       # encrypt a file
    zen-pgp decrypt [FILE]       # decrypt a .gpg/.asc file

Without arguments everything is asked through gum. Requires `gum` and `gpg`
on PATH.
"""

from __future__ import annotations

import shutil
import subprocess
import sys
from pathlib import Path

# ── gum / shell helpers ────────────────────────────────────────────────────


def die(msg: str, code: int = 1) -> None:
    gum("style", "--foreground", "1", f"✗ {msg}")
    sys.exit(code)


def info(msg: str) -> None:
    gum("style", "--foreground", "4", msg)


def ok(msg: str) -> None:
    gum("style", "--foreground", "2", f"✓ {msg}")


def gum(*args: str, capture: bool = False, check: bool = True) -> str:
    """Run a gum subcommand. Returns stdout when capture=True.

    Only stdout is piped — gum renders its interactive TUI on stderr, so
    stderr must stay attached to the terminal or the widget appears to hang.
    """
    proc = subprocess.run(
        ["gum", *args],
        text=True,
        stdout=subprocess.PIPE if capture else None,
    )
    if proc.returncode != 0:
        # gum returns non-zero when the user aborts (Esc / Ctrl-C)
        if check or capture:
            sys.exit(130)
    return (proc.stdout or "").strip() if capture else ""


def choose(header: str, *options: str) -> str:
    return gum("choose", "--header", header, *options, capture=True)


def prompt(placeholder: str, value: str = "") -> str:
    args = ["input", "--placeholder", placeholder]
    if value:
        args += ["--value", value]
    return gum(*args, capture=True)


def password(placeholder: str = "Passphrase") -> str:
    return gum("input", "--password", "--placeholder", placeholder, capture=True)


def confirm(msg: str) -> bool:
    return subprocess.run(["gum", "confirm", msg]).returncode == 0


# ── core operations ────────────────────────────────────────────────────────


def pick_file(header: str) -> Path:
    """Let the user pick an existing file via gum file (fallback: input)."""
    selection = gum("file", "--header", header, capture=True, check=False)
    if not selection:
        selection = prompt("Path to file")
    if not selection:
        die("no file selected")
    path = Path(selection).expanduser()
    if not path.is_file():
        die(f"not a file: {path}")
    return path


def run_gpg(args: list[str], passphrase: str | None = None) -> None:
    cmd = ["gpg"]
    if passphrase is not None:
        cmd += ["--batch", "--yes", "--pinentry-mode", "loopback",
                "--passphrase-fd", "0"]
    cmd += args
    proc = subprocess.run(
        cmd,
        input=(passphrase + "\n") if passphrase is not None else None,
        text=True,
    )
    if proc.returncode != 0:
        die("gpg failed")


def encrypt(target: Path | None) -> None:
    src = target or pick_file("File to encrypt")
    mode = choose(
        "Encryption mode",
        "Password (symmetric)",
        "Recipient key (asymmetric)",
    )

    if mode.startswith("Password"):
        passphrase = password("Encryption passphrase")
        if not passphrase:
            die("empty passphrase")
        confirm_pass = password("Confirm passphrase")
        if passphrase != confirm_pass:
            die("passphrases do not match")
        algo = choose(
            "Cipher algorithm",
            "AES256", "AES192", "AES", "TWOFISH",
            "CAMELLIA256", "CAMELLIA192", "CAMELLIA128",
        )
        out = src.with_suffix(src.suffix + ".gpg")
        run_gpg(
            ["--symmetric", "--cipher-algo", algo,
             "--output", str(out), str(src)],
            passphrase=passphrase,
        )
    else:
        recipient = prompt("Recipient (email or key id)")
        if not recipient:
            die("no recipient given")
        out = src.with_suffix(src.suffix + ".gpg")
        run_gpg(
            ["--encrypt", "--recipient", recipient,
             "--output", str(out), str(src)],
        )

    ok(f"encrypted → {out}")


def decrypt(target: Path | None) -> None:
    src = target or pick_file("File to decrypt")

    # Strip a trailing .gpg/.asc/.pgp for the default output name.
    if src.suffix in {".gpg", ".asc", ".pgp"}:
        out = src.with_suffix("")
    else:
        out = src.with_name(src.name + ".dec")

    out = Path(prompt("Output path", str(out)) or out).expanduser()
    if out.exists() and not confirm(f"{out} exists — overwrite?"):
        die("aborted", code=0)

    # Symmetric files need a passphrase; key-based files use the agent.
    # Ask the user which; loopback passphrase is harmless for key files too,
    # but agent handling differs, so let them choose.
    mode = choose("Decryption mode", "Password (symmetric)", "Private key")
    if mode.startswith("Password"):
        passphrase = password("Passphrase")
        run_gpg(["--decrypt", "--output", str(out), str(src)],
                passphrase=passphrase)
    else:
        run_gpg(["--decrypt", "--output", str(out), str(src)])

    ok(f"decrypted → {out}")


# ── entrypoint ─────────────────────────────────────────────────────────────


def main() -> None:
    for binary in ("gum", "gpg"):
        if shutil.which(binary) is None:
            sys.stderr.write(f"zen-pgp: `{binary}` not found on PATH\n")
            sys.exit(127)

    args = sys.argv[1:]
    action = args[0] if args else None
    target = Path(args[1]).expanduser() if len(args) > 1 else None

    if action is None:
        action = choose("zen-pgp", "Encrypt", "Decrypt").lower()

    if action in {"encrypt", "enc", "e"}:
        encrypt(target)
    elif action in {"decrypt", "dec", "d"}:
        decrypt(target)
    else:
        die(f"unknown action: {action}")


if __name__ == "__main__":
    main()
