# ADR-0001: Single-file architecture

**Date:** 2026-05-01  
**Status:** Accepted

## Decision

All logic lives in `omarchy_wled.py`. No package structure, no sub-modules.

## Reason

The tool is small and purpose-specific. A single file is easier to install via AUR, inspect, and distribute. Adding package structure would increase scaffolding with no benefit at this scale.

## Consequences

If the codebase grows significantly (multiple sources, transports, or a config system), revisit and split into a proper package. The `ColorSource` protocol already defines a clean seam for that split.
