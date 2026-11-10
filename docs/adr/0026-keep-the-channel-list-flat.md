# ADR-0026: Keep the channel list flat: no detail pane, no sort options, no render-path micro-optimizations

- **Status:** Rejected (the features); the list stays as it is (amended 2026-11-09: the rendered view is kept between spectrum frames)
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
  - *Amendment 2026-11-09.* The spectrum visualizer (ADR-0034) brought
    one: it redraws 25 times a second while music plays, and every frame
    rendered the list and the cards again, to the same text. On an M4,
    with 45 channels in a 160×45 window, a frame cost 0.61 ms, 397 KB and
    2042 allocations with the bars, and 0.78 ms, 1.13 MB and 2309 with
    the waterfall (`BenchmarkSpectrumFrame`). `View` now keeps the view
    it rendered for the last message of any other kind (`viewBase`) and
    draws only the visualizer behind it for a frame: 0.11 ms, 177 KB and
    533 allocations with the bars, 0.26 ms, 0.90 MB and 805 with the
    waterfall. The view kept is dropped at every message that is not a
    frame. That is enough because the view changes through messages
    alone: the sleep timer's countdown reads the clock, but a tick
    follows every change of its label. This is a cache of the finished
    view, not the hoisting declined above; a render that does happen
    costs what it did, and nothing has measured that as slow.

## Consequences

- Both filtered views this record anticipated landed on the flat list
  without a pane or sort order: a favorites-only toggle (7ca6bcf) and a
  matches-only view while searching (1198435).
