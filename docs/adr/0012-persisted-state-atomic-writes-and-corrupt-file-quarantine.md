# ADR-0012: Persisted state uses atomic writes, quarantines corrupt files, and orders saves by sequence

- **Status:** Accepted
- **Date:** 2026-07-05 (sequencing 2026-07-08, retry-on-shutdown 2026-07-12)
- **Sources:** e32a35a, df4f0f0, afdd677, f47d0fe, 748e409, a4d6016; `internal/atomicfile`, `internal/state`, `internal/channels`

## Context

`state.json` and the channel cache were written in place, so a crash mid
write could leave truncated JSON, and a corrupt state file made the daemon
refuse to start until the user deleted it by hand. Later, two concurrent
mutations could reach the disk in the wrong order.

## Decision

- All persisted files go through `atomicfile`: temp file in the same
  directory, fsync, rename, fsync the parent.
- An unparseable state or cache file is moved to `<name>.corrupt` and the
  daemon starts fresh. "A corrupt state file must not brick startup"; the
  move keeps the evidence.
- Every mutation carries a monotonic sequence number; a save whose
  sequence is not newer than the last written one is dropped. A failed
  write keeps the newest state dirty and flushes it at shutdown.
- User state lives in the XDG state dir (`~/.local/state`, or Application
  Support on macOS), not the config dir, because it is not meant to be
  user-edited.

## Consequences

- Saves are synchronous, so tests observe writes deterministically; the
  fsync cost is hidden behind an injectable persister in tests.
- Any new persisted file must use the same helper and the same quarantine
  behaviour.
