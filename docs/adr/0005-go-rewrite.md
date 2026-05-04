# ADR-0005: Rewrite core CLI in Go

**Date:** 2026-05-02  
**Status:** Accepted

## Context

The Python implementation required:
- `python` runtime as a hard AUR dependency
- `python-watchdog` for filesystem watching
- `python-pillow` for wallpaper color averaging
- `python-hatchling` + `python-installer` as build tools

This made the AUR package heavier than necessary for a small daemon that only
watches a file and posts JSON to an HTTP endpoint.

## Decision

Rewrite `omarchy-wled` (the core CLI/daemon binary) in Go. The Go version:

- Compiles to a **single static binary** with zero runtime dependencies.
- Uses `github.com/fsnotify/fsnotify` (compiled in) instead of `watchdog`.
- Uses Go's `image` stdlib for wallpaper averaging instead of Pillow.
- Parses the simple TOML config with a regexp instead of a TOML library.

The PKGBUILD now has only `makedepends=('go')` and no `depends` at all.

## Update (2026-05): Python removed from the repository

The legacy Python package (`omarchy_wled.py`, `omarchy_wled_tui.py`, `pyproject.toml`,
pytest suites) has been **removed**. Configuration UI is **`omarchy-wled tui`** (Bubble Tea,
same binary). CI runs **Go only**.

## Consequences

- AUR install becomes `yay -S omarchy-wled` with no Python or pip involvement.
- The binary is ~7 MB (stripped), smaller than a typical Python venv.
- CLI flags use Go's `flag` package style (`-flag` rather than `--flag`),
  which is a minor UX change for existing users.
- The `bg` source supports PNG and JPEG out of the box; other formats (WebP,
  GIF, …) are not decoded unless extra Go image decoders are imported.
