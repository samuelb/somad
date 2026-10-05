# ADR-0034: The TUI's spectrum visualizer is analyzed in the daemon and drawn into the view's blank cells

- **Status:** Accepted
- **Date:** 2026-10-06
- **Sources:** `internal/audio/spectrum.go`, `internal/server/spectrum.go`, `internal/ui/visualizer.go`, `internal/app/commands.go` (`syncSpectrum`), `internal/app/view.go` (`View`)

## Context

The user asked for a cava-style animation behind the TUI, toggled with a
key. cava draws a spectrum of the audio actually playing; an imitation
that moves on a timer was offered and declined. The TUI never touches
audio (ADR-0001, ADR-0003), and it may be on another machine than the
speakers (ADR-0007), so the samples exist only in the daemon.

## Decision

- **The daemon analyzes.** A tap (`pcmTap`) on the decoded PCM the oto
  player pulls keeps the last 1.5 s, mixed to mono. `AudioPlayer.Spectrum`
  runs a 4096-point FFT (Hann window, hand-written radix-2; no dependency
  for thirty lines) over the window that is audible now: the tap runs
  ahead of the speakers by what the oto player has buffered
  (`BufferedSize`, up to half a second) plus an estimate of the device's
  own buffers, so the window ends that far back.
- **Bands on the wire.** `Spectrum.Bands(n)` sums the power into `n`
  bands spread logarithmically over 50 Hz–12 kHz, per octave so the levels
  do not depend on `n`, and maps −45…−12 dB onto a byte. The range was
  measured on SomaFM channels from Drone Zone to DEF CON: bands spend most
  of their time between −40 and −20 dB, so a narrower range keeps the bars
  moving, and a fixed one needs no per-client gain state.
- **Opt-in, per connection.** A `spectrum` request with `bands` > 0
  subscribes the connection (0 unsubscribes); a frame loop then sends
  `spectrum` events, one byte per band, 25 a second while audio plays,
  then 25 silent frames after it stops so the bars settle, then nothing
  until it plays again. A stream that has stalled (the tap took no audio
  for 250 ms) counts as stopped, so its last window does not hold the
  bars up until the watchdog reconnects. The loop runs only while a connection is
  subscribed. Events go through their own latest-wins slot per connection
  (ADR-0018), and the client keeps them apart from the state and channel
  events, so frames can never push a snapshot out of either queue.
- **No protocol version bump.** The method and event are additions: an
  older client never subscribes, so it never sees the event, and a newer
  TUI talking to an older daemon gets "unknown method", turns the
  visualizer off, and says the daemon is out of date when it is (the
  restart onto the new version follows at the next interruption,
  ADR-0006).
- **The TUI draws.** `v` toggles it, off at start and not persisted. The
  model asks for a band per bar across the window (at most 512, shared by
  neighboring bars past about 1500 columns), keeps one subscribe
  request in flight and re-syncs on its reply (a connection's requests
  are handled concurrently, so two in flight could land out of order),
  and subscribes afresh after a reconnect. Bars rise quickly and fall
  under their own gravity, smoothed in the TUI from the frames as they
  come; no tick of its own (ADR-0033's chain is untouched).
- **Behind the view, not over it.** The rendered view is composited: a
  bar draws its glyphs into blank cells and carries on behind anything
  drawn as the cells' background, blended 40 % towards the terminal's
  background so the text on it stays legible; the blank cell on either
  side of text takes that shade too, so words never run into a bar.
  Cells with a background of their own keep it. The bars rise from the
  top of the cards, which stay clear, to the top of the screen. A run of
  blanks no bar reaches is written back byte for byte, so the view is
  unchanged where the bars are not. They shade from teal to gold, the
  palette's accents toned down, as adaptive pairs (ADR-0020).

## Consequences

- A TUI with the visualizer on redraws 25 times a second while music
  plays, and the daemon wakes 25 times a second while any client is
  subscribed, playing or not (it then only checks for a session).
- A remote TUI receives about 3 KB/s of frames while it shows the
  visualizer; without it, nothing.
- The `outputPlayer` boundary gains `BufferedSize`, the one call made off
  the session goroutine that otherwise owns the oto player; oto locks it.
- Anything drawn into the view with spaces under a background color stays
  opaque; plain spaces between a list row's columns let the bars through.

## Rejected alternatives

- **Bars only through blank cells** (2026-10-06, the first version): with
  a blank margin around text and no half bars, every row of text cut the
  bars into pieces, which the user found disruptive. A shade of 25 %
  behind text kept the bars whole but left grey and red text hard to
  read on them; 50 % read as holes cut into the bars.

- **A procedural animation** in the TUI alone (2026-10-06, offered and
  declined): no protocol or daemon change, but it would not follow the
  music, which is the point of cava.
- **Raw samples to the client**, analyzed there: 44.1 kHz of PCM over a
  remote connection to draw a few dozen bars.
- **Bars through the cards**: drawn into the now-playing card's padding
  they looked busy, so the cards read as panels in front of them.
