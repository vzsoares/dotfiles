#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#   "msal>=1.30",
#   "requests>=2.32",
# ]
# ///
# pyright: reportMissingImports=false
"""OneDrive CLI for the company tenant (Microsoft Graph, device-code auth).

First-time:
    uv run onedrive.py setup
    uv run onedrive.py auth

Daily:
    uv run onedrive.py ls [path]
    uv run onedrive.py cat <path>
    uv run onedrive.py get <remote> [local]
    uv run onedrive.py put <local> <remote>
    uv run onedrive.py search <query>
    uv run onedrive.py whoami

Config:    ~/.config/onedrive-cli/config.json   (tenant_id, client_id, default_root)
Token:     ~/.cache/onedrive-cli/token.json     (msal SerializableTokenCache)
"""
from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any
from urllib.parse import quote

import msal
import requests

CONFIG_DIR = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config")) / "onedrive-cli"
CACHE_DIR = Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache")) / "onedrive-cli"
CONFIG_PATH = CONFIG_DIR / "config.json"
TOKEN_CACHE_PATH = CACHE_DIR / "token.json"

GRAPH = "https://graph.microsoft.com/v1.0"
# offline_access is granted automatically for public client flows; do not list it
# in scopes — MSAL rejects "reserved" scopes.
SCOPES = ["Files.ReadWrite.AppFolder", "User.Read"]
# AppFolder scope: every Graph call is rooted at /me/drive/special/approot, which
# resolves to the app's auto-created /Apps/<app-display-name> folder. The app
# cannot see anything else in the user's OneDrive.
DRIVE_ROOT = "/me/drive/special/approot"
LARGE_FILE_THRESHOLD = 4 * 1024 * 1024  # 4 MiB — Graph small-upload limit
UPLOAD_CHUNK_SIZE = 5 * 1024 * 1024  # 5 MiB; Graph requires multiples of 320 KiB


# --------------------------------------------------------------------------- #
# config + token cache
# --------------------------------------------------------------------------- #


def load_config() -> dict[str, str]:
    if not CONFIG_PATH.exists():
        sys.exit(f"No config at {CONFIG_PATH}. Run `onedrive.py setup` first.")
    return json.loads(CONFIG_PATH.read_text())


def save_config(cfg: dict[str, str]) -> None:
    CONFIG_DIR.mkdir(parents=True, exist_ok=True)
    CONFIG_PATH.write_text(json.dumps(cfg, indent=2))
    CONFIG_PATH.chmod(0o600)


def load_cache() -> msal.SerializableTokenCache:
    cache = msal.SerializableTokenCache()
    if TOKEN_CACHE_PATH.exists():
        cache.deserialize(TOKEN_CACHE_PATH.read_text())
    return cache


def save_cache(cache: msal.SerializableTokenCache) -> None:
    if cache.has_state_changed:
        CACHE_DIR.mkdir(parents=True, exist_ok=True)
        TOKEN_CACHE_PATH.write_text(cache.serialize())
        TOKEN_CACHE_PATH.chmod(0o600)


def build_app(
    cfg: dict[str, str], cache: msal.SerializableTokenCache
) -> msal.PublicClientApplication:
    return msal.PublicClientApplication(
        client_id=cfg["client_id"],
        authority=f"https://login.microsoftonline.com/{cfg['tenant_id']}",
        token_cache=cache,
    )


_TOKEN_CACHE: dict[str, str] = {}


