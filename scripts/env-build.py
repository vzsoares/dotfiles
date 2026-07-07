#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#     "boto3>=1.34",
#     "typer>=0.12",
# ]
# ///
#
# ==============================================================================
#
#      _____  _   ___     __   ____  _   _ ___ _     ____
#     | ____|| \ | \ \   / /  | __ )| | | |_ _| |   |  _ \
#     |  _|  |  \| |\ \ / /   |  _ \| | | || || |   | | | |
#     | |___ | |\  | \ V /    | |_) | |_| || || |___| |_| |
#     |_____||_| \_|  \_/     |____/ \___/|___|_____|____/
#
# ------------------------------------------------------------------------------
#
#  NAME
#      env-build.py  --  resolve .env.example parameters from AWS SSM
#
#  SYNOPSIS
#      zen-env [options]
#
#  OPTIONS
#      -i, --input FILE    Template to read (default: .env.example)
#      -o, --output FILE   File to write   (default: .env)
#      -e, --env ENV       Environment prefix to try first (e.g. dev, prod)
#      -p, --prefix PATH   Additional SSM path prefix (e.g. /myapp)
#          --dry-run       Print resolved output without writing
#          --profile NAME  AWS profile name
#          --region NAME   AWS region
#      -y, --yes           Skip write confirmation
#
#  SSM RESOLUTION ORDER  (for a key named FOO_BAR)
#      --env dev --prefix /myapp  →  /myapp/dev/FOO_BAR  →  /myapp/FOO_BAR
#      --env dev                  →  /dev/FOO_BAR         →  /FOO_BAR
#      --prefix /myapp            →  /myapp/FOO_BAR
#      (none)                     →  /FOO_BAR
#
#  NOTES
#      - SecureString parameters are decrypted automatically.
#      - Keys absent from SSM keep their default from the template (if any).
#      - Comments and blank lines in the template are preserved in the output.
#
# ==============================================================================

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path
from typing import Optional

import typer

app = typer.Typer(add_completion=False)

# ─── gum helpers ─────────────────────────────────────────────────────────────


def _require(cmd: str) -> None:
    if subprocess.run(["which", cmd], capture_output=True).returncode != 0:
        print(f"Error: {cmd} is required but not found on PATH.", file=sys.stderr)
        raise typer.Exit(1)


def gum_style(text: str, *flags: str) -> None:
    subprocess.run(["gum", "style", *flags, text], check=False)


def info(text: str) -> None:
    gum_style(text, "--faint")


def ok(text: str) -> None:
    gum_style(text, "--foreground", "82")


def warn(text: str) -> None:
    gum_style(text, "--foreground", "214")


def fail(text: str) -> None:
    gum_style(text, "--foreground", "196")


def gum_confirm(prompt: str) -> bool:
    return subprocess.run(["gum", "confirm", prompt], check=False).returncode == 0


# ─── .env parsing ────────────────────────────────────────────────────────────

_VAR_RE = re.compile(r'^([A-Za-z_][A-Za-z0-9_]*)=(.*)$')

Entry = tuple[str, str | None, str]  # (kind, key|None, raw_line)


def parse_env_file(path: Path) -> list[Entry]:
    entries: list[Entry] = []
    for raw in path.read_text().splitlines():
        m = _VAR_RE.match(raw)
        if m:
            entries.append(("var", m.group(1), raw))
        else:
            entries.append(("other", None, raw))
    return entries


# ─── SSM helpers ─────────────────────────────────────────────────────────────


def _candidates(key: str, env: str | None, prefix: str | None) -> list[str]:
    base = f"/{prefix.strip('/')}" if prefix else ""
    paths = []
    if env:
        paths.append(f"{base}/{env}/{key}")
    paths.append(f"{base}/{key}")
    return paths


def fetch_ssm(ssm, key: str, env: str | None, prefix: str | None) -> tuple[str | None, str | None]:
    """Return (value, resolved_path) or (None, None) if not found anywhere."""
    from botocore.exceptions import ClientError

    for path in _candidates(key, env, prefix):
        try:
            resp = ssm.get_parameter(Name=path, WithDecryption=True)
            return resp["Parameter"]["Value"], path
        except ClientError as exc:
            code = exc.response["Error"]["Code"]
            if code in ("ParameterNotFound", "ParameterVersionNotFound"):
                continue
            raise
    return None, None


