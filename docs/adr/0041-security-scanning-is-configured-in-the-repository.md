# ADR-0041: Security scanning is configured in the repository: CodeQL as a workflow, Dependabot in `dependabot.yml`

- **Status:** Accepted
- **Date:** 2026-11-11
- **Sources:** `.github/workflows/codeql.yml`, `.github/dependabot.yml`; a
  review of the repository's code security settings on 2026-11-11

## Context

GitHub's code security features can be set up in two places: the
repository settings, whose state lives on GitHub with no history in the
repository, or files committed to it. CodeQL had been turned on in the
settings ("default setup"), which picks its languages by itself, Ruby
among them for the one Homebrew formula template. Dependabot's version
updates were already in `.github/dependabot.yml`, but some of its options,
grouping security updates and ignoring a dependency, can also be set in
the settings or with `@dependabot` comments on a pull request. Its commit
messages followed whatever it inferred from recent history, which had
given `chore(deps):` and bare `Bump …` titles as well as `build(deps):`.

## Decision

- Whatever a file in the repository can configure is configured there, not
  in the repository settings or with `@dependabot` comment commands. The
  settings keep only what no file can express: turning Dependabot alerts,
  Dependabot security updates, secret scanning, push protection and
  private vulnerability reporting on or off, and dismissing alerts.
- CodeQL runs as advanced setup, `.github/workflows/codeql.yml`, on pushes
  and pull requests to `main` and every Monday, over Go (autobuild), the
  GitHub Actions workflows, and the website's script. Ruby is left out:
  the formula template is all there is of it. Each analysis keeps default
  setup's category (`/language:<language>`), so the alerts raised before
  the switch carry over. The job's token adds only `security-events:
  write`, to upload the results, and its actions are SHA-pinned
  (ADR-0027).
- Dependabot waits seven days after a release before proposing it as a
  version update (`cooldown`; GitHub's own default is three), since a
  compromised release is usually caught and pulled within that time.
  Security updates ignore the cooldown and are not grouped: each fix
  arrives as its own pull request.
- Dependabot's commit messages are pinned to `build(deps):` for Go modules
  and `ci(deps):` for actions, so git-cliff files them under Miscellaneous
  rather than "Other" (ADR-0022).
- The settings, in turn, have on: Dependabot alerts and security updates,
  secret scanning with push protection, private vulnerability reporting,
  immutable releases, and the Actions policy that every action be pinned
  to a full commit SHA. Two rulesets, with no bypass: `v*` tags cannot be
  updated or deleted, and `main` cannot be force-pushed or deleted.

## Consequences

- Default setup must stay off: GitHub refuses the workflow's results while
  it is on.
- CodeQL overlaps with gosec (ADR-0025) but does not read `#nosec`, so a
  finding already justified there is dismissed in its alert with the same
  reason. Those dismissals live on GitHub.
- CodeQL builds the Go code for Linux, so it does not analyze the
  `_darwin.go` files.
- A version update arrives a week after its release at the earliest.
- A `uses:` that is not pinned to a full commit SHA fails its workflow,
  and so does one inside a workflow it calls: `samuelb/homebrew-tap`'s
  `publish.yml`, which the release calls, pins its own actions, and its
  own `dependabot.yml` keeps them current. This repository's pin of that
  workflow is still moved by hand (ADR-0027).
- A release is never fixed by moving or re-creating its tag, nor by
  replacing its files once published; it takes a new version. The release
  action uploads to a draft and publishes last, as immutable releases
  require, so a step added after it must not change the published release.
  To move a tag anyway, the ruleset has to be switched off.
- The `main` ruleset has no required checks or pull requests: those would
  stop the direct pushes of ADR-0022 and the release workflow's
  version-bump push.
- To ignore a dependency, add an `ignore` entry to `dependabot.yml`. That
  replaces any ignore conditions that comment commands stored for the
  ecosystem; none had been set when this was decided.

## Rejected alternatives

- **CodeQL default setup** (turned on 2026-11-11, replaced the same day).
  It needs no file and picks up new languages by itself, but its
  configuration is visible only in the settings.
- **Moving `HOMEBREW_TAP_TOKEN` into an environment limited to `main`**,
  as `AUR_SSH_PRIVATE_KEY` is (2026-11-11). A job that calls a reusable
  workflow cannot name an environment, so the environment would have to
  be declared in `samuelb/homebrew-tap`'s `publish.yml`, which
  `samuelb/whirr`'s release shares; the token would have to be
  regenerated, since a secret's value cannot be read back; and only a
  real release would test it. The gain is small: the token is a
  fine-grained one, limited to the tap, and the only workflows outside
  `main` that could read a repository secret are ones the maintainer
  pushes, since neither Dependabot nor pull requests from forks get them.