def get_token(force_interactive: bool = False) -> str:
    """Return a valid access token, doing device-code flow only when needed.

    Result is memoized within a single CLI invocation so commands that issue
    multiple Graph requests don't re-derive the token each call.
    """
    if not force_interactive and "access" in _TOKEN_CACHE:
        return _TOKEN_CACHE["access"]

    cfg = load_config()
    cache = load_cache()
    pca = build_app(cfg, cache)

    result: dict[str, Any] | None = None
    accounts = pca.get_accounts()

    if accounts and not force_interactive:
        result = pca.acquire_token_silent(SCOPES, account=accounts[0])

    if not result:
        flow = pca.initiate_device_flow(scopes=SCOPES)
        if "user_code" not in flow:
            sys.exit(f"Failed to start device flow:\n{json.dumps(flow, indent=2)}")
        # Always print the device-code message to stderr so it shows up even
        # when stdout is being captured/piped.
        print(flow["message"], file=sys.stderr, flush=True)
        result = pca.acquire_token_by_device_flow(flow)

    save_cache(cache)

    if not result or "access_token" not in result:
        err = (result or {}).get("error", "unknown")
        desc = (result or {}).get("error_description", "")
        sys.exit(f"Auth failed ({err}): {desc}")

    _TOKEN_CACHE["access"] = result["access_token"]
    return result["access_token"]


# --------------------------------------------------------------------------- #
# graph helpers
# --------------------------------------------------------------------------- #


def graph_headers(extra: dict[str, str] | None = None) -> dict[str, str]:
    h = {"Authorization": f"Bearer {get_token()}"}
    if extra:
        h.update(extra)
    return h


def graph_get(path: str, **kwargs: Any) -> requests.Response:
    return requests.get(f"{GRAPH}{path}", headers=graph_headers(), **kwargs)


def resolve_path(user_path: str | None, root: str | None) -> str:
    """Combine root with the user-provided path. Returns a clean POSIX-style
    path (no leading/trailing slash). Empty string means drive root."""
    parts: list[str] = []
    if root and root != "/":
        parts.append(root.strip("/"))
    if user_path:
        parts.append(user_path.strip("/"))
    return "/".join(p for p in parts if p)


def item_url(remote_path: str) -> str:
    """Build path-based addressing rooted at the AppFolder.

    Empty path returns the approot itself (folder metadata/children).
    The path component is URL-encoded but `/` is preserved so subdirs work.
    """
    if not remote_path:
        return DRIVE_ROOT
    return f"{DRIVE_ROOT}:/{quote(remote_path, safe='/')}:"


def die_on_error(r: requests.Response, prefix: str) -> None:
    if r.status_code >= 400:
        # Graph errors are JSON: {"error": {"code": "...", "message": "..."}}
        try:
            err = r.json().get("error", {})
            msg = f"{err.get('code', 'unknown')}: {err.get('message', '')}"
        except ValueError:
            msg = r.text[:500]
        sys.exit(f"{prefix} ({r.status_code}): {msg}")


# --------------------------------------------------------------------------- #
# commands
# --------------------------------------------------------------------------- #


def cmd_setup(_args: argparse.Namespace) -> None:
    del _args  # subparser dispatch passes args; setup ignores it
    print("Entra app values (entra.microsoft.com → App registrations → your app → Overview):")
    tenant = input("Directory (tenant) ID: ").strip()
    client = input("Application (client) ID: ").strip()
    default_root = input("Default sub-folder inside AppFolder (blank = AppFolder root): ").strip()
    if not tenant or not client:
        sys.exit("tenant_id and client_id are required.")
    save_config(
        {"tenant_id": tenant, "client_id": client, "default_root": default_root}
    )
    print(f"Saved {CONFIG_PATH}")
    print("Next: `uv run onedrive.py auth`")


def cmd_auth(args: argparse.Namespace) -> None:
    token = get_token(force_interactive=args.force)
    r = requests.get(f"{GRAPH}/me", headers={"Authorization": f"Bearer {token}"})
    die_on_error(r, "auth probe failed")
    me = r.json()
    print(f"Authenticated as {me.get('userPrincipalName')} ({me.get('displayName')})")


def cmd_whoami(args: argparse.Namespace) -> None:
    cmd_auth(args)


def _root_for(args: argparse.Namespace) -> str:
    cfg = load_config()
    if args.root is not None:
        return args.root
    return cfg.get("default_root", "")


