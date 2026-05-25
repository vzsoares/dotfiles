---
title: Audio — PipeWire/Spotify Fix
category: concept
updated: 2026-05-25
related: [fresh-install, overview]
---

# Audio — PipeWire/Spotify Fix

Fix for Spotify showing **"can't play this right now"** on Arch/Manjaro + i3, immediately after a fresh install.

## Cause

`pipewire` was running, but `wireplumber` (the session manager that creates audio **sinks**) and `pipewire-pulse` (the PulseAudio interface Spotify talks to) were missing. With no sink, Spotify has nothing to play to.

## Fix

```bash
sudo pacman -S --needed wireplumber pipewire-pulse pipewire-alsa libpulse
systemctl --user enable --now wireplumber pipewire pipewire-pulse
pkill spotify   # then relaunch Spotify
```

## Verify

```bash
pactl list short sinks   # should list a sink instead of "no server"
```

## Notes (fresh i3 setup)

- i3 has no audio applet by default. Install `pavucontrol` to pick the default output (handy with multiple cards): `sudo pacman -S pavucontrol`
- The user services auto-start on login via socket activation once enabled — one-time setup.

## See Also

- [[fresh-install]] — step 6 (Audio) of the new-machine bring-up links here
- [[overview]] — overall environment this fits into
