# ADR-0029: macOS releases are signed with a Developer ID and notarized when the secrets exist

- **Status:** Accepted
- **Date:** 2026-09-03
- **Sources:** `.github/workflows/release.yml` `build-darwin`

## Context

The macOS binary and the `.dmg` around it were unsigned, so Gatekeeper
refused every download until the user removed the quarantine attribute.
Signing needs an Apple Developer Program membership (Developer ID
Application certificate) that the project does not have yet, and the
release workflow must keep working without it.

## Decision

- The `build-darwin` job signs and notarizes only when the
  `MACOS_CERTIFICATE_P12` secret is set; otherwise it ships the unsigned
  build exactly as before. The presence check is materialized as a
  job-level `env` because `secrets` cannot be read in a job-level `if`.
- Required repository secrets once the identity exists:
  - `MACOS_CERTIFICATE_P12`: the Developer ID Application certificate
    with its private key, exported as `.p12` and base64-encoded;
  - `MACOS_CERTIFICATE_PASSWORD`: the export password of that file;
  - `APPLE_ID`, `APPLE_TEAM_ID`, `APPLE_APP_PASSWORD`: the Apple ID, its
    ten-character team ID, and an app-specific password for
    `notarytool`. An App Store Connect API key would work too; the
    app-specific password needs no extra role setup, so it is the default.
- The certificate is imported into a throwaway keychain that a
  `if: always()` step deletes. The binary is signed with the hardened
  runtime and a secure timestamp (both required by notarization; the
  Cocoa tray needs no entitlements). The `.dmg` is then signed, submitted
  with `notarytool submit --wait`, stapled, and assessed with `spctl`.
- Only the `.dmg` is submitted. Notarization registers a ticket for every
  code object inside the image, so the bare `soma_darwin_universal`
  download passes Gatekeeper online as well; a bare Mach-O cannot carry a
  stapled ticket, which is why the image is the recommended download.
- The README keeps the `xattr -d com.apple.quarantine` instructions for
  builds that are not notarized: source builds, and releases made before
  the secrets were configured.

## Consequences

- Turning signing on is a matter of adding the five secrets; no workflow
  change is needed. Dry runs exercise the signing steps too when the
  secrets exist.
- The `.dmg` is the only artifact with an offline-verifiable ticket.

## Rejected alternatives

- Wrapping the binary in an `.app` bundle or `.pkg` so it can be stapled:
  soma is a command-line tool that users copy onto their `PATH`
  (ADR-0024); an installer package would only get in the way.
- Ad-hoc signing (`codesign -s -`): it silences one warning but does not
  pass Gatekeeper for downloaded files, so it buys nothing over unsigned.
