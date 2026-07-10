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
#      _____ ___ ____  _
#     |__  /| __|  _ \| |      z e n - y t d l
#       / / |  _| | | | |
#      / /_ | |_| |_| | |___
#     /____||___|____/|_____|
#
# ------------------------------------------------------------------------------
#
#  NAME
#      ytdl.py  --  interactive yt-dlp downloader with WhatsApp-safe re-encode
#
#  SYNOPSIS
#      zen-ytdl [URL] [options]
#
#  OPTIONS
#      -f, --format {video,audio}   What to grab. Prompted if omitted.
#      -r, --resolution RES         Max height for video: 360|480|720|1080|best.
#      -a, --audio-format FMT       Audio-only container: mp3|m4a|opus.
#      -s, --start TS               Trim start (e.g. 0:15 or 12). Blank = start.
#      -e, --end TS                 Trim end   (e.g. 1:30 or 90). Blank = end.
#      -o, --output DIR             Destination directory (default: cwd).
#          --no-fix                 Skip the WhatsApp re-encode; keep raw file.
#          --keep-raw               Keep the downloaded source next to the fix.
#      -y, --yes                    Headless: no gum prompts, use flags/defaults.
#
#  DESCRIPTION
#      Downloads a YouTube (or any yt-dlp-supported) URL, letting you pick
#      video vs. audio and a max resolution via `gum`. Video is then re-encoded
#      to a universally WhatsApp-compatible MP4 (H.264 baseline/high + AAC +
#      faststart, even dimensions) so it actually plays in the app instead of
#      only opening in a local player. Audio is extracted straight to mp3/m4a.
#
#      Runs HEADLESS (no gum prompts) when there is no TTY or when --yes is
#      given — url + format + resolution come from the flags/defaults.
#
#  REQUIRES
#      yt-dlp, ffmpeg/ffprobe, gum.
#
# ==============================================================================

from __future__ import annotations

import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

import typer

VIDEO_RESOLUTIONS = ["360", "480", "720", "1080", "best"]
AUDIO_FORMATS = ["mp3", "m4a", "opus"]


# --------------------------------------------------------------------------- #
# gum / shell wrappers                                                         #
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


def gum_choose(header: str, options: list[str]) -> str:
    return _gum("choose", "--header", header, *options)


def gum_input(header: str, placeholder: str = "") -> str:
    args = ["input", "--header", header]
    if placeholder:
        args += ["--placeholder", placeholder]
    return _gum(*args)


def gum_confirm(prompt: str, default_yes: bool = True) -> bool:
    args = ["gum", "confirm", prompt]
    if not default_yes:
        args.append("--default=false")
    return subprocess.run(args, check=False).returncode == 0


def run(*args: str) -> None:
    # Inherit stdio so yt-dlp / ffmpeg progress is visible live.
    if subprocess.run(args, check=False).returncode != 0:
        fail(f"Command failed: {args[0]}")
        raise typer.Exit(1)


# --------------------------------------------------------------------------- #
# download + fix                                                               #
# --------------------------------------------------------------------------- #


def build_format(kind: str, resolution: str) -> str:
    if kind == "audio":
        return "bestaudio/best"
    if resolution == "best":
        return "bv*+ba/b"
    return f"bv*[height<={resolution}]+ba/b[height<={resolution}]/b"


def build_section(start: str, end: str) -> str | None:
    """yt-dlp --download-sections spec, or None for the whole video."""
    if not start and not end:
        return None
    return f"*{start or '0'}-{end or 'inf'}"


def download(
    url: str,
    kind: str,
    resolution: str,
    audio_format: str,
    section: str | None,
) -> Path:
    """Download into a temp dir; return the path of the produced file."""
    tmp = Path(tempfile.mkdtemp(prefix="zen-ytdl-"))
    path_file = tmp / ".filepath"
    cmd = [
        "yt-dlp",
        "--no-simulate",
        "--print-to-file",
        "after_move:filepath",
        str(path_file),
        "-o",
        str(tmp / "%(title)s.%(ext)s"),
        "-f",
        build_format(kind, resolution),
    ]
    if section:
        # force-keyframes gives accurate cut boundaries rather than snapping
        # out to the nearest surrounding keyframes.
        cmd += ["--download-sections", section, "--force-keyframes-at-cuts"]
    if kind == "audio":
        cmd += ["-x", "--audio-format", audio_format, "--audio-quality", "0"]
    else:
        cmd += ["--merge-output-format", "mp4"]
    cmd.append(url)

    run(*cmd)

    if not path_file.exists():
        fail("yt-dlp did not report an output file.")
        shutil.rmtree(tmp, ignore_errors=True)
        raise typer.Exit(1)
    return Path(path_file.read_text().strip())


