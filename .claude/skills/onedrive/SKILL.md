---
name: onedrive
description: Access the user's app-scoped OneDrive folder (Microsoft Graph AppFolder) on the company tenant. List, search, read inline, download, and upload files inside the app's auto-created /Apps/<app-name>/ folder. Auth is device-code flow. Use when the user mentions OneDrive, the company KB, the app folder, or wants to fetch/sync KB files from their company drive.
argument-hint: <setup|auth|whoami|ls|cat|get|put|mkdir|mv|rm|search> [args...]
allowed-tools: Bash, Read, Write
---

# OneDrive Skill (AppFolder)

Drives a single-file Python CLI (`onedrive.py`) that talks to **Microsoft Graph** with **device-code auth**. The Entra app uses the **`Files.ReadWrite.AppFolder`** scope, so every operation is scoped to a single auto-created folder on the user's OneDrive: `Documents/Apps/<app-display-name>/` (or `Documentos/Aplicativos/<app-display-name>/` on pt-BR locales). The app cannot see anything else on the drive.

The script runs with `uv run` (PEP 723 inline metadata — deps fetched on first run, cached afterwards).

## Files in this skill

- `SKILL.md` — this file.
- `onedrive.py` — the CLI. Run via `uv run /home/zizmackrok/.claude/skills/onedrive/onedrive.py <cmd>`.
- `reference.md` — Entra setup checklist, Graph endpoints, AppFolder mechanics, error decoding.

## When to use proactively

- User says "the AppFolder", "the company KB", "OneDrive KB", "company drive"
- User wants to read or sync a KB file
- User wants to publish a locally-generated doc into the AppFolder
- User asks to search KB content by file name

## When NOT to use

- Files anywhere else in the user's OneDrive — AppFolder scope cannot see them. Switch scope to `Files.ReadWrite` if needed (requires admin consent).
- SharePoint sites or Teams files — need `Sites.ReadWrite.All` scope and a different code path (`/sites/...` endpoints).
- A real multi-user shared KB — AppFolder is **per-user**. Each consenting user gets their own isolated folder. For a true team KB, use a SharePoint document library.

## Important: app display name = folder name

The folder on OneDrive is named after the **App's Display Name** in Entra. Set that intentionally before first auth (e.g. "Approva KB", not "App registration 1") because:
- It becomes user-visible in their OneDrive web UI
- Renaming the app later does **not** rename existing AppFolders that have already been created

## First-time setup (one-time per machine)

Required Entra app config (entra.microsoft.com → App registrations → your app):

1. **API permissions → Microsoft Graph → Delegated:** `Files.ReadWrite.AppFolder`, `User.Read`, `offline_access`
2. **Admin consent** — `Files.ReadWrite.AppFolder` is user-consentable in most tenants (the user can self-consent on first sign-in). If your tenant disables user consent globally, you'll still need an admin to grant it once. Failure mode: `AADSTS65001` on first auth.
3. **Authentication → "Allow public client flows" = Yes** — required for device code flow.
4. **App display name** — verify it's what you want users to see in their OneDrive (this becomes the folder name).

Then run, in order:

```bash
uv run /home/zizmackrok/.claude/skills/onedrive/onedrive.py setup
# prompts for tenant_id, client_id, default sub-folder (blank = AppFolder root)
# writes ~/.config/onedrive-cli/config.json (chmod 600)

uv run /home/zizmackrok/.claude/skills/onedrive/onedrive.py auth
# prints a URL + 9-char code; user opens URL, pastes code, signs in
# refresh token cached at ~/.cache/onedrive-cli/token.json (chmod 600)
# the AppFolder is auto-created on the first write (or first ls of root, in some tenants)
```

After this, all other commands re-use the cached refresh token silently.

## Daily commands

All paths are relative to the configured root **inside** the AppFolder. With no default sub-folder set, paths are relative to the AppFolder itself. Pass `--root some/sub` to override per-call.

| Command | Purpose | Notes |
|---|---|---|
| `ls [path]` | List folder contents | `--json` for structured output. Default: human table with kind/name/size/mtime. |
| `cat <path>` | Print a file's bytes to stdout | Best for small text files (.md, .txt, .json). Output is raw — pipe through tools as needed. |
| `get <remote> [local]` | Download to disk | Streams in 64 KiB chunks. Default local = basename in cwd. |
| `put <local> <remote>` | Upload, overwriting | Auto-uses upload session for files >4 MiB (chunked, 5 MiB chunks). Creates parent dirs as needed. |
| `mkdir [-p] <path>` | Create a folder | `-p` creates intermediate parents and succeeds if the leaf already exists. |
| `mv <src> <dst>` | Move or rename | Works at any depth — renames *and* cross-parent moves. Microsoft used to reject nested PATCH in AppFolder scope (the old "TOP-LEVEL only" rule); that no longer applies on current SPO. If you ever see 403 on a nested move, use the OneDrive web UI. |
| `rm [-r] <path>` | Delete a file (or `-r` folder) | Works at any depth. `-r` is **required** for folders (empty or not), because Graph deletes folder contents recursively without prompting. |
| `search <query>` | Fuzzy-search by name | Scoped to the AppFolder (server-side). `--json` available. |
| `whoami` | Print authenticated user | Sanity check after `auth`. |