def cmd_ls(args: argparse.Namespace) -> None:
    root = _root_for(args)
    full = resolve_path(args.path, root)
    children_path = f"{item_url(full)}/children"
    r = graph_get(
        children_path,
        params={
            "$top": 200,
            "$select": "name,size,folder,file,lastModifiedDateTime,webUrl",
        },
    )
    die_on_error(r, "ls failed")
    items = r.json().get("value", [])

    if args.json:
        print(json.dumps(items, indent=2))
        return

    label = full or "AppFolder root"
    if not items:
        print(f"(empty: {label})")
        return

    name_w = max(len(i["name"]) for i in items)
    print(f"# {label}")
    for i in items:
        if "folder" in i:
            kind = "DIR "
            size_h = f"{i['folder'].get('childCount', 0)} items"
        else:
            kind = "FILE"
            size_h = human_size(i.get("size", 0))
        mtime = i.get("lastModifiedDateTime", "")[:19].replace("T", " ")
        print(f"{kind}  {i['name']:<{name_w}}  {size_h:>12}  {mtime}")


def _download_url(remote_path: str) -> str:
    """Resolve a pre-authenticated downloadUrl for a file at remote_path.

    Required for nested files in SPO-backed OneDrive AppFolder, where direct
    `:/{path}:/content` returns 400. We list the parent and pull the
    `@microsoft.graph.downloadUrl` (pre-signed, short-lived).
    """
    parent, _, name = remote_path.rpartition("/")
    children_url = f"{item_url(parent)}/children" if parent else f"{DRIVE_ROOT}/children"
    r = graph_get(children_url, params={"$top": 1000})
    die_on_error(r, f"lookup failed for parent of {remote_path}")
    for item in r.json().get("value", []):
        if item.get("name") == name:
            url = item.get("@microsoft.graph.downloadUrl")
            if not url:
                sys.exit(f"{remote_path}: no downloadUrl (folder, not file?)")
            return url
    sys.exit(f"not found: {remote_path}")


def _fetch_content(remote_path: str, stream: bool = False) -> requests.Response:
    """Get file bytes. Tries direct content URL first, falls back to downloadUrl
    on 400 (the SPO-backed AppFolder quirk for nested paths)."""
    r = graph_get(f"{item_url(remote_path)}/content", allow_redirects=True, stream=stream)
    if r.status_code == 400:
        # SPO quirk: nested paths bug out. Use pre-signed downloadUrl.
        url = _download_url(remote_path)
        r = requests.get(url, stream=stream)  # no auth header — URL is pre-signed
    return r


def cmd_cat(args: argparse.Namespace) -> None:
    root = _root_for(args)
    full = resolve_path(args.path, root)
    if not full:
        sys.exit("cat: need a file path")
    r = _fetch_content(full)
    die_on_error(r, "cat failed")
    sys.stdout.buffer.write(r.content)


def cmd_get(args: argparse.Namespace) -> None:
    root = _root_for(args)
    full = resolve_path(args.remote, root)
    local = Path(args.local) if args.local else Path(Path(args.remote).name)
    r = _fetch_content(full, stream=True)
    die_on_error(r, "get failed")
    with local.open("wb") as fh:
        for chunk in r.iter_content(chunk_size=64 * 1024):
            if chunk:
                fh.write(chunk)
    print(f"Downloaded {full} -> {local} ({human_size(local.stat().st_size)})")


def cmd_mkdir(args: argparse.Namespace) -> None:
    root = _root_for(args)
    full = resolve_path(args.path, root)
    if not full:
        sys.exit("mkdir: need a path")
    parts = full.split("/")
    if args.parents:
        for i in range(1, len(parts) + 1):
            _mkdir_one("/".join(parts[:i]), ignore_exists=True)
    else:
        _mkdir_one(full, ignore_exists=False)
    print(f"mkdir: {full}")