def probe_height(src: Path) -> int | None:
    out = subprocess.run(
        [
            "ffprobe", "-v", "error", "-select_streams", "v:0",
            "-show_entries", "stream=height", "-of", "csv=p=0", str(src),
        ],
        text=True, stdout=subprocess.PIPE, check=False,
    ).stdout.strip()
    return int(out) if out.isdigit() else None


def whatsapp_fix(src: Path, dst: Path) -> None:
    """Re-encode to a WhatsApp-safe MP4: H.264 + AAC + faststart, even dims."""
    height = probe_height(src)
    # baseline maxes out around 720p; use high profile above that so libx264
    # can pick a valid level instead of erroring on an out-of-range one.
    profile = "baseline" if height and height <= 720 else "high"
    run(
        "ffmpeg", "-y", "-loglevel", "error", "-stats", "-i", str(src),
        "-c:v", "libx264", "-profile:v", profile, "-pix_fmt", "yuv420p",
        "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2",
        "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", str(dst),
    )


def unique(path: Path) -> Path:
    if not path.exists():
        return path
    stem, suffix, parent = path.stem, path.suffix, path.parent
    n = 1
    while (candidate := parent / f"{stem} ({n}){suffix}").exists():
        n += 1
    return candidate


# --------------------------------------------------------------------------- #
# main                                                                         #
# --------------------------------------------------------------------------- #


def main(
    url: str = typer.Argument("", help="Video URL (prompted if omitted)."),
    fmt: str = typer.Option(
        "", "--format", "-f", help="video | audio (prompted if omitted)."
    ),
    resolution: str = typer.Option(
        "", "--resolution", "-r", help="Max height: 360|480|720|1080|best."
    ),
    audio_format: str = typer.Option(
        "mp3", "--audio-format", "-a", help="Audio container: mp3|m4a|opus."
    ),
    start: str = typer.Option(
        "", "--start", "-s", help="Trim start (e.g. 0:15 or 12). Blank = start."
    ),
    end: str = typer.Option(
        "", "--end", "-e", help="Trim end (e.g. 1:30 or 90). Blank = end."
    ),
    output: Path = typer.Option(
        Path.cwd(), "--output", "-o", help="Destination directory."
    ),
    no_fix: bool = typer.Option(
        False, "--no-fix", help="Skip the WhatsApp re-encode; keep raw file."
    ),
    keep_raw: bool = typer.Option(
        False, "--keep-raw", help="Keep the downloaded source alongside the fix."
    ),
    yes: bool = typer.Option(
        False, "--yes", "-y", help="Headless: no prompts, use flags/defaults."
    ),
) -> None:
    """Interactive yt-dlp downloader with a WhatsApp-safe re-encode."""
    for tool in ("yt-dlp", "ffmpeg", "ffprobe", "gum"):
        if shutil.which(tool) is None:
            print(f"Error: {tool} is required and was not found on PATH.")
            raise typer.Exit(1)

    headless = yes or not sys.stdin.isatty()

    if not url:
        if headless:
            fail("Error: a URL is required in headless mode.")
            raise typer.Exit(1)
        url = gum_input("Video URL", "https://youtube.com/watch?v=...")
    if not url:
        raise typer.Abort()

    kind = fmt.lower()
    if kind not in ("video", "audio"):
        kind = "video" if headless else gum_choose("Download", ["video", "audio"])

    if kind == "video":
        if resolution not in VIDEO_RESOLUTIONS:
            resolution = "720" if headless else gum_choose(
                "Max resolution", VIDEO_RESOLUTIONS
            )
    else:
        if audio_format not in AUDIO_FORMATS:
            audio_format = "mp3" if headless else gum_choose(
                "Audio format", AUDIO_FORMATS
            )

    if not headless and not start and not end and gum_confirm(
        "Trim a section?", default_yes=False
    ):
        start = gum_input("Start (blank = beginning)", "0:15")
        end = gum_input("End (blank = end)", "1:30")

    section = build_section(start, end)
    if section:
        info(f"Trimming section {start or 'start'} -> {end or 'end'}")

    output.mkdir(parents=True, exist_ok=True)

    info(f"Downloading {kind} from {url} ...")
    raw = download(url, kind, resolution or "best", audio_format, section)
    tmpdir = raw.parent

    if kind == "video" and not no_fix:
        info("Re-encoding for WhatsApp compatibility ...")
        final = unique(output / f"{raw.stem}.mp4")
        whatsapp_fix(raw, final)
        if keep_raw and raw.suffix.lower() != ".mp4":
            kept = unique(output / f"{raw.stem}.raw{raw.suffix}")
            shutil.move(str(raw), str(kept))
            info(f"Kept source: {kept}")
    else:
        final = unique(output / raw.name)
        shutil.move(str(raw), str(final))

    shutil.rmtree(tmpdir, ignore_errors=True)
    good(f"Done -> {final}")


if __name__ == "__main__":
    typer.run(main)