# ─── output formatting ───────────────────────────────────────────────────────

_NEEDS_QUOTE = re.compile(r'[\s#$`"\'\\!]')


def quote_value(value: str) -> str:
    if not value:
        return ""
    if _NEEDS_QUOTE.search(value) or value != value.strip():
        return '"' + value.replace("\\", "\\\\").replace('"', '\\"') + '"'
    return value


# ─── main ────────────────────────────────────────────────────────────────────


@app.command()
def main(
    input: Path = typer.Option(Path(".env.example"), "--input", "-i", help="Template file"),
    output: Path = typer.Option(Path(".env"), "--output", "-o", help="Output .env file"),
    env: Optional[str] = typer.Option(None, "--env", "-e", help="Env prefix (dev/prod/...)"),
    prefix: Optional[str] = typer.Option(None, "--prefix", "-p", help="SSM path prefix"),
    dry_run: bool = typer.Option(False, "--dry-run", help="Print without writing"),
    profile: Optional[str] = typer.Option(None, "--profile", help="AWS profile"),
    region: Optional[str] = typer.Option(None, "--region", help="AWS region"),
    yes: bool = typer.Option(False, "--yes", "-y", help="Skip confirmation"),
) -> None:
    _require("gum")

    if not input.exists():
        fail(f"Template not found: {input}")
        raise typer.Exit(1)

    entries = parse_env_file(input)
    var_entries = [(key, raw) for kind, key, raw in entries if kind == "var"]

    if not var_entries:
        warn(f"No variables found in {input}")
        raise typer.Exit(0)

    # ── boto3 ─────────────────────────────────────────────────────────────────
    try:
        import boto3
        from botocore.exceptions import ClientError, NoCredentialsError, ProfileNotFound
    except ImportError:
        fail("boto3 is not available (should be installed by uv automatically).")
        raise typer.Exit(1)

    try:
        session = boto3.Session(profile_name=profile, region_name=region)
        ssm = session.client("ssm")
    except ProfileNotFound as exc:
        fail(str(exc))
        raise typer.Exit(1)

    # ── resolve ───────────────────────────────────────────────────────────────
    hint = f"  env={env}" if env else ""
    hint += f"  prefix={prefix}" if prefix else ""
    info(f"Fetching {len(var_entries)} parameters from SSM{hint} …")

    results: dict[str, tuple[str | None, str | None]] = {}
    try:
        for key, _ in var_entries:
            results[key] = fetch_ssm(ssm, key, env, prefix)
    except NoCredentialsError:
        fail("AWS credentials not found. Set AWS_* env vars, ~/.aws/credentials, or use --profile.")
        raise typer.Exit(1)
    except ClientError as exc:
        fail(f"AWS error: {exc}")
        raise typer.Exit(1)

    # ── status report ─────────────────────────────────────────────────────────
    missing: list[str] = []
    for key, raw in var_entries:
        value, path = results[key]
        default = (_VAR_RE.match(raw) or [None, None, ""])[2]  # type: ignore[index]
        m = _VAR_RE.match(raw)
        default = m.group(2) if m else ""

        label = f"  {key:<32}"
        if value is not None:
            ok(f"{label}✓  {path}")
        elif default:
            warn(f"{label}-  kept default: {default!r}")
        else:
            fail(f"{label}✗  not found")
            missing.append(key)

    n_found = sum(1 for v, _ in results.values() if v is not None)
    n_default = len(var_entries) - n_found - len(missing)
    info(f"\n  {n_found} from SSM · {n_default} defaults · {len(missing)} missing\n")

    # ── build content ─────────────────────────────────────────────────────────
    out_lines: list[str] = []
    for kind, key, raw in entries:
        if kind != "var":
            out_lines.append(raw)
            continue
        value, _ = results[key]
        if value is not None:
            out_lines.append(f"{key}={quote_value(value)}")
        else:
            out_lines.append(raw)

    content = "\n".join(out_lines) + "\n"

    if dry_run:
        info("── dry-run ─────────────────────────────────────────")
        print(content)
        return

    if not yes and not gum_confirm(f"Write {len(var_entries)} vars to {output}?"):
        raise typer.Abort()

    output.write_text(content)
    ok(f"Written → {output}")


if __name__ == "__main__":
    app()
