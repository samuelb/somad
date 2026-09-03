# ADR-0024: Package with nfpm, keep the PKGBUILD as a VCS build, ship a universal macOS binary in a plain DMG

- **Status:** Accepted
- **Date:** 2026-07-13 (universal binary 2026-07-06)
- **Sources:** bcc819e, de2e067, c82d8fc, 3a60bcd; `packaging/nfpm.yaml`, `packaging/arch/PKGBUILD`, `packaging/macos/build-dmg.sh`, `scripts/stage-arch-release.sh`

## Context

A hand-rolled `build-deb.sh`, an SSH-based AUR auto-publish, and an inline
Homebrew tap push each needed their own maintenance and secrets. The Nix
package version had gone stale at 0.9.0. macOS shipped two architecture
specific binaries.

## Decision

- `.deb` and `.rpm` for amd64 and arm64 are built with nfpm from one
  `packaging/nfpm.yaml`; the deb declares `replaces`/`conflicts` on the old
  `somatui` name.
- The committed PKGBUILD is a generic VCS build (`pkgver()` from
  `git describe`, checksums skipped). The release pipeline renders a pinned
  copy with a real checksum and attaches it to the GitHub release. The AUR
  auto-publish and its SSH secret were removed; publishing to the AUR is
  planned but manual.
- The Homebrew formula is rendered from a checked-in template. The tap repo
  is `homebrew-tap`.
- `flake.nix` is bumped by `scripts/bump-version.sh` in the prepare job so
  it cannot go stale.
- macOS ships one universal binary merged with `lipo`, inside a DMG that
  carries the raw binary plus docs rather than an `.app` bundle, because
  soma is a CLI-first tool that users copy onto their `PATH`.

## Consequences

- linux/arm64 and the universal build are exercised only on release day
  (a compile-only CI matrix is an open item).
- The binary is unsigned; signing and notarization are planned and need an
  Apple Developer ID first (TODO.md).
- No service unit is packaged yet although the README recommends a service
  manager (TODO.md).
