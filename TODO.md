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

- [ ] **Release binaries embed the wrong commit** [S]
      (`.github/workflows/release.yml:145` and `:176`). Both build jobs
      stamp `-X main.commit=${GITHUB_SHA::7}`, the workflow-trigger SHA, but
      the tag points at the "Release vX" bump commit that `prepare` pushes.
      `needs.prepare.outputs.sha` already exists and is already used for
      the `checkout` refs and `target_commitish`; only the two ldflags lines
      are wrong. Every release reports its parent commit in
      `soma --version`. Dry runs hide it because the bump is discarded.
      Bash slicing does not work on an expression, so put the output in an
      env var first.
- [ ] **TCP hardening bundle** [M] (`internal/server/conn.go`,
      `server.go` `acceptLoop`, `cmd/soma/main.go` `listenTCP`). Exposure
      exists only when the operator opts into `--listen`, and
      `checkTCPSecurity` already warns about open configurations, but once
      enabled:
      - No `SetDeadline`/`SetReadDeadline`/`SetWriteDeadline` anywhere.
        An unauthenticated peer holds an fd, the `serveConn` goroutine
        (parked in `Scan`), the `writeLoop` goroutine (started before
        auth), and the scanner buffer forever. With TLS the handshake
        happens lazily inside the first `Read`, so a peer can also stall
        mid-handshake forever. No write deadline either: a stuck reader
        blocks `c.write` while holding `writeMu` (goroutine/fd leak, not a
        server stall — broadcasts use latest-wins channels).
      - No cap on total connections: `acceptLoop` does an unconditional
        `go s.serveConn(nc)`; `maxConcurrentRequests = 32` is per
        connection, not a connection cap.
      - The 1 s auth failure delay (`conn.go:100`) is per connection and
        bypassed by simply opening another one, but the HMAC-SHA256
        single-use-nonce challenge makes online key guessing unrealistic;
        resource exhaustion is the actual threat, which the cap and
        deadlines below address. No shared per-address limiter (decided).
      - **Pre-auth line size**: the server's one scanner is created before
        auth with `MaxLineBytes = 4 MiB` (`internal/protocol/codec.go:11`),
        so peak transient allocation is ~6 MiB per unauthenticated
        connection. The 4 MiB budget exists for the server→client catalog
        event; every client→server line is tiny. Better than a
        pre-auth/post-auth switch (a `bufio.Scanner` cannot be resized and
        a second scanner would drop buffered bytes): cap the *server's read
        side* unconditionally at ~64 KiB and leave `MaxLineBytes` to the
        client's scanner. `TestNewScanner_LargeLine` pins the current
        behaviour and needs a companion.
      **Fix:** scope deadlines to the TCP listener only (the Unix socket
      dir is already `0700` and owner-checked, so a cap there guards only
      against a buggy local client): ~10 s pre-auth handshake deadline, an
      idle deadline after auth re-armed on every `Scan` and long enough not
      to drop an idle TUI (or add a ping), `netutil.LimitListener` around
      the TCP listener as the smallest possible cap. Unauthenticated
      conns are already unregistered (no
      broadcasts, do not hold off idle exit), so do not over-scope. No
      deadline or cap test exists.

## P2 — features and hardening

Ordered roughly by value ÷ effort.

- [ ] **Version-skew restart fires on "different", not "newer"** [S]
      (`internal/client/spawn.go:90` and
      `:110`, `cmd/soma/cli.go:75` and `:324`, `Model.skewed` in
      `internal/app/model.go:71`). All four sites are plain string
      inequality, no semver import exists, and `version = "dev"` gets no
      exemption. Two installations on one machine (a `go build` dev binary
      and the brew one) restart the daemon onto each other on every channel
      change. Exempt `dev` from the restart at all four sites; full semver
      ordering is not planned (the silent-downgrade case has never bitten).
- [ ] **Enter on the already-playing channel tears the stream down** [S]
      (`internal/app/update.go:55`, and `playChannel` in
      `internal/server/playback.go:60`). Neither side compares against the
      current channel; the server bumps `playGen`, re-resolves the playlist,
      and reconnects. Fix server-side (or both) since `soma play <current>`,
      MPRIS `Play`, and the tray picker hit the same path. Promoted from P3:
      it is a user-visible glitch, not polish.
