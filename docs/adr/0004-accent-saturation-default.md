# ADR-0004: Accent source defaults to 1.2× saturation

**Date:** 2026-05-01  
**Status:** Accepted

## Decision

When `--source accent` is used and `--saturation` is not explicitly set, saturation defaults to `1.2`. All other sources (`fg`, `bg`) default to `1.0`.

## Reason

Theme accent colors in Omarchy are often muted relative to how they look on LEDs. A 1.2× boost makes them more vivid without being aggressive. This was verified perceptually — the other sources (foreground and wallpaper average) do not have the same muting issue.

## Consequences

Users who want the raw accent color must pass `-s 1.0` explicitly. The default is overridable; it is not baked into `ThemeColorSource`.
