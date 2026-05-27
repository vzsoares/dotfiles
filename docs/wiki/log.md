# Wiki Log

## [2026-05-25] bootstrap + ingest | Project overview & fresh-install guide
Bootstrapped the wiki (index.md, log.md). Ingested a project overview from README, link scripts, .zshrc, mise config, and submodules. Created a fresh Manjaro i3 install guide and folded in the user-provided Spotify/PipeWire audio fix as a dedicated, cross-linked page. Noted two source discrepancies: README lists `packer` but nvim uses lazy.nvim; clone path is load-bearing (`~/code/personal/dotfiles`).
Pages created: overview.md, guides/fresh-install.md, guides/audio-pipewire-spotify.md, index.md, log.md

## [2026-05-25] reorg | Nest install guides under manjaroi3/
Moved fresh-install.md and audio-pipewire-spotify.md from guides/ into manjaroi3/. overview.md kept at wiki root. Wiki-links unchanged (filename-based); index "Guides" section relabeled "Manjaro i3".
Pages moved: manjaroi3/fresh-install.md, manjaroi3/audio-pipewire-spotify.md

## [2026-05-25] ingest | Chrome portal file-picker HiDPI fix
Documented the oversized/"blank" Chrome file picker bug: the dialog is drawn by the dbus/systemd-activated xdg-desktop-portal-gtk, which doesn't inherit GDK_SCALE/GDK_DPI_SCALE from ~/.profile. Fix is ~/.config/environment.d/hidpi.conf. Recorded the debugging dead-ends (not DISPLAY, not Chrome flags, not GTK4 GL/NVIDIA — portal is GTK3) so they aren't repeated.
Pages created: manjaroi3/chrome-portal-file-picker-hidpi.md

## [2026-05-26] ingest | Dark mode for GTK/GNOME apps on i3
Saved the `gsettings set org.gnome.desktop.interface color-scheme 'prefer-dark'` command. On i3 there's no settings daemon, so GTK4/libadwaita apps default to light; this dconf key flips them dark. Noted the legacy `gtk-theme` key for GTK3 apps and that the portal exposes the setting to Flatpaks.
Pages created: manjaroi3/dark-mode-gtk-apps.md

## [2026-05-26] ingest | Color emoji fonts (Notion/browsers)
Documented enabling color emoji: install `noto-fonts-emoji` (extra repo, not yay) + a `75-noto-color-emoji.conf` aliasing Apple/Segoe/Twemoji names to Noto. Verified live on this machine (`fc-match emoji` was falling back to Noto Znamenny Musical Notation). Recorded that fontconfig 2.17 makes the old `70-no-bitmaps.conf` panic unnecessary — color emoji are flagged scalable and survive the reject; only swap to the except-emoji variant if `fc-match emoji` still fails after install.
Pages created: manjaroi3/color-emoji-fonts.md
