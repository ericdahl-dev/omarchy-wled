# ADR-0003: sentinel() excluded from ColorSource protocol

**Date:** 2026-05-01  
**Status:** Accepted

## Decision

`sentinel()` is not part of the `ColorSource` protocol. It is implemented on concrete sources but passed directly to `_poll()` as a callable — or accessed internally — rather than being an interface requirement.

## Reason

`sentinel()` is a poll-fallback-only concern. Including it on the protocol forces every future `ColorSource` implementor to think about polling semantics, which is irrelevant when `watchdog` is installed. The protocol interface should only contain what all callers need: `read()`, `watch_path()`, `is_trigger()`.

## Consequences

New sources must still implement `sentinel()` to support the poll fallback. This is a convention, not enforced by the protocol type.
