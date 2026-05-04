# ADR-0006: Go `internal/` package layout

**Date:** 2026-05-03  
**Status:** Accepted

## Context

The Go CLI grew enough that one `main.go` file mixed unrelated concerns: HTTP to WLED,
wallpaper γ math, theme path wiring, flat config parsing, and the fsnotify/poll loop.

## Decision

Split by responsibility under `internal/` (not importable by other modules):

| Package | Responsibility |
|--------|----------------|
| `internal/paths` | Resolve `$HOME`-based Omarchy and app config paths (`paths.Init()`). |
| `internal/color` | Parse hex keys from `colors.toml`; HSV saturation scaling of sRGB. |
| `internal/config` | Regex-driven flat TOML key=value load (no nested tables). |
| `internal/wallpaper` | Decode symlink target image; γ pipeline; column strip → LED samples. |
| `internal/wled` | JSON POST to WLED (`PostSolidJSON`, `PostSpatialGradientJSON`, LED count). |
| `internal/source` | `Source` interface plus `ThemeEntry` / `WallpaperAverage` implementations. |
| `internal/daemon` | Dedupe, push-if-changed, fsnotify watch loop, sentinel polling fallback. |

Repository root keeps `main.go` (CLI wiring only), `tui.go`, `tui_config.go`, and tests.

The Python tree stays single-file per ADR-0001.

## Consequences

- Call sites read as verbs + nouns (`daemon.PushCurrentColorIfChanged`, `wled.PostSolidJSON`).
- Adding a transport or source touches one package instead of a monolith file.
- One extra indirection when navigating code (jump to `internal/`).
