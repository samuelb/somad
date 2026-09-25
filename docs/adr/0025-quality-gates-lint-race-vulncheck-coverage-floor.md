# ADR-0025: Quality gates: golangci-lint with gosec, race tests everywhere, govulncheck, a 60 % coverage floor

- **Status:** Accepted
- **Date:** 2026-07-05 (hooks 2026-02-09, macOS race 2026-07-08)
- **Sources:** 9489d5e, 658f3b6, 8fbbe1d, 7ba2316, 3596858; `.golangci.yml`, `lefthook.yml`, `.github/workflows/ci.yml`; rejection of a coverage ratchet on 2026-09-03

## Context

Coverage was displayed but never enforced and could regress silently. The
macOS job ran tests without `-race` while Linux used it, so a race that
only surfaces on the darwin scheduler could pass. govulncheck installed
the `go.mod` floor toolchain, whose stdlib carried already-fixed CVEs.

## Decision

- `golangci-lint` with twenty linters including gosec; gosec `G104` is
  excluded because the code uses explicit `_ =` for best-effort cleanup,
  and per-site `#nosec` annotations carry their own reasons.
- lefthook runs lint and `go test -race` on pre-commit (Go files only) and
  pre-push (always); `--no-verify` is the documented WIP escape hatch.
- CI runs `-race` on both Linux and macOS, runs govulncheck with
  `check-latest` on a patched toolchain, and fails if total coverage drops
  below 60 %, "comfortably below the current" value at the time.
  - *Amendment 2026-09-25.* `check-latest` cannot move an exact patch
    version, so CI and the release kept building on `go 1.25.13` after Go
    1.27 had taken the 1.25 line out of support. `go.mod` now carries a
    `toolchain` line (go1.27.1), which `actions/setup-go` prefers over the
    `go` directive; the `go` line stays the minimum for source builds.
    govulncheck runs against that pinned toolchain, so a standard-library
    fix it lacks fails CI and prompts the bump, and `check-latest` is gone.
- Test speed is a design concern: fsync-heavy persistence is injectable so
  the suite runs in well under a second where it took 29 s.

## Consequences

- gofmt drift was invisible to the hooks and CI until 2144e6a enabled the
  gofmt and goimports formatters in golangci-lint.
- `make ci` had drifted from what CI runs until 6283a1b made it mirror
  CI again.

## Rejected alternatives

- **Raising the coverage floor toward the actual value** (2026-09-03,
  actual was 73.1 %). A ratchet adds friction on every commit for no
  concrete gain; the floor exists to catch large regressions.
