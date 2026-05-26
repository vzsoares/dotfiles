---
title: Dark Mode for GTK/GNOME Apps
category: concept
updated: 2026-05-26
related: [fresh-install, chrome-portal-file-picker-hidpi, overview]
---

# Dark Mode for GTK/GNOME Apps

On i3 there's no GNOME settings daemon to set a system-wide color scheme, so GTK4/libadwaita apps (and portals) default to light. Set the `color-scheme` preference manually so apps that honor it render dark.

## Fix

```bash
gsettings set org.gnome.desktop.interface color-scheme 'prefer-dark'
```

This writes to dconf and persists across sessions. Apps that read `org.gnome.desktop.interface color-scheme` (GTK4/libadwaita, many Electron apps via the portal) switch to their dark variant.

## Verify

```bash
gsettings get org.gnome.desktop.interface color-scheme   # -> 'prefer-dark'
```

## Notes

- Older GTK3 apps may also need the legacy theme key: `gsettings set org.gnome.desktop.interface gtk-theme 'Adwaita-dark'`.
- The xdg-desktop-portal exposes this setting to sandboxed/Flatpak apps, so the same command often flips Flatpak app theming too.

## See Also

- [[chrome-portal-file-picker-hidpi]] — another portal/GTK quirk on i3 with no desktop environment
- [[fresh-install]] — new-machine bring-up where this is a useful post-install tweak
- [[overview]] — the i3 environment this applies to
