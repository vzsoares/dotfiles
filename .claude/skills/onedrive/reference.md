# OneDrive Skill — Reference

Deep-dive notes for the `onedrive` skill. Read when troubleshooting auth failures, expanding scopes, or modifying the script.

## 1. Entra app registration — the full checklist

Portal: <https://entra.microsoft.com> → **App registrations** → your app.

### Overview tab — record these
- **Application (client) ID** — used as `client_id` in config
- **Directory (tenant) ID** — used as `tenant_id` in config (a real tenant GUID, **not** `common`/`organizations`, since this is a single-tenant corporate app)

### Authentication tab
- **Allowed account types**: "Accounts in this organizational directory only (Single tenant)"
- **Advanced settings → Allow public client flows**: **Yes** ← required for device code
- No platform/redirect URI needed for device code flow.

### API permissions tab
Add **Microsoft Graph → Delegated permissions**:

| Permission | Why |
|---|---|
| `Files.ReadWrite.AppFolder` | Read + write **only** the app's auto-created folder under the user's OneDrive (`/me/drive/special/approot`). The app cannot see the rest of the drive. |
| `User.Read` | Resolve `/me` for the `whoami` probe |
| `offline_access` | Issue a refresh token so the cache survives across sessions (without this, every run = device code) |

`Files.ReadWrite.AppFolder` is **user-consentable** in tenants with default consent settings — the user can self-consent on first sign-in, no admin involvement. If the tenant has globally disabled user consent ("Users → User settings → Users can consent to apps = No" in Entra), an admin still needs to grant it once. Without consent, first auth fails with `AADSTS65001`.

### Display name (this matters!)
The AppFolder is named after the **app's Display Name** when first created. Whatever shows on the app's Overview tab becomes the folder name in OneDrive (`Documents/Apps/<display-name>/`). Pick a clean, user-facing name (e.g. "Approva KB") **before** first auth. Renaming the app later does **not** rename existing AppFolders.

### Certificates & secrets tab
**Skip.** Public-client / device-code flows do not use a secret. Adding one does no harm, but it's not used.

### Expose an API / API roles
**Skip.** Not needed.

## 2. Auth flow — what actually happens

```
┌──────────────┐
│ uv run auth  │
└──────┬───────┘
       │ MSAL.PublicClientApplication.acquire_token_silent
       │   reads ~/.cache/onedrive-cli/token.json (SerializableTokenCache)
       │   if a valid AT exists → return it
       │   else if a refresh token exists → POST /token, swap for new AT, save
       │   else (or both expired) → fall through
       ▼
       MSAL.initiate_device_flow
       → POST https://login.microsoftonline.com/<tenant>/oauth2/v2.0/devicecode
       ← { device_code, user_code, verification_uri, message, ... }
       ▼
       Print the message ("To sign in, open https://microsoft.com/devicelogin and enter code XXXX-XXXX")
       ▼
       MSAL.acquire_token_by_device_flow
       → polls /token every 5s until the user signs in or the code expires
       ← { access_token, refresh_token, id_token, expires_in: 3600, ... }
       ▼
       cache.has_state_changed → write token.json (chmod 600)
```

After the first device-code completion, every subsequent run uses `acquire_token_silent` and is fully non-interactive. Refresh tokens for AAD work-or-school accounts are valid for **90 days of inactivity**; conditional access policies can shorten that.

## 3. Microsoft Graph endpoints used

Base: `https://graph.microsoft.com/v1.0`. All file ops are rooted at `/me/drive/special/approot`, which resolves the AppFolder by alias (no need to remember an item ID).

| Op | Endpoint | Notes |
|---|---|---|
| whoami | `GET /me` | Returns `userPrincipalName`, `displayName`, `id`, etc. |
| ls (root) | `GET /me/drive/special/approot/children` | Lists AppFolder root |
| ls (sub) | `GET /me/drive/special/approot:/{path}:/children` | Lists a sub-folder |
| cat / get | `GET /me/drive/special/approot:/{path}:/content` | Returns 302 to a pre-authenticated CDN download URL. `requests` follows the redirect; the bearer token is stripped on cross-origin redirect (intended). |
| put (small) | `PUT /me/drive/special/approot:/{path}:/content` | Body = raw bytes. Works up to 4 MiB. Auto-creates the file and any missing parent folders. |
| put (large) | `POST /me/drive/special/approot:/{path}:/createUploadSession` then chunked `PUT` to the returned `uploadUrl` | Chunks must be multiples of 320 KiB except the last. Skill uses 5 MiB. The upload URL is its own auth — do **not** send the bearer token on chunk PUTs. |
| search | `GET /me/drive/special/approot/search(q='{query}')` | `search()` is a function on a driveItem and is scoped to its descendants — calling it on approot already restricts results to the AppFolder, no client-side filtering needed. |

### The `/me/drive/special/approot:/{path}:` addressing
`/me/drive/special/approot` is a Graph alias that resolves to the app's auto-created folder driveItem. After it, the same `:/{path}:` path-based syntax used elsewhere applies. The trailing `:` is required as a separator before any sub-resource (`/children`, `/content`, etc.). Path components are URL-encoded but `/` is preserved (script uses `quote(path, safe='/')`).

The first call that performs a write (or in some tenants, even the first read of children) triggers Graph to create the AppFolder under `/Apps/<app-display-name>` in the user's OneDrive. The folder is visible in the OneDrive web UI; the user can drop files into it directly and the app will see them.

## 4. Error decoding

### Auth-time errors (during device flow)

