# TODO

Open findings from the 2026-09 code assessment and the Codex reviews of the
`improvements` branch (TLS 1.3, adaptive colors, jitter buffer, AAC playback —
all landed there). Priorities: **P1** = known bugs or real exposure, fix next;
**P2** = high-value improvements; **P3** = polish and nice-to-haves.

## P1 — correctness and security

- [ ] **TCP hardening bundle** (`internal/server/conn.go`): no read deadlines
      anywhere — an unauthenticated peer can hold a connection (goroutine + fd)
      open forever — and no cap on total connections. Add a pre-auth handshake
      deadline (~10 s), an idle deadline after auth, and a max-connection cap.
- [ ] **Auth throttling is per-connection only** (`conn.go:100`): the 1 s
      failure delay costs nothing across parallel connections. Use a shared
      limiter keyed on remote address.
- [ ] **Pre-auth line size**: the protocol scanner allows 4 MiB lines
      (`internal/protocol/codec.go`) before authentication. Cap pre-auth lines
      at a few KB; only hello/auth traffic is legitimate there.
- [ ] **Stop-vs-play supersede window** (`internal/server/playback.go`,
      pre-existing, narrowed but not closed by the per-candidate guard): a Stop
      landing between the last generation check and `player.Play` lets stale
      audio start; the success path detects the supersede but returns without
      stopping the committed session. Needs the server generation coupled to
      the player generation (a stale request must never touch the player, and
      a newer owner must be able to reap its audio).
- [ ] **Crossfade track-title race** (`internal/audio/player.go`): during the
      250 ms crossfade the old session's context is still live, so an ICY
      title from the *old* channel can arrive after `drainTrackUpdates` and be
      displayed under the new channel (`handleTrackUpdate` checks only
      status). Stamp `TrackInfo` with the play generation.
- [ ] **Decoder read errors are invisible mid-stream** (from Codex AAC review,
      applies to MP3 too): a decode failure after `Play` returns surfaces
      nowhere — oto stops pulling, playback goes silent until the 30 s stall
      watchdog trips via buffer backpressure. Route post-construction decoder
      `Read` errors into the player's error channel so reconnect/fallback
      reacts promptly.

## P2 — features and hardening

- [ ] **Track history**: `https://somafm.com/songs/<channel>.json` is already
      inside the host allowlist, and the daemon sees every title change. TUI
      pane (`h`) + `soma history [--json]`.
- [ ] **Linux AAC decoding**: the format-preference plumbing is done
      (`aac_other.go` is the stub); needs a decoder (fdk-aac/faad2 via cgo)
      plus packaging fallout (CI apt, deb/rpm depends, brew, nix, PKGBUILD).
- [ ] **Stream quality knob**: a `quality: low` config for metered
      connections; selection currently always takes the best playlist.
- [ ] **Finish `--json`** on `play`, `next`, `prev`, `pause`, `stop`,
      `volume` (only `list`/`favorite`/`status` have it).
- [ ] **PSK quality**: template suggests `psk: "change-me"`
      (`internal/config/config.go`), nothing warns on weak keys, and PSK file
      permissions go unchecked (`cmd/soma/endpoint.go`) although the socket
      dir is strictly checked. Add `soma daemon --gen-psk` + an SSH-style
      permissions check.
- [ ] **Prefer https stream URLs** (`internal/security/validation.go` allows
      plain http): audio and displayed ICY titles are MITM-able on hostile
      networks; pick the https playlist entry when the channel offers one.
- [ ] **Release supply chain**: provenance attestation
      (`actions/attest-build-provenance`), SHA-pin actions, checksum the
      curl'd nfpm .deb, pin golangci-lint/govulncheck versions, move
      `contents: write` from workflow level down to the `prepare`/`release`
      jobs, run govulncheck in the release job too.
- [ ] **Compile-only CI matrix for release targets**: linux/arm64 and the
      darwin universal (lipo) build are first exercised on release day.
- [ ] **Split `cmd/soma/main.go`** (658 lines): extract a pure
      `resolveDaemonOptions(cfg, args)` (the flag/config precedence merge is
      untestable behind eight `log.Fatalf`s), split `daemon.go`/`tui.go`;
      have `cli.go`'s `runX` return errors instead of `fail()` exiting so
      tests can run in-process. `cmd/soma` is the lowest-covered package.
- [ ] **Mute toggle** (`m` in the TUI, `soma volume mute`) restoring the
      previous level.
- [ ] **Search improvements**: an actual filter mode (matches-only view —
      README already says "filter"), fuzzy matching via the already-vendored
      `sahilm/fuzzy`, and `/` pre-filling the previous query instead of
      resetting it (`internal/app/update.go`).
- [ ] **Perceptual volume curve**: `SetVolume` maps percent linearly to
      amplitude, so most audible change sits in the bottom quarter. Apply a
      cubic/exponential mapping inside the player; keep the wire in percent.
- [ ] **systemd unit / launchd plist in packages**: the README recommends
      running the daemon under a service manager but nothing ships one.
- [ ] **macOS signing + notarization**: the README's
      `xattr -d com.apple.quarantine` workaround is the tell (needs an Apple
      Developer ID, $99/yr — a decision, not just a task).

## P3 — polish

- [ ] Enter on the already-playing channel reconnects the stream
      (`internal/app/update.go`); make it a no-op.
- [ ] Sleep timer (`soma stop --in 45m`; the server already has timers).
- [ ] Favorites-only view toggle in the TUI.
- [ ] Desktop notification on track change (opt-in via config).
- [ ] MPRIS `mpris:artUrl` from the channel image (~5 lines,
      `internal/platform/mpris_linux.go`); GNOME/KDE popups show the logo.
- [ ] Channel detail pane (descriptions truncate to one line; they are the
      discovery mechanism).
- [ ] Mouse support (`tea.WithMouseCellMotion()`).
- [ ] Sort options (listeners/genre/name).
- [ ] TLS 1.3 note: raise the TCP floor is done; consider a protocol
      min/max version range in hello so remote pairs don't need lockstep
      upgrades (`internal/protocol/protocol.go` exact-match).
- [ ] Deduplicate: XDG/darwin base-dir resolution ×3 (`internal/state`,
      `internal/config`, `internal/channels`), favorites `map[string]bool`
      built ×4, `str(*string)` helper ×2.
- [ ] Hoist per-frame lipgloss styles in `internal/ui/delegate.go`; make
      `Model.IsMatch` a set lookup.
- [ ] Fuzz targets for `parseICYMetadata`/`icyDemuxer`, the ADTS reader, and
      `pkg/playlist`.
- [ ] Repo hygiene: `SECURITY.md` (ships a network listener), CONTRIBUTING,
      committed CHANGELOG via git-cliff.
- [ ] Coverage gate: raise CI threshold from 60 % toward actual (~72 %).
- [ ] AUR: README says installable "once published to the AUR" — publish or
      reword; committed `packaging/arch/PKGBUILD` has a stale `pkgver`.
- [ ] `make ci` target drifts from what CI actually runs.
- [ ] Theming via config (palette is now tokenized in `internal/ui/styles.go`,
      so a `theme:` section is straightforward).
- [ ] Scrobbling (Last.fm) — optional integration, larger scope.
