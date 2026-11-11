# ADR-0040: The release workflow publishes the `somad` AUR package

- **Status:** Accepted
- **Date:** 2026-11-11
- **Sources:** `.github/workflows/release.yml` `aur` and `publish-aur`, `packaging/arch/PKGBUILD`, `scripts/stage-arch-release.sh`; supersedes the manual-AUR part of ADR-0024

## Context

ADR-0024 removed the SSH auto-publish to the AUR and left publishing to
the AUR "planned but manual". Nobody did it by hand, so Arch users could
only download the `PKGBUILD` from a release. The removed job had written
its `.SRCINFO` from a heredoc beside a second copy of the `PKGBUILD`, so
the two could drift, and it trusted whatever host key `ssh-keyscan`
returned. The `PKGBUILD` itself built a binary that namcap flagged: no
PIE, no full RELRO, an empty debug package, the build directory in the
binary, and no systemd user unit, which the deb and rpm do install.

## Decision

- Every real release publishes the pinned `PKGBUILD` that
  `stage-arch-release.sh` renders to the AUR as `somad`: the latest
  release, built from the release's source tarball. That is what the AUR
  expects under the plain name; a `-git` package (the `main` branch) or a
  `-bin` package (the prebuilt binaries) would each be a separate package
  with its own suffix, and neither is published for now.
- The work is split as the Homebrew tap's is. The `release` job uploads
  the pinned `PKGBUILD` as an artifact. The `aur` job, holding a read-only
  token and no secrets, renders `.SRCINFO` with `makepkg --printsrcinfo`
  in a digest-pinned `archlinux` container, so the two cannot disagree. On
  a real run it also runs `makepkg --verifysource` against the published
  release, so the AUR never gets a source URL or checksum that does not
  match. The `publish-aur` job runs nothing but git and ssh: it clones the
  AUR repository, commits the two files on `master`, and pushes.
- The push key is an Ed25519 key used for nothing else, kept as the
  `AUR_SSH_PRIVATE_KEY` secret of the `aur` environment, which only
  `publish-aur` names and which deploys from `main` only. The key can push
  every package of the AUR account it is registered with. A copy is in the
  maintainer's password store. Without the secret the job warns and
  skips, as signing does without its secrets (ADR-0029). AUR's Ed25519
  host key is pinned in the workflow, checked against the fingerprint
  aur.archlinux.org publishes on its home page.
- The `PKGBUILD` follows the Arch Go package guidelines: `-buildmode=pie`,
  `-linkmode=external` with makepkg's `CFLAGS`/`LDFLAGS` passed to cgo,
  `-trimpath`, no `-s -w` (makepkg strips and splits the debug package),
  the build date from `SOURCE_DATE_EPOCH`, `glibc` among the dependencies,
  and the systemd user unit installed.

## Consequences

- A release reaches Arch users through the AUR without a manual step.
  A re-run publishes nothing new, since an unchanged `PKGBUILD` and
  `.SRCINFO` make no commit. Re-running an old release's run would push
  that release over a newer one.
- The `PKGBUILD` is built by nothing but the user's makepkg: CI does not
  build it, so a change to it should be tried with `makepkg` in an Arch
  container before a release (`build()` and `check()` take a few minutes).
- The `archlinux` image digest does not move by itself, since Dependabot
  does not track images named in `run:` steps. `--printsrcinfo` and
  `--verifysource` need nothing newer; re-pin it when makepkg's output
  format changes.
- Rotating the key means a new key in the environment secret and in the
  AUR account's SSH keys, which take one key per line.

## Rejected alternatives

- **A third-party "deploy to AUR" action.** It would run with the AUR key
  in its environment; the git and ssh it wraps are a dozen lines here.
- **Writing `.SRCINFO` in `stage-arch-release.sh`.** It needs no container,
  but it is a second rendering of the same fields that has to be kept in
  step with the `PKGBUILD` by hand, as the removed job's was.
- **Rendering `.SRCINFO` in the `release` job.** That job can push tags and
  mint OIDC tokens, and the container is third-party code (ADR-0027).