## Examples

```bash
# Browse the AppFolder root
uv run ~/.claude/skills/onedrive/onedrive.py ls

# Drill in
uv run ~/.claude/skills/onedrive/onedrive.py ls "policies"

# Read a doc inline (great for piping into Claude's context)
uv run ~/.claude/skills/onedrive/onedrive.py cat "policies/onboarding.md"

# Seed the KB with a local file
uv run ~/.claude/skills/onedrive/onedrive.py put ./local-runbook.md "runbooks/local-runbook.md"

# Pull a binary
uv run ~/.claude/skills/onedrive/onedrive.py get "reports/2026-Q1.pdf" /tmp/q1.pdf

# Search KB
uv run ~/.claude/skills/onedrive/onedrive.py search "policy"

# Create folders (intermediates included)
uv run ~/.claude/skills/onedrive/onedrive.py mkdir -p "vendors/morada"

# Rename in place (works at any depth)
uv run ~/.claude/skills/onedrive/onedrive.py mv "policies/OLD_NAME.txt" "policies/new-name.txt"

# Move + rename across folders (any depth)
uv run ~/.claude/skills/onedrive/onedrive.py mv "Loose File.pdf" "archive/loose-file.pdf"
uv run ~/.claude/skills/onedrive/onedrive.py mv "partners/foo/setup.pdf" "vendors/foo/setup.pdf"

# Delete (folders need -r)
uv run ~/.claude/skills/onedrive/onedrive.py rm "drafts/stale.md"
uv run ~/.claude/skills/onedrive/onedrive.py rm -r "drafts/old-project"
```

## Common errors (quick triage)

- `AADSTS65001` → user consent missing. If your tenant blocks user consent, an admin needs to grant it for `Files.ReadWrite.AppFolder` (one-time, low-risk).
- `AADSTS50020 / 53003` → tenant conditional access. IT must whitelist or sign in from a compliant device.
- `401 Unauthorized` mid-session → token cache stale; re-run `auth --force`.
- `404 itemNotFound` → path wrong. Remember the AppFolder is the root; don't prefix `/Apps/<name>/`. Just say `policies/file.md`.
- `403 accessDenied` → trying to read/write a path that doesn't resolve inside the AppFolder. Check your `--root`/`default_root` config. May also appear on `mv`/`rm` of a nested item if your tenant still enforces the legacy AppFolder rule that blocked nested PATCH/DELETE — current SPO has lifted this for most tenants, but fall back to the OneDrive web UI if you hit it.
- `423 notAllowed (locked)` → the file is open in Office (desktop or web) or held by the OneDrive desktop sync client. Office acquires a co-authoring lock for the whole editing session, not just during saves; **retries don't help**. Ask the user to close the file everywhere, wait ~30s, then re-run. Read-only ops (`cat`, `get`) keep working — only mutating ops (`put` overwrite, `mv`) hit 423.
- `429 Too Many Requests` → throttling. Wait the `Retry-After` and re-run; the CLI does not auto-retry.

For deeper diagnosis (Graph endpoints, error envelope, large-file upload internals, scope expansion), read `reference.md`.

## Privacy / hygiene

- The skill folder is symlinked into the dotfiles repo and gets committed. **Never** put credentials in this folder.
- Config (`tenant_id`, `client_id`, `default_root`) lives at `~/.config/onedrive-cli/config.json` (mode 600).
- Token cache (refresh + access tokens) lives at `~/.cache/onedrive-cli/token.json` (mode 600).
- Both paths are explicitly outside the dotfiles repo.
- To revoke: delete `~/.cache/onedrive-cli/token.json`. To fully reset: also delete `~/.config/onedrive-cli/`.

## Notes on "company KB" intent

AppFolder is **per-user, isolated**. This skill builds a KB that lives in *your* OneDrive only — perfect for a personal KB synced to OneDrive that Claude can read/write across machines. It is **not** automatically shared with teammates.

If you later want a true multi-user company KB:
- Move to a SharePoint document library on a Teams site
- Add scope `Sites.ReadWrite.All` with admin consent
- Replace `/me/drive/special/approot` with `/sites/{site-id}/drive/root` in the script
The CLI structure (commands, auth, token cache) stays the same.