| Code | Meaning | Fix |
|---|---|---|
| `AADSTS65001` | Admin consent missing | Tenant admin clicks "Grant admin consent" on the API permissions page |
| `AADSTS70011` | Invalid scope | Don't pass `offline_access` explicitly to MSAL — it's reserved/auto |
| `AADSTS50020` / `53003` / `530003` | Conditional access blocked | IT must whitelist or sign in from compliant device |
| `AADSTS700016` | Application not found in tenant | Wrong `client_id` for this `tenant_id` |
| `AADSTS900023` | Tenant identifier invalid | Wrong `tenant_id` (use the directory GUID, not the domain) |

### Graph errors (after auth)

| HTTP | Code | Meaning / fix |
|---|---|---|
| 401 | `InvalidAuthenticationToken` | Token expired mid-run (rare). `auth --force` to refresh. |
| 403 | `accessDenied` | Scope mismatch. Confirm `Files.ReadWrite` is granted + consented. |
| 404 | `itemNotFound` | Path wrong. Remember the configured root is prepended — pass `--root /` to address absolute. |
| 409 | `nameAlreadyExists` | Only on small upload without `conflictBehavior`. The large path uses `replace`; small path overwrites by default — should not happen. |
| 423 | `resourceLocked` | File is checked out / open in another client. Wait or close it. |
| 429 | `activityLimitReached` | Throttling. Honour the `Retry-After` header. The CLI does not auto-retry. |
| 507 | `quotaLimitReached` | OneDrive full. |

The Graph error envelope:
```json
{ "error": { "code": "itemNotFound", "message": "...", "innerError": {...} } }
```
The CLI's `die_on_error` parses this for friendly messages.

## 5. Token cache layout

`~/.cache/onedrive-cli/token.json` (mode 600). MSAL `SerializableTokenCache` produces JSON with:

```
{
  "AccessToken":   { "<key>": { "secret": "<JWT>", "expires_on": "...", ... } },
  "RefreshToken":  { "<key>": { "secret": "...", "family_id": "1", ... } },
  "IdToken":       { "<key>": { "secret": "<JWT>", ... } },
  "Account":       { "<key>": { "username": "...", "home_account_id": "...", ... } },
  "AppMetadata":   { "<key>": { ... } }
}
```

The refresh token is what makes subsequent runs silent. **Never commit this file.** To revoke: delete the file (and optionally revoke from <https://myapps.microsoft.com>).

## 6. Common extensions (not yet implemented)

### Expand to the user's full OneDrive
Replace `Files.ReadWrite.AppFolder` with `Files.ReadWrite` (admin consent typically needed). Change `DRIVE_ROOT` constant from `/me/drive/special/approot` to `/me/drive/root`. Now the script sees the whole user OneDrive instead of just the AppFolder.

### Shared / multi-user company KB (SharePoint)
This is the right path if you want a true team KB.
1. Add scope `Sites.ReadWrite.All` with admin consent.
2. Replace `DRIVE_ROOT` with `/sites/{site-id}/drive/root` for a specific Teams/SharePoint site library, or `/sites/{site-id}/lists/{list-id}/items` for list-based content.
3. Site IDs come from `GET /sites/{hostname}:/sites/{site-path}` — e.g. `GET /sites/approva.sharepoint.com:/sites/kb`.
4. The CLI structure (commands, auth, token cache) stays the same; only the constant changes.

### Other users' drives / shared-with-me items
Add `Files.Read.All` (admin consent). Use `/users/{upn-or-id}/drive/root:/{path}` or `/me/drive/sharedWithMe` (different shape — items have `remoteItem` references that point to the original drive).

### Delta sync
`GET /me/drive/root/delta` returns a token; subsequent calls with that token return only changed items. Useful for a local mirror.

### Batch requests
`POST /$batch` runs up to 20 requests in parallel inside one HTTP call. Useful for "fetch 50 file metadata at once."

### Webhooks (subscriptions)
`POST /subscriptions` with `resource: "/me/drive/root"` registers a webhook for change notifications. Needs a public HTTPS endpoint.

## 7. uv / PEP 723 notes

The script's header:
```python
# /// script
# requires-python = ">=3.11"
# dependencies = ["msal>=1.30", "requests>=2.32"]
# ///
```

`uv run script.py` reads this, creates an isolated environment in `~/.cache/uv/`, installs the deps, and executes. First run fetches `msal` + `requests` + transitive (~15s); subsequent runs are ~200ms.

The shebang `#!/usr/bin/env -S uv run --script` makes `./onedrive.py ...` work directly (after `chmod +x`), but the skill always uses the explicit `uv run` form so behavior is independent of the executable bit.

## 8. Files this skill touches

| Path | Purpose | In dotfiles repo? |
|---|---|---|
| `~/.claude/skills/onedrive/SKILL.md` | Skill instructions | **Yes** (symlinked) |
| `~/.claude/skills/onedrive/onedrive.py` | The CLI | **Yes** (symlinked) |
| `~/.claude/skills/onedrive/reference.md` | This file | **Yes** (symlinked) |
| `~/.config/onedrive-cli/config.json` | tenant_id, client_id, default_root | **No** — explicitly outside repo |
| `~/.cache/onedrive-cli/token.json` | MSAL token cache | **No** — explicitly outside repo |

Resetting:
- Re-auth only: `rm ~/.cache/onedrive-cli/token.json` then `uv run onedrive.py auth`
- Full reset: `rm -rf ~/.config/onedrive-cli ~/.cache/onedrive-cli`
