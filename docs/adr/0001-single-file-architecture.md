# ADR-0001: Single-file architecture

**Date:** 2026-05-01  
**Status:** Superseded (historical)

## Original decision (Python)

All logic lived in `omarchy_wled.py`. No package structure, no sub-modules.

## Current state

The **core shipped product is Go**: `main.go` plus focused companions (`wallpaper.go`,
`wled.go`, …). The Python single-file implementation has been **removed** from the tree
(see ADR-0005).

## Reason (historical)

The tool was small and purpose-specific. A single file was easier to install via AUR,
inspect, and distribute.

## Consequences

The `ColorSource` seam in Go remains the extension point for new sources.
