# ADR-0033: The TUI frames the list with cards, marks a channel tuning in at once, and animates only while waiting

- **Status:** Accepted
- **Date:** 2026-09-29
- **Sources:** `internal/ui/widgets.go`, `internal/ui/delegate.go`, `internal/app/view.go`, `internal/app/model.go` (`tuningID`), `internal/app/commands.go` (`syncAnim`)

## Context

The TUI was a plain list over a one-line status bar, with the about and
history views as separator-and-text footers and the list's own help in
between. The playback state was easy to miss. Switching channel gave no
visible sign in the list: the server reports the new channel as
connecting while the old stream keeps playing and fades out under it, and
the list only marked a channel once it was playing.

## Decision

- **Layout.** A header line (a red "SomaFM" badge, the view's name, its
  channel count, the listener column heading), the flat list (ADR-0026),
  then cards: about and history when open, and an always-present
  now-playing card, with the key help at the very bottom. Cards are
  rounded boxes with their title set into the top border (`ui.Card`); the
  now-playing border takes the state's color (teal playing, gold
  connecting, gray stopped, red disconnected).
- **Rows** add the genre after the title, a static ▶ and a colored heart
  ahead of it for the playing and favorite channels, the listener count
  alone in its column, and the search match highlighted: always in the
  title, in the description only when it is one run of adjacent
  characters, since a fuzzy match scattered across a sentence highlights
  noise.
- **A channel tuning in is marked at once.** From the key press, the row
  shows a spinner and "tuning in…" in place of its genre, until the
  channel plays or the play fails. Before the server answers, the mark
  comes from `requestedID`, a request in flight that the next snapshot or
  a failed request clears (like `pendingPlayID` for upgrade restarts), so
  the model still keeps no playback state (ADR-0003); after that, from the
  snapshot's connecting or reconnecting channel.
- **Animation** is only spinners: loading, connecting, reconnecting, and
  the TUI's own reconnect to the server. One tick chain drives them
  (`syncAnim`): it starts when a spinner is on screen and ends at the
  first tick with none, so a playing or stopped TUI never wakes up, and at
  most one chain runs.
- **Short windows** lose the blank spacing lines first, then the short
  help (never the full help, which was asked for), before the list is
  squeezed below its minimum and the header is pushed off the top.
- **Help.** The short help is rendered by `ui.ShortHelp`, not
  `help.Model.ShortHelpView`, which overflows its width when an item does
  not fit and neither would its ellipsis.
- Stream titles are shown as "Artist — Title" using `audio.SplitTitle`,
  the same split MPRIS and Last.fm use.

## Consequences

- New visual states need an entry in `animates` if they show a spinner,
  or the chain will not run (or will not stop).
- Every color stays an adaptive pair (ADR-0020); the palette gained
  `BorderColor`, `DimColor` and `OnAccentColor` for chrome.
- The cards cost vertical space: the now-playing card is four lines
  where the status bar was two. The list's own status line (the channel
  count, now in the header) is gone in exchange.

## Rejected alternatives

- **An animated equalizer** for the playing channel, ahead of its title
  in the list and the card (2026-09-29, tried and dropped on review): a
  decorative animation that never stops while playing; a static ▶ marks
  the channel instead.
- **A listener meter** (signal bars on a logarithmic scale) beside the
  count (2026-09-29, tried and dropped on review): the number says it.
- **Modal overlays** for about and history (2026-09-29). Keys keep acting
  on the list while they are open, so covering the rows would hide what
  the keys act on; they stay cards stacked above the now-playing card.
- **A background highlight** on the selected row. A fixed gray per
  light/dark side clashes with terminals whose background is neither
  black nor white; the selection keeps its accent bar and title color.
- **Setting the terminal window title** to the track. Nothing restores it
  on exit, so it would go stale in shells that do not set their own.
