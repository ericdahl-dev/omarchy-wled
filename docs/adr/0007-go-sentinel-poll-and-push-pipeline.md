# ADR-0007: Go `Source.Sentinel`, poll fallback type, and split push pipeline

**Date:** 2026-05-04  
**Status:** Accepted  
**Supersedes context:** ADR-0003 for Python; Go differs intentionally.

## Decision

1. **`internal/source.Source` includes `Sentinel()`** — poll fallback is a compile-time requirement for every source implementation (contrast ADR-0003’s Python “convention only”).
2. **`internal/daemon.PollFallback`** encapsulates the blocking sentinel polling loop (interval + `PollTick`); `RunSentinelPollLoop` delegates to it.
3. **Push pipeline is split** — `PreparePushColors` (read + saturation + optional gradient strip) and `DeliverPreparedColors` (dedupe + HTTP + logging) replace an opaque monolith so CLI and TUI share one prepare/deliver path.

## Reason

- Prevents silent omission of poll support for new sources in Go.
- Makes the poll adapter explicit and testable by name.
- Separates color computation from WLED transport while preserving daemon/TUI behavior (strict vs silent errors documented on `PreparePushColors`).

## Consequences

- ADR-0003 remains historical documentation for the Python design; new work should reference this ADR for Go.
