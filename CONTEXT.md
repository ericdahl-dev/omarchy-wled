# omarchy-wled — Domain Context

## Purpose

Sync color from an [Omarchy](https://omarchy.org) desktop environment to a [WLED](https://kno.wled.ge) LED device in real time. Watches for theme or wallpaper changes and pushes the new color over HTTP.

## Domain Glossary

**Color Source** — anything that can produce an RGB color and describe what file to watch for changes. Three concrete sources exist: `accent` (theme accent key), `fg` (theme foreground key), `bg` (wallpaper average). Implemented via `ThemeColorSource` and `BgColorSource`.

**Accent** — the highlight color defined in the Omarchy theme's `colors.toml`. Typically vivid but may be muted depending on the theme; boosted to 1.2× saturation by default.

**Theme** — an Omarchy color scheme. Lives under `~/.config/omarchy/current/theme/`. A change is detected by watching `theme.name`, which is rewritten on theme switch.

**Wallpaper / Background** — the current desktop background image, symlinked at `~/.config/omarchy/current/background`. Average color is computed by resizing to 1×1 via Pillow (LANCZOS).

**WLED** — an open-source LED controller firmware. Accepts color via HTTP POST to `/json/state` with a JSON payload `{ on, bri, seg[0].col[0]: [r,g,b] }`.

**Saturation boost** — scaling the HSV saturation channel before sending. Accent defaults to 1.2×; fg/bg default to 1.0×. Overridable via `-s`.

**Sentinel** — a value that changes when the color source changes (e.g. file mtime, symlink target). Used by the poll fallback only — not part of the `ColorSource` protocol.

**Poll fallback** — when `watchdog` is not installed, `_poll()` runs a 1-second loop checking the sentinel value instead of using filesystem events.

**Seam** — the `ColorSource` protocol: `read()`, `watch_path()`, `is_trigger()`. Adding a new source means implementing this protocol only.

## File Layout

```
omarchy_wled.py         — all logic (single-file project)
test_omarchy_wled.py    — pytest suite
omarchy-wled@.service   — systemd user service template
PKGBUILD                — AUR package definition
pyproject.toml          — Python package metadata
```

## Key Paths (runtime)

```
~/.config/omarchy/current/theme/colors.toml   — accent/fg/bg hex values
~/.config/omarchy/current/theme.name          — rewritten on theme switch (watch trigger)
~/.config/omarchy/current/background          — symlink to current wallpaper image
```
