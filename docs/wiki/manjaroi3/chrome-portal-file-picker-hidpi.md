---
title: Chrome File Picker — Portal HiDPI Fix
category: concept
updated: 2026-05-25
related: [fresh-install, overview]
---

# Chrome File Picker — Portal HiDPI Fix

Fix for Chrome's **file picker rendering as an oversized, seemingly-blank window** (only the theme background visible, content pushed off-screen) on Manjaro + i3 with a 4K display.

## Symptom

Opening a file dialog in Chrome (Ctrl+O, uploads, etc.) shows a huge window split into two flat colour panels with no widgets — or a stray half-cut icon at the top. The dialog is not blank, it is **scaled too large** so the content sits beyond the screen edges.

## Cause

HiDPI scaling is configured in `~/.profile` as `GDK_SCALE=2` + `GDK_DPI_SCALE=0.5` (with `Xft.dpi: 192` from X resources). But `~/.profile` only reaches apps that **i3 launches directly**.

Chrome's file dialog is drawn by **`xdg-desktop-portal-gtk`** — a separate **dbus/systemd-activated user service**, not Chrome's own GTK. That service never inherits `~/.profile`, so it sees only `Xft.dpi=192` (2× fonts) with no `GDK_DPI_SCALE=0.5` compensation → an oversized dialog.

Key point: the renderer is a *different process* from Chrome, so Chrome flags/env (`--gtk-version`, `--force-device-scale-factor`, stripping `GDK_SCALE` from Chrome, portal feature toggles) **do not affect it**.

## Fix

Give systemd-activated services the same scaling recipe, in the file systemd reads *before* starting any user service:

```ini
# ~/.config/environment.d/hidpi.conf
GDK_SCALE=2
GDK_DPI_SCALE=0.5
```

Takes effect on next login (the portal picks it up at activation). To apply immediately without relogging:

```bash
systemctl --user import-environment GDK_SCALE GDK_DPI_SCALE
systemctl --user restart xdg-desktop-portal-gtk
```

## Verify

```bash
# scaling env reaches the portal process
tr '\0' '\n' < /proc/$(pgrep -f xdg-desktop-portal-gtk | head -1)/environ | grep GDK
# -> GDK_SCALE=2  /  GDK_DPI_SCALE=0.5
```

Then open a Chrome file picker — it should be correctly sized.

## Notes

- `~/.config/environment.d/` is **not** in the dotfiles repo, so this fix is not version-controlled / portable. To track it, add the file to the repo and symlink it from a `link` script.
- An alternative (repo-tracked) mechanism is an i3 `exec --no-startup-id dbus-update-activation-environment --systemd GDK_SCALE GDK_DPI_SCALE`, but it can lose a race if the portal is already activated at login — `environment.d` is more reliable.
- Debugging dead-ends that wasted time (avoid next time): it is **not** the portal's `DISPLAY` env (already `:0`), **not** `GDK_SCALE` on the Chrome process, **not** the GTK4 GSK GL renderer / NVIDIA driver (the portal links **GTK3**), and **not** `--gtk-version=3`. When a portal GTK dialog renders wrong, inspect the **portal process's own `/proc/<pid>/environ`** first.

## See Also

- [[fresh-install]] — HiDPI scaling is set up as part of new-machine bring-up
- [[overview]] — the i3/Manjaro environment this fits into
