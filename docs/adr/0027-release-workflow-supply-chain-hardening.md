# ADR-0027: Release workflow supply-chain hardening

- **Status:** Accepted
- **Date:** 2026-09-03
- **Sources:** TODO.md assessment of 2026-09-03; `.github/workflows/release.yml`, `.github/workflows/ci.yml`

## Context

All 30 `uses:` references across `release.yml` and `ci.yml` floated on a
major-version tag (`samuelb/homebrew-tap/…@main` floated on a branch), so a
compromised upstream tag or branch head would run in this repo's release
pipeline with `contents: write` and repo secrets on the next dispatch.
`golangci-lint` and `govulncheck` were installed at `latest`/`@latest`,
so a run could pick up a new tool version with no warning. The nfpm `.deb`
used to bootstrap the packaging step was curl'd by pinned version but its
download was never checksummed. `contents: write` was granted at the
workflow level, so all six jobs held it even though only `prepare` (the
version-bump commit) and `release` (the tag push and GitHub Release) use
it. No release artifact carried build provenance.

## Decision

- Every `uses:` in both workflows is pinned to a full commit SHA with a
  trailing `# vN` (or `# main`, for the homebrew-tap reusable workflow)
  comment recording the ref it was resolved from, so a `git blame` or diff
  shows what moved when re-pinning to a new version.
- `golangci-lint` (`version:`) and `govulncheck` (`go install …@`) are
  pinned to a specific version in both workflows instead of `latest`;
  bumping either is a deliberate, reviewable diff.
- The nfpm `.deb` download in the `release` job is checksummed against the
  sha256 published in that nfpm release's `checksums.txt` before `dpkg -i`
  runs it as root.
- `permissions: contents: write` moved off the workflow level and onto
  only the `prepare` and `release` jobs; every other job gets an explicit
  `contents: read`. `release` additionally gets `id-token: write` and
  `attestations: write` for the provenance step.
- `release` runs `actions/attest-build-provenance` over `dist/*` after
  checksums are generated, but only `if: inputs.dry_run == false` —
  attesting a dry run would create a permanent, publicly verifiable
  Sigstore record for artifacts that were never actually released.
- `prepare` now runs `govulncheck` (it previously only ran lint and tests),
  so a release can't ship a version the `vuln` CI job would have failed on
  a rebase-flavored branch.

## Consequences

- Bumping a pinned action means resolving a new SHA (`gh api
  repos/OWNER/REPO/commits/TAG`), not just editing a version number; the
  `# vN` comment is what makes that diff reviewable.
- The homebrew-tap reusable-workflow pin (`@main`) goes stale the moment
  that repo's `main` moves; it does not auto-track new commits the way a
  floating `@main` did. It must be re-pinned by hand when the tap workflow
  changes.
- The nfpm checksum is version-specific: bumping `NFPM_TOOL_VERSION` means
  updating `NFPM_DEB_SHA256` from that release's `checksums.txt` in the
  same change.

## Rejected alternatives

- **Attesting on dry runs too.** Rejected: it would create verifiable
  provenance for binaries that were built but never published, which is
  confusing to anyone later trying to match an attestation to a release.
