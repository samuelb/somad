# ADR-0026: Keep the channel list flat: no detail pane, no sort options, no render-path micro-optimizations

- **Status:** Rejected (the features); the list stays as it is
- **Date:** 2026-09-03
- **Sources:** TODO.md assessment of 2026-09-03; `internal/ui/delegate.go`, `internal/app/favorites.go`, `internal/app/search.go`

## Context

The 2026-09 assessment proposed a channel detail pane (descriptions
truncate to one line), sort options by listeners, genre or name, and
hoisting the per-frame lipgloss styles plus a set-based `IsMatch`.

## Decision

- **No detail pane.** Channel descriptions are short; there is not much to
  show beyond the one line already rendered.
- **No sort options.** API order with favorites hoisted is enough.
- **No style hoisting or `IsMatch` set lookup.** The delegate renders a few
  dozen rows per frame against at most a few dozen matches; nothing has
  measured it as slow. Revisit only with a measurement.

## Consequences

- Search stays a search-and-jump; a matches-only view and a favorites-only
  view are still open items that would share plumbing (TODO.md).
