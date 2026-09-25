# ADR-0031: Last.fm scrobbling lives in the daemon, opt-in, with the session key kept out of the config file

- **Status:** Accepted (amended 2026-09-09: one scrobble per play across
  interruptions; 2026-09-25: a reload can start scrobbling, local daemons
  only)
- **Date:** 2026-09-03
- **Sources:** TODO.md "Last.fm scrobbling"; `internal/lastfm`,
  `internal/server/lastfm.go`, `internal/state/lastfm.go`,
  `internal/config/config.go`, `cmd/soma/lastfm.go`; ADR-0010, ADR-0012,
  ADR-0019, ADR-0030

## Context

TODO.md asked for Last.fm now-playing updates and scrobbling: an API key
and session key configured once, then now-playing on track change and a
scrobble once a track has played long enough — the usual ≥30 s / half-track
rule, though radio streams carry no track length so only the 30 s floor
applies. Two things this codebase already draws a line on had to be
respected: all outbound HTTP is allowlisted to SomaFM hosts (ADR-0010), and
the config file is meant to be hand-editable, never machine-written
(ADR-0011).

## Decision

- The daemon sends now-playing updates and scrobbles itself, from
  `handleTrackUpdate` (`internal/server/lastfm.go`), the same place that
  already updates MPRIS, notifications (ADR-0030), and history — not the TUI
  or CLI, for the same reason ADR-0019 and ADR-0030 put MPRIS, the tray, and
  notifications in the daemon: it is the process guaranteed to be running
  when a title changes.
- `ws.audioscrobbler.com` is a second, explicit entry in
  `internal/security`'s host allowlist (`lastfmHosts`), https-only, rather
  than a widening of the SomaFM check — exactly what ADR-0010 asked any new
  integration to do.
- The Last.fm session key obtained by `soma lastfm login` is written to its
  own state file (`internal/state`'s `lastfm.json`, atomic writes, 0600,
  corrupt-file quarantine — the same treatment ADR-0012 gives every other
  machine-written file), never to `config.yaml`. The config's
  `lastfm.session_key` still exists as an explicit override for config-
  management setups, but logging in itself never touches that file.
- A track is scrobbled when it ends (the next title change, a stop, or a
  channel switch) if it played for at least 30 seconds
  (`lastfmMinPlayDuration`); a title with no artist (`audio.SplitTitle`
  finds none) is skipped entirely; both submissions run off the server's
  lock on a goroutine with one bounded retry, and a failure is logged once
  per kind, never blocking or failing playback.
- A play is scrobbled at most once (amended 2026-09-09). Every interruption
  tears the stream down and the fresh connection re-reports the current
  title with no memory of the last one, so a stall-and-reconnect, a pause,
  or a channel round trip mid-track used to end the play (scrobbling it)
  and then start a second play of the same track (scrobbling it again).
  Live radio has no seek or skip, so the same artist/title reappearing on
  the same channel is the same play: `Server.lastfmRecent` remembers, per
  channel, the play last interrupted there, and `updateLastfmLocked`
  resumes it (original start time, accumulated listened time, `scrobbled`
  flag) when the title matches within `lastfmSameTrackWindow` (2 h since
  the first sighting; long enough for the longest ambient sets, short
  enough that a genuine replay later in the day still scrobbles). Only
  listened stretches count towards the 30 s, not time paused. The memory
  is in-process: a daemon restart mid-track can still scrobble that one
  track twice, accepted rather than persisting it.
- `server.Config.Scrobbler` is a small interface
  (`UpdateNowPlaying`/`Scrobble`/`SetSessionKey`) that `internal/lastfm.Client`
  implements against the real API; tests inject a fake, the same pattern
  ADR-0030 used for `Notifier`.
- A `reloadLastfm` RPC method (protocol version 2, unreleased since the last
  bump, so no further bump was needed) lets `soma lastfm login`/`logout`
  apply a fresh (or cleared) session to an already-running daemon
  immediately, instead of only on the next restart. The daemon re-reads
  the config file for it (amended 2026-09-25): a daemon started before
  `api_key`/`api_secret` were set has no scrobbler, and used to acknowledge
  the reload and silently keep not scrobbling; it now builds one then
  (`server.Config.LoadScrobbler`, injected from `cmd/soma` so the server
  stays free of config-file knowledge), and a config file that no longer
  loads fails the reload visibly. The CLI sends the reload to a local
  daemon only: a remote one (`--server`) reads the session file on its own
  host, which a login here never touches, so it prints a note to log in on
  that host instead.

## Consequences

- `internal/lastfm` stays a plain API client (request building, signing,
  response decoding) with no knowledge of where the session key is stored;
  that split lives in `cmd/soma` (resolving it at daemon startup) and
  `internal/state` (persisting it), keeping the client testable against a
  bare `httptest` server.
- A local daemon started before login, or with scrobbling not configured
  at all, needs no restart to catch up once `reloadLastfm` fires. The
  track already playing when a scrobbler is first built is neither
  announced nor scrobbled; tracking starts with the next title.
- Losing the session file (or never logging in) degrades to "scrobbling
  configured but inactive," not a startup failure: `Config.Scrobbler` is
  still constructed once `api_key`/`api_secret` are set, and calls simply
  fail (logged once) until a session exists.
