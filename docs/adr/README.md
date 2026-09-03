# Architecture Decision Records

One file per decision, numbered in the order they were written down, not
the order they were made. Each record states the context, the decision,
its consequences, and any alternative that was considered and rejected.
Rejected ideas get a record too, so they are not proposed again without
new information.

Read these before changing the architecture, the wire protocol, the
security model, the audio pipeline, or the release process. When you make
a decision that future work must respect, or reverse one recorded here,
add a record or mark the old one superseded in the same commit. Use
`0000-template.md`.

| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-playback-in-a-background-daemon.md) | Playback runs in a background daemon, shipped in one binary | Accepted |
| [0002](0002-newline-delimited-json-with-full-snapshots.md) | Newline-delimited JSON with full-state snapshots and an exact protocol version | Accepted |
| [0003](0003-tui-holds-no-playback-state.md) | The TUI holds no playback state of its own | Accepted |
| [0004](0004-single-daemon-instance-and-detached-spawn.md) | One daemon instance via a lock file; clients spawn it detached | Accepted |
| [0005](0005-daemon-runs-until-stopped.md) | The daemon runs until stopped explicitly | Accepted (supersedes the two-minute default from 95fb723) |
| [0006](0006-version-skew-restart-only-when-playback-is-interrupted.md) | Restart a version-skewed daemon only at moments that already interrupt playback | Accepted |
| [0007](0007-unix-socket-trusted-by-permissions-tcp-requires-tls-and-psk.md) | The Unix socket is authorized by file permissions; non-loopback TCP requires TLS and a PSK | Accepted |
| [0008](0008-psk-challenge-response.md) | PSK authentication is an HMAC challenge-response; the client always authenticates when a key is configured | Accepted |
| [0009](0009-tls-1-3-only-with-a-long-lived-self-signed-certificate.md) | TLS 1.3 only, with an auto-generated long-lived self-signed certificate and three trust modes | Accepted |
| [0010](0010-outbound-http-restricted-to-somafm.md) | All outbound HTTP goes through a SomaFM host allowlist, with redirects re-validated and bodies capped | Accepted |
| [0011](0011-strict-config-file-supplies-flag-defaults.md) | The config file is strict, supplies flag defaults, and ships fully commented out | Accepted |
| [0012](0012-persisted-state-atomic-writes-and-corrupt-file-quarantine.md) | Persisted state uses atomic writes, quarantines corrupt files, and orders saves by sequence | Accepted |
| [0013](0013-stream-resilience-jitter-buffer-watchdog-reconnect-forever.md) | Stream resilience: jitter buffer, stall watchdog, clean EOF is an error, reconnect forever | Accepted |
| [0014](0014-session-owned-playback-with-generation-counters.md) | One goroutine owns each playback session; the newest Play or Stop always wins | Accepted |
| [0015](0015-icy-metadata-demuxed-from-the-playback-connection.md) | Track titles are demuxed from the playback connection, not fetched separately | Accepted |
| [0016](0016-stream-format-selection-aac-where-decodable-mp3-otherwise.md) | Prefer AAC where the platform decodes it, MP3 elsewhere, always the highest quality; no Linux AAC decoder | Accepted |
| [0017](0017-go-mp3-directly-and-release-the-audio-device-when-idle.md) | Decode MP3 with go-mp3 directly, and release the audio device when idle through an oto fork | Accepted |
| [0018](0018-per-connection-backpressure-and-latest-wins-events.md) | Per-connection request cap and latest-wins event delivery | Accepted |
| [0019](0019-mpris-and-tray-live-in-the-daemon.md) | MPRIS and the system tray live in the daemon process | Accepted |
| [0020](0020-adaptive-palette-following-somafm-no-theming.md) | The palette follows somafm.com and adapts to light and dark terminals; no user theming | Accepted |
| [0021](0021-side-effect-free-completion-and-json-output.md) | Shell completion is cache-only and side-effect free; scripting uses `--json` | Accepted |
| [0022](0022-trunk-based-development-conventional-commits-git-cliff.md) | Trunk-based development, Conventional Commits, and git-cliff-driven releases | Accepted |
| [0023](0023-vendored-dependencies.md) | Dependencies are vendored | Accepted |
| [0024](0024-packaging-nfpm-vcs-pkgbuild-universal-macos-binary.md) | Package with nfpm, keep the PKGBUILD as a VCS build, ship a universal macOS binary in a plain DMG | Accepted |
| [0025](0025-quality-gates-lint-race-vulncheck-coverage-floor.md) | Quality gates: golangci-lint with gosec, race tests everywhere, govulncheck, a 60 % coverage floor | Accepted |
| [0026](0026-keep-the-channel-list-flat.md) | Keep the channel list flat: no detail pane, no sort options, no render-path micro-optimizations | Rejected (the features); the list stays as it is |
| [0027](0027-release-workflow-supply-chain-hardening.md) | Release workflow supply-chain hardening: SHA-pinned actions, pinned tool versions, checksummed nfpm download, minimal permissions, build provenance | Accepted |
| [0028](0028-perceptual-volume-curve.md) | Volume is a linear percent on the wire and a cubic curve at the device | Accepted |
| [0029](0029-macos-signing-and-notarization.md) | macOS releases are signed with a Developer ID and notarized when the secrets exist | Accepted |
