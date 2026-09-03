# Security Policy

## Supported versions

Somad ships one line of development: only the latest
[release](https://github.com/samuelb/somad/releases) is supported. There are
no maintained older branches — upgrade to the latest release before
reporting an issue, and expect fixes to land there rather than be
backported.

## Reporting a vulnerability

Please report suspected vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/samuelb/somad/security/advisories/new)
for this repository, rather than filing a public issue. If that route isn't
available to you, GitHub also accepts advisories via the "Security" tab of
the repository (https://github.com/samuelb/somad/security). Include
reproduction steps, affected version (`soma --version`), and the impact you
expect.

We don't currently commit to a fixed response SLA (this is a personal
project maintained on a best-effort basis), but reports are taken seriously
and a fix or mitigation is prioritized over other work.

## Threat model, in brief

Somad's daemon (`soma daemon`) controls audio playback and, when configured
for remote control, listens on the network. The security model, in short:

- **Local control (Unix domain socket)** is authorized by filesystem
  permissions, not a credential: the socket directory is created `0700` and
  owner-checked, so the OS user boundary is the trust boundary. See
  [ADR-0007](docs/adr/0007-unix-socket-trusted-by-permissions-tcp-requires-tls-and-psk.md).
- **Remote control (TCP)** requires both TLS and a pre-shared key by
  default; the daemon refuses to start a non-loopback listener without
  them. TLS is 1.3-only with an auto-generated (or supplied) certificate
  ([ADR-0009](docs/adr/0009-tls-1-3-only-with-a-long-lived-self-signed-certificate.md));
  the PSK is verified with an HMAC challenge-response so it never crosses
  the wire in the clear
  ([ADR-0008](docs/adr/0008-psk-challenge-response.md)). Pass `--insecure`
  (or `server.insecure` in the config file) to explicitly opt out on a
  trusted network — see
  [ADR-0007](docs/adr/0007-unix-socket-trusted-by-permissions-tcp-requires-tls-and-psk.md)
  for what that trades away.
- **Outbound HTTP** (channel catalog, playlists, streams) is restricted to
  an allowlist of SomaFM hosts, with redirect targets re-validated against
  the same allowlist and response bodies capped, to close off SSRF-shaped
  abuse of attacker-influenceable URLs. See
  [ADR-0010](docs/adr/0010-outbound-http-restricted-to-somafm.md).

Read the ADRs above (and the rest of `docs/adr/`) for the full reasoning
and any since-superseded decisions.

## Existing gates

Every change is checked by:

- **gosec**, run as a `golangci-lint` linter (`make lint` /
  `.golangci.yml`) on every commit and in CI.
- **[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)**,
  run in CI (`.github/workflows/ci.yml`) against the module's dependency
  graph.

Neither gate is a substitute for a report — please still tell us about
anything they miss.
