# ADR-0033: The TUI frames the list with cards, marks a channel tuning in at once, and animates only while waiting

- **Status:** Accepted (amended 2026-10-06: an equalizer ahead of the
  playing track in the now-playing card; amended 2026-11-09: the history
  card follows what plays, and takes the lines a short window frees)
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

- **Layout.** A header line (the red "SomaFM Stations" title, its
  channel count, the listener column heading), the flat list (ADR-0026),
  then cards: about and history when open, and an always-present
  now-playing card, with the key help at the very bottom. Cards are
  rounded boxes with their title set into the top border (`ui.Card`); the
  now-playing border takes the state's color (teal playing, gold
  connecting, gray stopped, red disconnected).
  - *Amendment 2026-11-13.* A lost daemon connection is red only once it
    is not routine. A restart the TUI itself asked for (a version upgrade
    or a lost audio device, `restartCmd`) shows gold "Updating" until the
    fresh daemon is there; any other drop shows gold "Reconnecting" and
    turns red "Disconnected" only after `serverLostGrace` (3 s), since a
    daemon another client or the daemon itself restarts is back well
    within it.
  - *Amendment 2026-11-09.* The history card follows what plays while it
    is open (`followHistory`): a new track fetches it again, and a new
    channel takes over its title and entries. It is not modal (see the
    rejected overlays below), so it stays open while the list is used,
    and it showed the tracks of the moment `h` was pressed, under the
    name of a channel long left. When playback stops it keeps the last
    channel's history. One fetch is in flight at a time and a newer one
    follows its reply, as with the volume (ADR-0003): the daemon may
    answer out of order, and reads the history before a slow backfill.
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
- **Animation** is the spinners (loading, connecting, reconnecting, and
  the TUI's own reconnect to the server) and a three-bar equalizer ahead
  of the playing track in the now-playing card. One tick chain drives
  them (`syncAnim`): it starts when one is on screen and ends at the
  first tick with none, so a stopped TUI never wakes up, and at most one
  chain runs. Spinners tick every 120 ms, the
  equalizer alone every 250 ms; the two are never on screen together,
  and the equalizer lies flat while the snapshot is stale (server lost).
  The opt-in spectrum visualizer (ADR-0034) moves on the daemon's frames,
  not on this chain.
- **Short windows** lose the blank spacing lines first, then the short
  help (never the full help, which was asked for), before the list is
  squeezed below its minimum and the header is pushed off the top.
  - *Amendment 2026-11-09.* The history card takes the lines those steps
    free, like the list's own beyond its minimum: it shows all its
    entries at the first step with the room for them, or else as many as
    the last step fits (`historyEntryLimit`). Its limit used to be
    measured against the full chrome whichever step the layout then
    took, so at 80×14 it drew an empty card titled "latest 0 of 20"
    above lines nothing used, and showed its first entry at 16 lines. The
    entries were asked for; the spacing and the short help were not. So
    on an 80×24 terminal a history of 20 now shows 13 of them without
    spacing or short help, where it showed 9 with both, and one of five
    leaves the window as it is. With no room for a single entry (11
    lines and fewer) the card is not drawn at all.
- **Help.** The short help is rendered by `ui.ShortHelp`, not
  `help.Model.ShortHelpView`, which overflows its width when an item does
  not fit and neither would its ellipsis.
- Stream titles are shown as "Artist — Title" using `audio.SplitTitle`,
  the same split MPRIS and Last.fm use.

## Consequences

- New visual states need an entry in `spins` if they show a spinner,
  or the chain will not run (or will not stop).
- A playing TUI wakes four times a second to redraw the equalizer
  (amended 2026-10-06, at the user's request: the playing state should
  be seen to move; a ♫/♪ swap was tried first and did not convince).
- Every color stays an adaptive pair (ADR-0020); the palette gained
  `BorderColor` and `DimColor` for chrome.
- The cards cost vertical space: the now-playing card is four lines
  where the status bar was two. The list's own status line (the channel
  count, now in the header) is gone in exchange.

## Rejected alternatives

- **An animated equalizer in the list** ahead of the playing channel's
  title (2026-09-29, tried and dropped on review, together with the
  card's): a static ▶ marks the channel there. Since 2026-10-06 the card
  has one again, the only animation shown while playing.
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
