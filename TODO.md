# TODO

Open findings from the 2026-09 code assessment, the Codex reviews of the
`improvements` branch (TLS 1.3, adaptive colors, jitter buffer, AAC playback —
all landed on `main`), and the 2026-09-03 follow-up analysis. Every item was
re-verified against the tree on 2026-09-03; nothing here is already done.
Items deliberately dropped are recorded in `docs/adr/` and listed under
"Not planned" at the end so they are not re-proposed.

Priorities: **P1** = known bugs or real exposure, fix next; **P2** = high-value
improvements; **P3** = polish and nice-to-haves. Each item carries a rough
effort tag: **[S]** under an hour, **[M]** an afternoon, **[L]** multi-day or
needs a decision.

## P1 — correctness and security

## P2 — features and hardening

Ordered roughly by value ÷ effort.

- [ ] **PSK quality** [S]. Template suggests `psk: "change-me"`
      (`internal/config/config.go:235` and `:258`, mirrored in README);
      `Config.validate` checks only mutual exclusivity; `readPSKFile`
      (`cmd/soma/endpoint.go:96`, reused by the daemon) rejects only an
      empty file and never stats it, while the socket dir *is* checked for
      `0o077` and owner uid in `internal/protocol/socket.go:32`. Add
      `soma daemon --gen-psk` (32 random bytes at 0600, modelled on
      `--show-cert`) plus an SSH-style permissions check in `readPSKFile`.
- [ ] **Track history** [M]. `somafm.com` is allowed exactly by
      `ValidateURL` (`validation.go:68`), so
      `https://somafm.com/songs/<channel>.json` passes today, and the daemon
      sees every title change. Needs a protocol method, server fetch/cache,
      `soma history [--json]`, and a TUI pane. Note `h` is already bound by
      the bubbles list as *previous page* (`left, h, pgup, b, u`); a
      model-level `h` case would silently shadow pagination, so pick another
      key or remove `h` from the list keymap.
- [ ] **Search improvements** [M] (`internal/app/search.go`,
      `internal/app/update.go:85`). `UpdateSearchMatches` only collects
      indices and jumps; the list's own filtering is disabled at
      `cmd/soma/main.go:556`, so there is no matches-only view although the
      README says "Filter channels". Matching is lowercase substring.
      `sahilm/fuzzy` is in `vendor/` only transitively via bubbles'
      `DefaultFilter` (which this project switches off), so it is available
      at zero dependency cost. `/` resets the query instead of pre-filling
      it. The matches-only view is the real work: the delegate and
      `SearchMatches` index arithmetic assume a stable full list.
- [ ] **Split `cmd/soma/main.go`** [M] (658 lines). `runServer` has ten
      `log.Fatal*` calls; the five in option resolution (config load,
      tls-cert/key pairing, cert prep, cert load, PSK file) are what block a
      pure, testable `resolveDaemonOptions(cfg, args)`; the rest are runtime
      failures that can stay fatal. `cli.go` `fail()` calls `os.Exit(1)` at
      31 sites, so `runX` cannot run in-process in tests. Split
      `daemon.go`/`tui.go`. Coverage: `cmd/soma` is at 41.9%, the lowest
      package with real logic (`internal/platform` at 0% and `tray` at 28.8%
      are thin OS bindings).
- [ ] **Desktop notification on track change** [M]: show the track
      title and artist, opt-in via config. `handleTrackUpdate`
      (`internal/server/playback.go`) only sets `s.trackTitle`, updates
      MPRIS, and broadcasts today. `TrackInfo` carries the raw ICY
      `StreamTitle` (`Artist - Title`) unsplit, and `updateMPRISLocked`
      passes the channel title as `xesam:artist`, so split it once and let
      MPRIS use the same artist/title. Linux: `notify-send`/D-Bus
      `org.freedesktop.Notifications`; macOS: `osascript` or
      `UNUserNotificationCenter` via the existing Cocoa bridge. Should
      fire from the daemon, not the TUI, so it works with the TUI closed.
- [ ] **Last.fm scrobbling** [L, wanted]. Now-playing on track change,
      scrobble after the usual ≥30 s / half-track rule, API key + session
      key in config, one-time auth flow (`soma lastfm login`). Two
      constraints from the codebase: all outbound HTTP goes through
      `security.NewRequest`, whose allowlist is SomaFM-only, so
      `ws.audioscrobbler.com` needs an explicit second allowlist entry
      rather than a widening; and the artist/title split above is a
      prerequisite.
- [ ] **macOS signing + notarization** [L, planned — research first].
      README's `xattr -d com.apple.quarantine` workaround is the tell;
      `build-darwin` goes `go build` → `lipo` → `build-dmg.sh` → upload
      with no `codesign` or `notarytool` anywhere. Needs an Apple Developer
      ID ($99/yr), .p12 secrets handling in the workflow, and a
      hardened-runtime check against the cgo/Cocoa tray. The workflow
      change itself is small once the identity exists. Blocked on reading
      up on the notarization flow first.

## P3 — polish

Small hygiene fixes first, then features, then code quality.

### Hygiene (each [S])

### Features

- [ ] Sleep timer (`soma stop --in 45m`): `runStop` takes no args today.
      Model it on the idle-exit `time.AfterFunc` in
      `internal/server/server.go:467`. [S–M]
- [ ] Favorites-only view toggle in the TUI. `sortItemsWithFavorites`
      (`internal/app/favorites.go:81`) only partitions favorites first;
      nothing filters. [S–M, shares the matches-only plumbing with search]
- [ ] Mouse support: sole `tea.NewProgram` call at `cmd/soma/main.go:571`
      uses only `WithAltScreen`. [S]

### Code quality

- [ ] Deduplicate: XDG/darwin base-dir resolution ×2 (`internal/state`
      and `internal/config` are structurally identical; `internal/channels`
      uses a simpler `os.UserCacheDir` variant that should fold into the
      same helper), favorites `map[string]bool` built ×4 (`cli.go:204`,
      `cli.go:233`, `server.go:300`, `server.go:517`), and a byte-identical
      `str(*string)` closure ×2 in package `main` (`endpoint.go:35`,
      `main.go:270`). [S]
## Not planned

Scope cuts are recorded as Architecture Decision Records in `docs/adr/`
(see the "Rejected alternatives" sections); this list only points there so
they are not re-proposed.

- Linux AAC decoding — ADR-0016
- Theming via config — ADR-0020
- Channel detail pane, sort options, per-frame style hoisting — ADR-0026
- Protocol min/max version range in hello — ADR-0002
- Shared per-address auth limiter — ADR-0008
- Semver comparison for version-skew restarts — ADR-0006
- CONTRIBUTING.md and a committed CHANGELOG — ADR-0022
- Raising the CI coverage gate — ADR-0025
- Re-arming the stall watchdog on buffer consumption — ADR-0013