def _mkdir_one(path: str, ignore_exists: bool) -> None:
    parent, _, name = path.rpartition("/")
    children_url = (
        f"{GRAPH}{item_url(parent)}/children"
        if parent
        else f"{GRAPH}{DRIVE_ROOT}/children"
    )
    body = {
        "name": name,
        "folder": {},
        "@microsoft.graph.conflictBehavior": "fail",
    }
    r = requests.post(
        children_url,
        headers=graph_headers({"Content-Type": "application/json"}),
        json=body,
    )
    if r.status_code == 409 and ignore_exists:
        return
    die_on_error(r, f"mkdir failed: {path}")


def cmd_mv(args: argparse.Namespace) -> None:
    """Move/rename a TOP-LEVEL item.

    SPO-backed OneDrive AppFolder scope blocks PATCH on nested items even
    when they belong to the AppFolder. For nested moves/renames, use the
    OneDrive web UI.
    """
    root = _root_for(args)
    src = resolve_path(args.src, root)
    dst = resolve_path(args.dst, root)
    if not src or not dst:
        sys.exit("mv: need src and dst")

    # Refuse nested sources — Graph returns 403/404 in this scope.
    if "/" in src:
        sys.exit(
            f"mv: nested source '{src}' not supported under Files.ReadWrite.AppFolder. "
            "Move/rename via OneDrive web UI, or move the top-level ancestor."
        )

    dst_parent, _, dst_name = dst.rpartition("/")
    body: dict[str, Any] = {"name": dst_name}
    if dst_parent:
        # Resolve destination parent's item id
        r = graph_get(f"{item_url(dst_parent)}", params={"$select": "id"})
        die_on_error(r, f"mv: cannot resolve dst parent '{dst_parent}'")
        body["parentReference"] = {"id": r.json()["id"]}

    r = requests.patch(
        f"{GRAPH}{item_url(src)}",
        headers=graph_headers({"Content-Type": "application/json"}),
        json=body,
    )
    die_on_error(r, f"mv failed ({src} -> {dst})")
    print(f"Moved {src} -> {dst}")


def cmd_put(args: argparse.Namespace) -> None:
    root = _root_for(args)
    full = resolve_path(args.remote, root)
    local = Path(args.local)
    if not local.is_file():
        sys.exit(f"put: not a file: {local}")
    size = local.stat().st_size
    if size <= LARGE_FILE_THRESHOLD:
        upload_small(full, local)
    else:
        upload_large(full, local, size)
    print(f"Uploaded {local} -> {full} ({human_size(size)})")


def upload_small(remote: str, local: Path) -> None:
    r = requests.put(
        f"{GRAPH}{item_url(remote)}/content",
        headers=graph_headers({"Content-Type": "application/octet-stream"}),
        data=local.read_bytes(),
    )
    die_on_error(r, "upload failed")


def upload_large(remote: str, local: Path, size: int) -> None:
    body = {"item": {"@microsoft.graph.conflictBehavior": "replace"}}
    r = requests.post(
        f"{GRAPH}{item_url(remote)}/createUploadSession",
        headers=graph_headers({"Content-Type": "application/json"}),
        json=body,
    )
    die_on_error(r, "createUploadSession failed")
    upload_url = r.json()["uploadUrl"]

    # The upload-session URL is pre-authenticated; do NOT send the bearer token
    # on chunk PUTs (Graph rejects it).
    with local.open("rb") as fh:
        offset = 0
        while offset < size:
            chunk = fh.read(UPLOAD_CHUNK_SIZE)
            if not chunk:
                break
            end = offset + len(chunk) - 1
            pr = requests.put(
                upload_url,
                headers={
                    "Content-Length": str(len(chunk)),
                    "Content-Range": f"bytes {offset}-{end}/{size}",
                },
                data=chunk,
            )
            die_on_error(pr, f"chunk upload failed at offset {offset}")
            offset = end + 1


