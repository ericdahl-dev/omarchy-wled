# ADR-0002: watchdog and Pillow are optional dependencies

**Date:** 2026-05-01  
**Status:** Superseded (historical)

## Original decision (Python)

`watchdog` and `Pillow` were declared as `[project.optional-dependencies]`, not hard deps.

## Current state

There is **no Python package** in this repository. Filesystem watching uses
`github.com/fsnotify/fsnotify` (compiled in). Wallpaper decoding uses Go's `image`
packages and `golang.org/x/image` where needed.

## Reason (historical)

Optional deps avoided bloating installs for accent/fg-only users.

## Consequences

N/A for current codebase; retained for audit trail only.