- [ ] **Prefer https stream URLs** [S]. Two separate chooser sites:
      `internal/channels/select.go` ranks `.pls` entries by format and
      quality only, and `pkg/playlist/parser.go:57` `parseFirstStreamURL`
      returns the first `FileN=` entry regardless of scheme. The latter is
      the one that matters for MITM of audio and ICY titles, since that is
      what `fetchStream` connects to. `security.ValidateURL` allows plain
      http by design (`validation.go:63`); prefer an https `FileN` and
      optionally tighten `ValidateURL` behind a flag.
- [ ] **PSK quality** [S]. Template suggests `psk: "change-me"`
      (`internal/config/config.go:235` and `:258`, mirrored in README);
      `Config.validate` checks only mutual exclusivity; `readPSKFile`
      (`cmd/soma/endpoint.go:96`, reused by the daemon) rejects only an
      empty file and never stats it, while the socket dir *is* checked for
      `0o077` and owner uid in `internal/protocol/socket.go:32`. Add
      `soma daemon --gen-psk` (32 random bytes at 0600, modelled on
      `--show-cert`) plus an SSH-style permissions check in `readPSKFile`.
- [ ] **Release supply chain** [S each, M total] (`release.yml`,
      `ci.yml`). All seven sub-claims hold: no provenance attestation; all
      30 `uses:` are floating tags (`samuelb/homebrew-tap/…@main` is a
      branch); nfpm `.deb` is curl'd with a pinned version but no checksum
      (`release.yml:225`); golangci-lint `version: latest` in both
      workflows; `govulncheck@latest`; `contents: write` at workflow level
      inherited by all six jobs (`prepare` needs it for the bump push and
      `release` for the tag + gh-release, the build jobs do not); govulncheck
      never runs in the release workflow. Add
      `actions/attest-build-provenance`, SHA-pin, checksum, pin versions,
      move permissions to the two jobs that need them, run govulncheck in
      `prepare`.
- [ ] **Compile-only CI matrix for release targets** [S]. `ci.yml` has
      `test` (ubuntu), `test-macos`, `vuln`, `lint`; linux/arm64
      (`ubuntu-24.04-arm` row) and the darwin universal `lipo` build are
      first exercised in `release.yml`. The arm64 row needs a real arm
      runner because `CGO_ENABLED=1`, so it is not a free `GOARCH=` cross
      build.
- [ ] **Play/pause toggle in the TUI** [S]. The `Backend` interface
      (`internal/app/commands.go:14`) has `Status, Channels, Play, Stop,
      SetVolume, ToggleFavorite, Shutdown` and no `PlayPause`, while
      `*client.Client`, the CLI (`soma pause`), MPRIS, the tray, and the
      server all have it. Today the TUI needs `s` then Enter. `p` is unbound
      in both the model and the bubbles list keymap.
- [ ] **Mute toggle** [S] (`m` in the TUI, `soma volume mute`) restoring
      the previous level. Nothing stores a pre-mute level today
      (`internal/state/state.go` keeps volume as a pointer so an explicit 0
      is distinguishable, but that is all). `m` is unbound in both keymaps.
- [ ] **Perceptual volume curve** [S]. Percent maps linearly to amplitude
      end to end: `cli.go:452` `pct/100` → server clamp
      (`playback.go:262`) → `AudioPlayer.SetVolume` (`player.go:396`) →
      oto multiplies samples by it. Most audible change sits in the bottom
      quarter. `AudioPlayer.SetVolume` is the single right place for a
      cubic/exponential mapping; keep `Volume()` returning the un-curved
      target so the wire stays in percent. The fade steps (`player.go:390`,
      `:435`) scale linearly too and should go through the same curve.
- [ ] **Stream quality knob** [S]. No `quality` key exists in any config
      struct (`KnownFields(true)` makes it a hard error today).
      `internal/channels/select.go` `selectBestQuality` always takes the
      lowest rank in `{highest, high, low}`. Add `quality` to `ServerConfig`
      and thread a preferred rank into `SelectPlaylists`.
