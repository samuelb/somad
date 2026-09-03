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
- Test speed is a design concern: fsync-heavy persistence is injectable so
  the suite runs in well under a second where it took 29 s.

## Consequences

- No formatter is enabled in golangci-lint, so gofmt drift is invisible to
  the hooks and CI (open item in TODO.md).
- `make ci` has drifted from what CI runs (TODO.md).

## Rejected alternatives

- **Raising the coverage floor toward the actual value** (2026-09-03,
  actual was 73.1 %). A ratchet adds friction on every commit for no
  concrete gain; the floor exists to catch large regressions.
