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

## Consequences

- Default setup must stay off: GitHub refuses the workflow's results while
  it is on.
- CodeQL overlaps with gosec (ADR-0025) but does not read `#nosec`, so a
  finding already justified there is dismissed in its alert with the same
  reason. Those dismissals live on GitHub.
- CodeQL builds the Go code for Linux, so it does not analyze the
  `_darwin.go` files.
- A version update arrives a week after its release at the earliest.
- To ignore a dependency, add an `ignore` entry to `dependabot.yml`. That
  replaces any ignore conditions that comment commands stored for the
  ecosystem; none had been set when this was decided.

## Rejected alternatives

- **CodeQL default setup** (turned on 2026-11-11, replaced the same day).
  It needs no file and picks up new languages by itself, but its
  configuration is visible only in the settings.
