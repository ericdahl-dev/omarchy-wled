# omarchy-wled — Domain Context

## Purpose

Sync color from an [Omarchy](https://omarchy.org) desktop environment to a [WLED](https://kno.wled.ge) LED device in real time. Watches for theme or wallpaper changes and pushes the new color over HTTP.

## Domain Glossary

**Color Source** — anything that can produce an RGB color and describe what file to watch for changes. Three concrete sources exist: `accent` (theme accent key), `fg` / `foreground` (same — maps to the **`foreground`** key in `colors.toml`, Omarchy’s UI/font color), `bg` (wallpaper average). In Go, `internal/source` implements this as the `Source` interface (`ThemeEntry` for TOML keys, `WallpaperAverage` for the background image).

**Accent** — the highlight color defined in the Omarchy theme's `colors.toml`. Typically vivid but may be muted depending on the theme; boosted to 1.2× saturation by default.

**Theme** — an Omarchy color scheme. Lives under `~/.config/omarchy/current/theme/`. Accent/fg sources watch `~/.config/omarchy/current/` for writes to **`theme.name`** (theme switch) and **`theme/colors.toml`** (color edits), so updates are not missed when only one file changes.

**Wallpaper / Background** — the current desktop background image, symlinked at `~/.config/omarchy/current/background`. Average color is computed by iterating all pixels in linear light (γ=2.2 decode → average → re-encode).

**WLED** — an open-source LED controller firmware. Accepts color via HTTP POST to `/json/state` with a JSON payload `{ on, bri, seg[0].col[0]: [r,g,b] }`. For a per-LED spatial strip, POST uses `seg[].i`: optional starting LED index, then one six-digit hex color per physical LED (see `internal/wled.PostSpatialGradientJSON`).

**Saturation boost** — scaling the HSV saturation channel before sending. Accent defaults to 1.2×; fg/bg default to 1.0×. Overridable via `-saturation`.

**Sentinel** — a string fingerprint that changes when the color source’s backing files change (e.g. `theme.name` / `colors.toml` mtimes, wallpaper symlink target). Exposed as `source.Source.Sentinel()` for the poll fallback and implemented per source type.

**Poll fallback** — when `fsnotify` cannot initialise a watcher (unusual on Linux), the daemon runs a 1-second loop: if `Sentinel()` differs from the previous value, it debounces and pushes (same path as a filesystem trigger).

**Seam** — the `source.Source` interface: `Read()`, `WatchDir()`, `IsTrigger()`, `Sentinel()`. Adding a new source means implementing this interface only.

## File Layout

```
main.go                 — CLI entry: paths.Init, flags, daemon loop orchestration
internal/paths          — Omarchy + app paths under $HOME
internal/color          — colors.toml hex parse; HSV saturation scaling
internal/config         — flat config.toml load (regex)
internal/wallpaper      — image decode, γ pipeline, column→LED strip
internal/wled           — HTTP JSON to WLED (solid, spatial seg.i, LED count)
internal/source         — Source interface; theme vs wallpaper implementations
internal/daemon         — push dedupe, fsnotify watch, poll fallback
tui.go                  — Bubble Tea TUI (`omarchy-wled tui`)
tui_config.go           — TUI config, systemd helpers, live preview
main_test.go            — Go test suite
tui_config_test.go      — TUI config / systemd unit tests
omarchy-wled@.service   — systemd user service template
PKGBUILD                — AUR package definition (Go build)
```

## Key Paths (runtime)

```
~/.config/omarchy/current/theme/colors.toml   — accent/fg/bg hex values
~/.config/omarchy/current/theme.name          — rewritten on theme switch (watch trigger)
~/.config/omarchy/current/background          — symlink to current wallpaper image
~/.config/omarchy-wled/config.toml            — optional saved settings (ip, source, brightness, saturation, optional gradient)
```