- [ ] **Finish `--json`** [S] on `play`, `next`, `prev`, `pause`, `stop`,
      `volume`. `parseJSONFlag` (`cli.go:159`) is called only from `runList`,
      `runFavorite`, `runStatus`; `soma play --json` is treated as a channel
      query and `soma volume --json` as a bad number. All six already hold a
      `protocol.PlaybackState`.
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
- [ ] **systemd unit / launchd plist in packages** [S]. `packaging/` has
      only `arch/PKGBUILD`, `homebrew/…`, `macos/build-dmg.sh`, `nfpm.yaml`;
      nfpm ships binary, README, LICENSE, completions and no unit. The
      README recommends a service manager (lines ~239 and ~304). The daemon
      already handles SIGTERM and ignores SIGHUP, so a user-level
      `soma.service` plus a `~/Library/LaunchAgents` plist and two nfpm
      `contents:` entries is all it takes.
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

- [ ] **gofmt drift the linter cannot see**: `gofmt -l` over tracked
      non-vendor Go lists exactly one file, `internal/app/model.go`
      (struct-field alignment at lines 46–51). `.golangci.yml` (v2) has no
      `formatters:` section and `lefthook.yml` runs only `golangci-lint` and
      `go test`, so nothing enforces `make fmt`. Run `gofmt -w`, then add
      `formatters: enable: [gofmt, goimports]`.
- [ ] **README "Keyboard Controls" vs in-app help disagree both ways**
      (`README.md:310`, `NewHelpKeys` in `internal/app/update.go:192`):
      README omits `a` (about) and `n`/`N` (next/prev match), which the help
      shows; `c` (clear search) and `esc` (close about / cancel search) are
      in *neither*, and the `=`/`_` volume aliases are documented nowhere;
      the help omits `Enter`/`Space`, the primary action; README calls `/`
      "Filter channels" while help says "search" (it is a search-and-jump,
      see the P2 search item). Fix both lists.
- [ ] `internal/channels/channels.go:184` warns about a failed cache write
      via a raw `fmt.Fprintf(os.Stderr, ...)` (return value unchecked) while
      the sibling corrupt-cache warnings at `:123`/`:125` use `log.Printf`,
      so this one line lands in `server.log` without a timestamp.
- [ ] **Makefile rot**: `make ci` (`deps test lint build`) lacks the
      coverage gate and govulncheck that CI runs and adds `deps`/`build`
      that CI never runs; `package-deb` calls `packaging/deb/build-deb.sh`,
      which no longer exists (packaging moved to nfpm), and `help` still
      advertises it along with `DEB_ARCH`; `make security` runs a bare
      `gosec ./...` that does *not* apply the `G104` exclusion
      `.golangci.yml` configures for its gosec linter, so the two can
      disagree. Rewire or drop.
- [ ] **AUR** [S, publishing planned]. README:101 already says "Once
      published to the AUR", which matches the plan, so leave the wording.
      The committed `packaging/arch/PKGBUILD` has `pkgver=0.13.0` vs tag
      v0.14.1; the `pkgver()` function overrides it at build time and
      `-X main.version` reads the updated value, so it is a misleading
      placeholder, not a broken build — bump it when publishing.

### Features

- [ ] Sleep timer (`soma stop --in 45m`): `runStop` takes no args today.
      Model it on the idle-exit `time.AfterFunc` in
      `internal/server/server.go:467`. [S–M]
- [ ] Favorites-only view toggle in the TUI. `sortItemsWithFavorites`
      (`internal/app/favorites.go:81`) only partitions favorites first;
      nothing filters. [S–M, shares the matches-only plumbing with search]
- [ ] MPRIS `mpris:artUrl` from the channel image
      (`internal/platform/mpris_linux.go`). `Channel` has `Image`,
      `LargeImage`, `XLImage`, but `updateMPRISLocked` passes only three
      strings, so the URL must be threaded through the server→platform
      boundary and added to both metadata builders (`SetPlaying` and
      `SetMetadata`). More than the "~5 lines" first estimated. [S]
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
- [ ] Fuzz targets: none exist (`func Fuzz` has zero hits). Candidates:
      `parseICYMetadata`/`icyDemuxer` (`internal/audio/metadata.go`), the
      ADTS reader (`internal/audio/adts.go`), `pkg/playlist`. [S]
- [ ] Add `SECURITY.md` (the daemon ships a network listener). [S]

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
