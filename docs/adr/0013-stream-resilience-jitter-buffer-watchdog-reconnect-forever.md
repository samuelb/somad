# ADR-0013: Stream resilience: jitter buffer, stall watchdog, clean EOF is an error, reconnect forever

- **Status:** Accepted (amended 2026-11-09: every reconnect moves to the next mirror; the backoff starts over only after a stream that lasted; the mirrors last listed stand in for a playlist that cannot be fetched)
- **Date:** 2026-07-05 (jitter buffer 2026-09-01; connect deadline to first decoded frame and mirror failover 2026-09-25)
- **Sources:** eead2e1, d39497b, de8dd79, f062394, 1f9dec1; `internal/audio/player.go`, `buffer.go`; earlier ae9f67c, 4ee15a3, 4f3332d; rejection of watchdog re-arm on 2026-09-03

## Context

The daemon is long-running. Overnight suspends, router reboots and NAT
timeouts all happen, and a live radio stream never ends on its own. An
unbuffered pipe fed the decoder at exactly the network's pace, so any
hiccup longer than the audio device's own small buffer was audible.

## Decision

- A 512 KiB jitter buffer (about half a minute at 128 kbps) sits between
  the fetch and the decoder, with a 32 KiB prefill bounded by a one-second
  deadline. After an underrun reads resume as soon as any byte arrives,
  "trading a possible stutter for the shortest dropout."
- A 30 s no-data watchdog aborts a connection that died without a FIN. It
  is armed before the request so a server that never answers is caught.
- **2026-09-25:** before playback, a 10 s connect deadline runs from the
  request until the first frame *decodes*, not until the first byte: a
  server sending data that never decodes (the wrong format, an endless
  error page) keeps the watchdog quiet and used to hold `Play` for as long
  as it kept sending. The ADTS reader likewise gives up after 32 KiB
  without a valid header. A `Stop` or newer `Play` aborts a `Play` that is
  still connecting instead of letting it run into its deadline. None of
  this touches an established stream, where the watchdog alone applies.
  - *Amendment 2026-11-09.* The MP3 path gives up the same way: the
    reader that starts an MP3 stream on a confirmed frame (ADR-0017)
    fails after 32 KiB without one, where go-mp3 alone went on searching
    until the connect deadline.
- A clean EOF is treated as an error: "a live stream never ends on its
  own; a clean EOF means the server hung up."
- Reconnection backs off exponentially to a one-minute cap and then
  retries forever. Only an explicit stop or a new play ends it. A missing
  playlist is never retried, because reconnecting cannot conjure one up;
  nor is a channel a catalog refresh dropped meanwhile, which stops with
  the error shown (2026-09-25).
  - *Amendment 2026-11-10.* Nor is a lost audio device
    (`audio.ErrAudioDeviceLost`): it does not come back in the daemon's
    process, so the daemon stops and a client replaces it (ADR-0037).
  - *Amendment 2026-11-09.* The backoff started over with every connect
    that succeeded, so the cap only ever applied to connects that failed:
    a stream that connected and dropped again within seconds was retried
    every two seconds for as long as that went on. It now starts over
    only when the stream that dropped had played for 30 s
    (`reconnectStableAfter`); one that dropped sooner is one more failed
    attempt, and the attempt number clients show keeps counting. On a
    line that drops every few seconds the gaps therefore grow to a
    minute, where the short gap bought little music anyway.
- **2026-09-25:** every SomaFM playlist lists the same stream on three
  mirror hosts, in the same host order for every format, and only the
  first was ever used, so one dead host failed every format and every
  reconnect. A play attempt now tries each format on up to three mirrors
  before falling back to the next format, and the first mirror tried
  rotates with the reconnect attempt. That bounds an attempt at 45 s per
  format (15 s playlist fetch plus three 10 s connect deadlines) plus one
  audio-device wait; an audio-device failure ends the attempt at once,
  since every other stream would wait for the device again.
  - *Amendment 2026-11-09.* The rotation was the backoff counter, which
    a successful connect reset, so only the first reconnect moved on:
    from the second drop every reconnect started on the mirror that had
    just dropped, and a relay that accepted, streamed briefly and hung up
    was retried first for good. The rotation is a counter of its own now
    (`mirrorRotation`): a committed stream leaves it on the mirror that
    plays, also when the attempt had to fall through to it, each
    scheduled reconnect moves it one on, and only a play the user asked
    for goes back to the playlist's first mirror.
  - *Amendment 2026-11-09.* The mirrors only helped while the playlist
    could be fetched: it comes from somafm.com for every attempt, and
    with that host down or hanging a stream that dropped never came back,
    although the mirrors it had just played from were fine. The daemon
    now remembers, in memory, the mirrors each playlist listed the last
    time its fetch worked, and uses them when the fetch fails
    (`resolveMirrors`). The fetch still comes first, so a changed
    playlist is picked up and the bound above holds: a hanging playlist
    host costs each attempt its 15 s before the remembered mirrors are
    tried. A playlist never fetched in this daemon's lifetime has nothing
    to fall back on.
- Each failure has exactly one reporting path, and the errors channel is
  lossy by design: it signals "currently unhealthy", it is not a log.

## Consequences

- A network drop plays out the buffered audio before the error surfaces,
  and ICY titles align with what is heard (ADR-0015).
- The watchdog only covers the network side. A decoder error after `Play`
  returned used to leave the buffer full and the fill goroutine parked,
  invisibly, until c175752 reported such errors on the error channel.

## Rejected alternatives

- **Custom ring buffer** (2026-01-31, replaced by `bufio` on 2026-02-03,
  which was in turn replaced by the dedicated jitter buffer above).
- **A budget of five reconnect attempts** (reversed 2026-07-05): "any
  longer outage meant the music never came back."
- **Re-arming the watchdog on buffer consumption** (2026-09-03). The
  decoder-error reader wrapper is the fix for the gap above; this was
  only an optional extra guard.
