---
title: Color Emoji Fonts (Notion, browsers)
category: concept
updated: 2026-05-26
related: [fresh-install, overview]
---

# Color Emoji Fonts (Notion, browsers)

Fix for **emoji rendering as tofu boxes / monochrome glyphs** in Notion, browsers, and other apps on a fresh Manjaro i3 install that ships with no color-emoji font.

## Symptom

Emoji show as `□` boxes or black-and-white outlines instead of color glyphs — most visibly in **Notion** (a Chromium/Electron app) and on web pages.

## Cause

Two independent issues, in order of importance:

1. **No emoji font installed.** A base Manjaro install has no color-emoji font, so `fc-match emoji` falls back to whatever Unicode-covering font sorts first — e.g. `Noto Znamenny Musical Notation` (a music-notation font, not emoji at all).
2. **Apps request emoji by vendor name.** Web/Electron CSS asks for `"Apple Color Emoji", "Segoe UI Emoji", ...` by name. Without fontconfig aliases mapping those names to an installed font, the request doesn't cleanly resolve.

## Fix

```bash
# 1. Install the color-emoji font (official 'extra' repo — NOT AUR/yay)
sudo pacman -S --needed noto-fonts-emoji

# 2. Add aliases so vendor emoji-font names resolve to Noto
sudo tee /etc/fonts/conf.d/75-noto-color-emoji.conf >/dev/null <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE fontconfig SYSTEM "fonts.dtd">
<fontconfig>
  <match target="pattern">
    <test qual="any" name="family"><string>emoji</string></test>
    <edit name="family" mode="assign" binding="same"><string>Noto Color Emoji</string></edit>
  </match>
  <match target="pattern">
    <test qual="any" name="family"><string>Apple Color Emoji</string></test>
    <edit name="family" mode="assign" binding="same"><string>Noto Color Emoji</string></edit>
  </match>
  <match target="pattern">
    <test qual="any" name="family"><string>Segoe UI Emoji</string></test>
    <edit name="family" mode="assign" binding="same"><string>Noto Color Emoji</string></edit>
  </match>
  <match target="pattern">
    <test qual="any" name="family"><string>Segoe UI Symbol</string></test>
    <edit name="family" mode="assign" binding="same"><string>Noto Color Emoji</string></edit>
  </match>
  <match target="pattern">
    <test qual="any" name="family"><string>Twemoji</string></test>
    <edit name="family" mode="assign" binding="same"><string>Noto Color Emoji</string></edit>
  </match>
  <match target="pattern">
    <test qual="any" name="family"><string>Symbola</string></test>
    <edit name="family" mode="assign" binding="same"><string>Noto Color Emoji</string></edit>
  </match>
</fontconfig>
EOF

# 3. Rebuild cache
fc-cache -f
```

Then **fully quit and reopen Notion / the browser** — they read fonts at startup, so a running instance won't pick up the change until relaunched.

## Verify

```bash
fc-match emoji                  # -> NotoColorEmoji.ttf: "Noto Color Emoji" "Regular"
fc-match "Apple Color Emoji"    # -> NotoColorEmoji.ttf  (alias works)
fc-match "Segoe UI Emoji"       # -> NotoColorEmoji.ttf
```

## Notes

- **Don't preemptively touch the bitmap-reject conf.** A common older tutorial (the r/archlinux guide this came from) blames `/etc/fonts/conf.d/70-no-bitmaps*.conf` for rejecting Noto Color Emoji (a CBDT *bitmap* font). On **fontconfig 2.17+** this is a non-issue: color emoji are flagged `scalable=true` and survive the reject. Confirm with `fc-match emoji` *after* installing the font — only if it still doesn't resolve should you swap the reject conf for the color-safe variant:
  ```bash
  sudo ln -sf /usr/share/fontconfig/conf.avail/70-no-bitmaps-except-emoji.conf \
              /etc/fonts/conf.d/70-no-bitmaps.conf && fc-cache -f
  ```
  (`70-no-bitmaps.conf` rejects all `scalable=false` fonts; `70-no-bitmaps-except-emoji.conf` additionally requires `outline=false`, so color emoji pass.)
- The generic `emoji` → Noto mapping and sans/serif/mono emoji *fallback* are already provided by fontconfig 2.17's `45-generic.conf` / `60-generic.conf`. The custom file above only adds the **vendor-name aliases** those defaults don't cover.
- The tutorial used `yay -S noto-fonts-emoji`, but the package is in the official `extra` repo — `pacman` is correct, no AUR needed.
- `/etc/fonts/conf.d/` is **not** in the dotfiles repo, so this fix is not version-controlled. To track it, add `75-noto-color-emoji.conf` to the repo and symlink it from a `link` script (same caveat as [[chrome-portal-file-picker-hidpi]]).

## See Also

- [[fresh-install]] — install color-emoji fonts as part of new-machine bring-up
- [[chrome-portal-file-picker-hidpi]] — another fontconfig/desktop-integration fix on the same setup
- [[overview]] — the i3/Manjaro environment this fits into