def cmd_search(args: argparse.Namespace) -> None:
    root = _root_for(args)
    # AppFolder scope: search() on a driveItem is scoped to its descendants,
    # so calling it on approot already restricts results to the AppFolder.
    q_escaped = args.query.replace("'", "''")
    base = item_url(root) if root else DRIVE_ROOT
    r = graph_get(
        f"{base}/search(q='{quote(q_escaped, safe=chr(39))}')",
        params={"$top": 100},
    )
    die_on_error(r, "search failed")
    items = r.json().get("value", [])

    if args.json:
        print(json.dumps(items, indent=2))
        return

    if not items:
        print(f"(no matches for '{args.query}' under {root or 'AppFolder root'})")
        return

    for i in items:
        # parentReference.path looks like "/drive/root:/Apps/<name>/sub"
        parent = i.get("parentReference", {}).get("path", "").split(":", 1)[-1]
        kind = "DIR " if "folder" in i else "FILE"
        print(f"{kind}  {parent}/{i['name']}")


# --------------------------------------------------------------------------- #
# misc
# --------------------------------------------------------------------------- #


def human_size(n: float) -> str:
    for unit in ("B", "KiB", "MiB", "GiB", "TiB"):
        if n < 1024:
            return f"{int(n)}{unit}" if unit == "B" else f"{n:.1f}{unit}"
        n /= 1024
    return f"{n:.1f}PiB"


def main() -> None:
    p = argparse.ArgumentParser(
        description="OneDrive CLI (Microsoft Graph, device-code auth).",
    )
    sub = p.add_subparsers(dest="cmd", required=True)

    sp = sub.add_parser("setup", help="Write config (tenant_id, client_id, default_root).")
    sp.set_defaults(func=cmd_setup)

    sp = sub.add_parser("auth", help="Run device-code login (or refresh from cache).")
    sp.add_argument("--force", action="store_true", help="Force a fresh device-code login.")
    sp.set_defaults(func=cmd_auth)

    sp = sub.add_parser("whoami", help="Show the authenticated user.")
    sp.add_argument("--force", action="store_true")
    sp.set_defaults(func=cmd_whoami)

    sp = sub.add_parser("ls", help="List a folder's contents.")
    sp.add_argument("path", nargs="?", default="")
    sp.add_argument("--root", help="Override the configured default root.")
    sp.add_argument("--json", action="store_true")
    sp.set_defaults(func=cmd_ls)

    sp = sub.add_parser("cat", help="Print a file's bytes to stdout.")
    sp.add_argument("path")
    sp.add_argument("--root")
    sp.set_defaults(func=cmd_cat)

    sp = sub.add_parser("get", help="Download a file.")
    sp.add_argument("remote")
    sp.add_argument("local", nargs="?")
    sp.add_argument("--root")
    sp.set_defaults(func=cmd_get)

    sp = sub.add_parser("put", help="Upload a file (overwrites if it exists).")
    sp.add_argument("local")
    sp.add_argument("remote")
    sp.add_argument("--root")
    sp.set_defaults(func=cmd_put)

    sp = sub.add_parser("search", help="Search file names under the root.")
    sp.add_argument("query")
    sp.add_argument("--root")
    sp.add_argument("--json", action="store_true")
    sp.set_defaults(func=cmd_search)

    sp = sub.add_parser("mkdir", help="Create a folder (parents created via -p).")
    sp.add_argument("path")
    sp.add_argument("--root")
    sp.add_argument("-p", "--parents", action="store_true",
                    help="Create parent folders as needed; succeed if they exist.")
    sp.set_defaults(func=cmd_mkdir)

    sp = sub.add_parser("mv", help="Move/rename a TOP-LEVEL item (nested moves blocked by AppFolder scope).")
    sp.add_argument("src")
    sp.add_argument("dst")
    sp.add_argument("--root")
    sp.set_defaults(func=cmd_mv)

    args = p.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
