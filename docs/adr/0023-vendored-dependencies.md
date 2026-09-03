# ADR-0023: Dependencies are vendored

- **Status:** Accepted
- **Date:** 2026-07-06
- **Sources:** 7333b4f (no commit body), `packaging/arch/PKGBUILD` (`-mod=vendor`), `flake.nix`, ee50300

## Context

`vendor/` landed in the same commit as the Nix flake, the AUR PKGBUILD and
the first deb packaging. Distro and Nix builds want a hermetic, offline
source tree, and the project depends on a fork of oto (ADR-0017) that
must survive dependency updates. The commit itself states no reason; this
record infers it from what landed together.

## Decision

- `vendor/` is committed. After changing `go.mod`, run
  `go mod tidy && go mod vendor`.
- Packaging builds use `-mod=vendor` and `-buildvcs=false`.

## Consequences

- Diffs that touch dependencies are large; review the `go.mod` change, not
  the vendor tree.
- A transitively vendored package (for example `sahilm/fuzzy` through the
  bubbles list filter) is available at zero cost but is not a project
  dependency until first-party code imports it.
