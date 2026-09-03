# ADR-0022: Trunk-based development, Conventional Commits, and git-cliff-driven releases

- **Status:** Accepted
- **Date:** 2026-07-13 (auto-bump 2026-07-14, branching rule 2026-09-03)
- **Sources:** bcc819e, 1b90bdf, def624b, 370e1be; `AGENTS.md`, `cliff.toml`, `.github/workflows/release.yml`; rejection of CONTRIBUTING/CHANGELOG on 2026-09-03

## Context

Releases were first triggered by tags, then by a workflow run after CI,
then by a manual version input. Each needed a human to pick a number.
Release notes were hand-written or absent.

## Decision

- Changes land directly on `main`. No feature branches, no pull requests.
- Commit subjects follow Conventional Commits. git-cliff generates release
  notes from them (`cliff.toml`, kept structurally identical to the sibling
  project's); non-conforming commits fall through to an "Other" heading
  rather than being dropped, which matters for pre-convention history.
- The manually dispatched Release workflow computes the next version with
  `git-cliff --bump` (breaking → major, `feat:` → minor, else patch),
  overridable with a `bump` input, and a concurrency group prevents two
  releases racing the computation. `dry_run` builds everything without
  pushing, tagging or releasing.

## Consequences

- Commit prefixes affect version numbers, so they are not optional.
- Agent guidance lives in the vendor-neutral `AGENTS.md`.

## Rejected alternatives

- **CONTRIBUTING.md and a committed CHANGELOG.md** (2026-09-03). This is a
  solo trunk-based project; AGENTS.md is the contributor document and
  git-cliff renders the changelog at release time, so a committed copy
  would only drift.
