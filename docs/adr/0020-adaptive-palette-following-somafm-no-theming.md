# ADR-0020: The palette follows somafm.com and adapts to light and dark terminals; no user theming

- **Status:** Accepted
- **Date:** 2026-09-01 (theming declined 2026-09-03)
- **Sources:** 9b254e7; `internal/ui/styles.go`

## Context

The palette was hardcoded for dark terminals: white titles and light-grey
secondary text were invisible on stock macOS Terminal or Solarized Light.
The colours themselves were chosen to match the somafm.com website.

## Decision

- Every colour is a `lipgloss.AdaptiveColor` pair. The Dark side keeps the
  original somafm.com-derived palette exactly; the Light side is a darker
  equivalent with WCAG AA contrast on white. lipgloss picks per the
  detected background.
- The TUI must keep working out of the box on both dark and light
  terminals. That is the invariant; any palette change must preserve it.

## Consequences

- The tokens are package-level variables consumed at init time by the
  style vars and the list delegate, and a few styles are built inline in
  `view.go` and `delegate.go`.

## Rejected alternatives

- **A `theme:` config section** (2026-09-03). The palette is tokenized, but
  init-time consumption would need a `SetTheme` call or struct-held styles,
  and the value is low for a radio client whose look is meant to match the
  station. Not planned.
