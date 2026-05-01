# ADR-0002: watchdog and Pillow are optional dependencies

**Date:** 2026-05-01  
**Status:** Accepted

## Decision

`watchdog` and `Pillow` are declared as `[project.optional-dependencies]`, not hard deps. The code catches `ImportError` at runtime and falls back gracefully (poll loop for watchdog; `RuntimeError` with install hint for Pillow).

## Reason

The most common use case (`--source accent` or `--source fg`) needs neither library. Forcing them on all users bloats the install. The AUR PKGBUILD still lists both in `depends` since AUR users expect a batteries-included package.

## Consequences

Users installing via `pip` need `pip install "omarchy-wled[watch]"` for filesystem watching, or `pip install "omarchy-wled[bg]"` for wallpaper color. `pip install "omarchy-wled[all]"` gets everything.
