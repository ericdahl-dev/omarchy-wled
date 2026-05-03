# omarchy-wled — Domain Context

## Purpose

Sync color from an [Omarchy](https://omarchy.org) desktop environment to a [WLED](https://kno.wled.ge) LED device in real time. Watches for theme or wallpaper changes and pushes the new color over HTTP.

## Domain Glossary

**Color Source** — anything that can produce an RGB color and describe what file to watch for changes. Three concrete sources exist: `accent` (theme accent key), `fg` / `foreground` (same — maps to the **`foreground`** key in `colors.toml`, Omarchy’s UI/font color), `bg` (wallpaper average). Implemented via `ThemeColorSource` and `BgColorSource`.

**Accent** — the highlight color defined in the Omarchy theme's `colors.toml`. Typically vivid but may be muted depending on the theme; boosted to 1.2× saturation by default.

**Theme** — an Omarchy color scheme. Lives under `~/.config/omarchy/current/theme/`. Accent/fg sources watch `~/.config/omarchy/current/` for writes to **`theme.name`** (theme switch) and **`theme/colors.toml`** (color edits), so updates are not missed when only one file changes.

**Wallpaper / Background** — the current desktop background image, symlinked at `~/.config/omarchy/current/background`. Average color is computed by iterating all pixels in linear light (γ=2.2 decode → average → re-encode).

**WLED** — an open-source LED controller firmware. Accepts color via HTTP POST to `/json/state` with a JSON payload `{ on, bri, seg[0].col[0]: [r,g,b] }`.

**Saturation boost** — scaling the HSV saturation channel before sending. Accent defaults to 1.2×; fg/bg default to 1.0×. Overridable via `-saturation`.

**Sentinel** — a value that changes when the color source changes (e.g. file mtime, symlink target). Used by the poll fallback only — not part of the `ColorSource` interface.

**Poll fallback** — when `fsnotify` cannot initialise a watcher (unusual on Linux), `poll()` runs a 1-second loop checking the sentinel value instead of using filesystem events.

**Seam** — the `ColorSource` interface: `Read()`, `WatchDir()`, `IsTrigger()`. Adding a new source means implementing this interface only.

## File Layout

```
main.go                 — CLI, Color Source, push/watch/poll, config
wallpaper.go            — Wallpaper / Background decode, γ pipeline, column→LED strip
wled.go                 — WLED HTTP client (solid, spatial seg.i, LED count)
main_test.go            — Go test suite
omarchy_wled.py         — Python source (used by TUI only)
omarchy_wled_tui.py     — Textual TUI (omarchy-wled-tui entry point)
test_omarchy_wled.py    — Python pytest suite (covers omarchy_wled.py)
omarchy-wled@.service   — systemd user service template
PKGBUILD                — AUR package definition (Go build)
pyproject.toml          — Python package metadata (TUI only)
```

## Key Paths (runtime)

```
~/.config/omarchy/current/theme/colors.toml   — accent/fg/bg hex values
~/.config/omarchy/current/theme.name          — rewritten on theme switch (watch trigger)
~/.config/omarchy/current/background          — symlink to current wallpaper image
~/.config/omarchy-wled/config.toml            — optional saved settings (ip, source, brightness, saturation)
```

