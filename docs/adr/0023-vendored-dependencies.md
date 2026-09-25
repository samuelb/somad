# ADR-0023: Dependencies are vendored

- **Status:** Accepted
- **Date:** 2026-07-06
- **Sources:** 7333b4f (no commit body), `packaging/arch/PKGBUILD` (`-mod=vendor`), `flake.nix`, ee50300

## Context

`vendor/` landed in the same commit as the Nix flake, the AUR PKGBUILD and
the first deb packaging. Distro and Nix builds want a hermetic, offline
source tree, and the project was then expected to depend on a fork of oto
(ADR-0017) that would have to survive dependency updates; that fork was
never adopted (ADR-0017's amendment), and `main` has always vendored
upstream oto. The commit itself states no reason; this record infers it
from what landed together.

## Decision

- `vendor/` is committed. After changing `go.mod`, run
  `go mod tidy && go mod vendor` (`make deps-update` does both after
  upgrading).
- Packaging builds use `-mod=vendor` and `-buildvcs=false`.
- CI's lint job re-runs `go mod tidy && go mod vendor` and fails on any
  diff (added 2026-09-25), because `-mod=vendor` builds never check
  vendored files against `go.sum`; `make fmt` skips `vendor/` for the same
  reason.

## Consequences

- Diffs that touch dependencies are large; review the `go.mod` change, not
  the vendor tree.
- A transitively vendored package (for example `sahilm/fuzzy` through the
  bubbles list filter) is available at zero cost but is not a project
  dependency until first-party code imports it.
