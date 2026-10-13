# ADR-0036: The TUI's mouse acts on the list, the volume gauge, and the help bar, hit-tested against View's own layout

- **Status:** Accepted
- **Date:** 2026-10-13
- **Sources:** `internal/app/mouse.go`, `internal/app/view.go` (`body`, `nowPlayingIndex`, `cardContentX`, `helpWidth`), `internal/ui/widgets.go` (`VolumeGaugeWidth`, `VolumeGaugeAt`, `ShortHelpSpans`), `internal/ui/delegate.go` (`RowHeight`, `RowSpacing`)

## Context

The TUI enabled mouse cell-motion reporting only so the wheel could move
the list cursor. Clicking a channel or the volume did nothing, though the
terminal already captured the mouse (so plain text selection needs the
terminal's bypass modifier either way). Bubble Tea reports bare screen
cells and the vendored bubbles/list handles no mouse events, so mapping a
cell to a row or a gauge position is ours to do.

## Decision

- **What the mouse does.** The wheel moves the list cursor, like j/k,
  except over the volume gauge, where it steps the volume like +/-. A
  click selects a channel; a click on the channel already selected plays
  it, through the same path as enter (`playChannel`), so a double click
  plays without timing clicks. A click on the gauge's bar sets the volume
  to fill through that cell, in whole percents, and dragging from there
  follows the pointer, clamped at the bar's ends, until the release; a
  click on the rest of the gauge toggles mute. Drags go through
  `setVolume`, which keeps one change in flight like the keys.
- **The short help bar is a row of buttons.** A click on an item presses
  its binding's first key (`pressBinding`), through the list-mode keymap
  and then the list, as a key press would, but even while the search
  input has the keyboard, where the key would otherwise be typed. Every
  item counts, quit included. `ui.ShortHelpSpans` returns where it put
  each item, so the click and the drawing share one measurement. The full
  help `?` opens is not clickable: its columns are laid out by bubbles'
  help model, which would have to be copied. Nothing highlights under the
  pointer, since that needs all-motion reporting, an event per mouse move.
- **Hit-testing reuses View's layout.** `mouseLayout` builds the same
  chrome and body View joins and measures them, including the lines
  Bubble Tea cuts from the top of a view taller than the window, rather
  than keeping a second copy of the geometry. Within that, rows are found
  by `ui.RowHeight`/`ui.RowSpacing` and the list's paginator, and the
  gauge by its fixed width at the right end of the now-playing card's
  first line.
- **The gauge has a fixed width.** Its label is padded to the width of
  "muted", so the bar no longer shifts as the percentage gains or loses a
  digit, which would move it under a dragging pointer.

## Consequences

- Layout changes must keep the mouse in step: `nowPlayingIndex` must
  match the order `layout` puts the cards in, `cardContentX` the card's
  margin, border, and padding, and the gauge must stay flush right on the
  card's first line. The tests in `internal/app/mouse_test.go` click
  positions found in the rendered view, across the chrome's arrangements,
  so a drift fails them.
- Each mouse event lays the view out once more; motion is reported only
  while a button is held, so this stays cheap.
- A help binding's first key must be one `keyMsg` can press: a named key
  it knows (enter, up, down, space) or a single character; a test checks
  every binding in the help.
- Other targets (pagination dots, closing cards, the full help) are not
  clickable; adding one means adding it to `mouseTargets`.

## Rejected alternatives

- **Play on a single click.** A stray click, or one meant to focus the
  terminal window, would change the station.
- **Timed double clicks.** Bubble Tea reports no click counts, and
  "click the selected one again" needs no timer.
