# AGENTS.md

Guidance for coding agents working in this repository. Facts below are
verified against the tree; when code and this file disagree, fix this file
in the same commit.

## Project

Soma is a SomaFM internet radio client for Linux and macOS, written in Go.
Playback runs in a background daemon so music keeps playing after the TUI
exits. Go module and package name: `somad`. Binary and command: `soma`.

## Commands

```sh
make build              # ./soma, version embedded via ldflags
make test               # go test -race ./...  (~10 s with a warm build cache)
make lint               # golangci-lint run ./...  (config: .golangci.yml, gosec enabled)
make check              # lint + test + vet — run before committing
go test -race ./internal/server/ -run TestName   # single test
make fmt                # gofmt -s (+ goimports if installed)
make site               # stage the website (site/ + demo.gif) into dist/site
```

- Tests need no network, audio device, or display: HTTP goes to `httptest`
  servers allowlisted via `internal/security/securitytest`, audio uses a
  fake `outputPlayer`.
- Dependencies are vendored. After changing `go.mod`: `go mod tidy && go mod vendor`.
- Linux builds need `libasound2-dev`; macOS needs nothing extra.
- lefthook (`lefthook.yml`) runs lint and race tests on pre-commit and
  pre-push. CI (`.github/workflows/ci.yml`) additionally runs `govulncheck`
  and fails below 60 % total coverage on the Linux job.
- Platform-conditional code uses build-tagged `_linux.go` / `_darwin.go` /
  `_other.go` pairs. Keep every side in sync when changing such interfaces.

## Workflow

- **Trunk-based.** Commit directly to `main`. No feature branches, no PRs.
- **Conventional Commits.** `feat:`, `fix:`, `perf:`, `refactor:`, `docs:`,
  `test:`, `chore:`, `ci:`, `build:`; `!` marks a breaking change. git-cliff
  (`cliff.toml`) generates release notes and derives the next version
  (breaking → major, `feat:` → minor, else patch), so prefixes decide
  version numbers. Unprefixed commits land under a generic "Other" heading.
- **Releases** are the manually dispatched Release workflow
  (`.github/workflows/release.yml`, inputs `bump` and `dry_run`).
- **Website** (https://samuelb.github.io/somad/) is a single hand-written
  page in `site/` (`index.html`, `style.css`, `favicon.svg`; no generator,
  no build) that presents the software, installation, and usage only. The
  Website workflow (`.github/workflows/website.yml`) deploys it to GitHub
  Pages on every push to `main` that touches `site/` or `demo.gif`. The
  page fetches the latest release tag from the GitHub API at view time.
  Keep its Install and Usage sections in sync with the README.
- **Decisions** live in `docs/adr/` (index: `docs/adr/README.md`). Read the
  relevant records before changing the architecture, wire protocol,
  security model, audio pipeline, or release process. When a change makes
  a decision future work must respect, or reverses one, add a record from
  `docs/adr/0000-template.md` or mark the old one superseded in the same
  commit. Rejected ideas get a record too. Which records cover what:
  - daemon lifecycle and spawning: 0001, 0004, 0005, 0006
  - wire protocol: 0002, 0018
  - TUI: 0003, 0020, 0026
  - security (socket, TCP, TLS, PSK, outbound HTTP): 0007–0010
  - config and persisted state: 0011, 0012
  - audio pipeline and formats: 0013–0017
  - MPRIS and tray: 0019
  - CLI scripting and completion: 0021
  - process, vendoring, packaging, quality gates: 0022–0025
  - website: 0027
- **Open work** is in `TODO.md`, grouped P1/P2/P3 with effort tags; its
  "Not planned" section only points at ADRs. Remove an item when you finish
  it.

## Where facts live (keep in sync)

| Fact | Source of truth | Also stated in |
|------|-----------------|----------------|
| CLI commands and flags | `printUsage` in `cmd/soma/main.go` | README "Commands", `site/index.html` "Usage" |
| Config keys | `internal/config/config.go` (structs + template text) | README "Configuration", `printUsage` example, `site/index.html` "Configuration" |
| Keyboard controls | keymap in `internal/app/update.go` | README "Keyboard Controls" (`<kbd>` tables), `site/index.html` "Keyboard controls" |
| Installation instructions | README "Installation" | `site/index.html` "Install" |
| Wire protocol | `internal/protocol/protocol.go`, `types.go` | ADR 0002 |
| Build and architecture | this file | — |

## Quick reference

**Subcommands** (`cmd/soma/main.go`): none → TUI; `daemon [flags]` runs the
playback server in the foreground (`daemon stop` shuts it down); `play`,
`list`, `favorite`/`fav`, `next`, `prev`, `pause`, `stop`, `status`,
`volume`, `completion <bash|zsh>`, `version`. Global connection flags
(`--server`, `--tls`, `--tls-ca`, `--tls-fingerprint`, `--psk-file`) go
before the command; daemon flags (`--idle-timeout`, `--no-tray`, `--listen`,
`--tls`, `--tls-cert`, `--tls-key`, `--psk-file`, `--insecure`,
`--show-cert`) go after it. `list`, `favorite`, `status` take `--json`.

**Environment**: `SOMAD_SOCKET` (socket path), `SOMAD_SERVER` (host:port,
like `--server`), `XDG_CONFIG_HOME` / `XDG_STATE_HOME` / `XDG_CACHE_HOME`
(honored on both platforms; use them to isolate manual runs).

**Config keys** (`internal/config`, YAML, all optional pointers; unknown keys
and parse errors are fatal by design): `server.{idle_timeout, tray, listen,
tls, tls_cert, tls_key, psk, psk_file, insecure}`, `client.{server, tls,
tls_ca, tls_fingerprint, psk, psk_file}`, `tui.shutdown_on_exit`. Config
supplies flag defaults; explicit flags win.

**Directories**: config `~/.config/somad/` (Linux) or
`~/Library/Application Support/somad/` (macOS); state (favorites, last
channel, volume, `server.log`, generated `tls-cert.pem`/`tls-key.pem`)
`~/.local/state/somad/` or the same macOS dir; cache `~/.cache/somad/` or
`~/Library/Caches/somad/`; socket `$XDG_RUNTIME_DIR/somad.sock` or a
per-user temp dir on macOS.

**RPC methods** (`internal/protocol/protocol.go`): `authChallenge`, `auth`,
`hello`, `status`, `channels`, `play`, `playPause`, `playRelative`, `stop`,
`setVolume`, `toggleFavorite`, `shutdown`. Events: `state`, `channels`.
`protocol.Version` (currently 1) must match exactly between client and
server; bump it on any incompatible wire change.

**Test helpers**: `internal/server/helpers_test.go` has `newTestServer`,
`newMockPlayer`, `connect` (a raw wire client with `call`, `hello`,
`waitState`, `waitChannels`). `internal/app/helpers_test.go` has
`newTestModel`, `fakeBackend`, `runCmd`. `cmd/soma/cli_e2e_test.go` has
`fakeDaemon` for CLI-over-the-wire tests. `internal/audio/player_test.go`
has `fakeAudioContext`. `internal/client` tests shrink `restartWait`.

**Without lefthook installed** the git hooks do not run; `make check` is
the equivalent, so run it before committing.

## Architecture

Two processes, one binary. The TUI and CLI never touch audio directly;
everything goes through the daemon over the wire protocol.

**Wire protocol** (`internal/protocol`): newline-delimited JSON over a Unix
domain socket (`SocketPath()` in `socket.go`) or, when configured, TCP with
optional TLS and pre-shared-key auth. Clients send `Request`s; the server
replies with ID-correlated `Response`s and pushes `Event`s carrying full
state snapshots. `auth.go` holds the HMAC challenge–response used for PSK
auth on TCP only; the Unix socket is guarded by file permissions.

**Daemon** (`internal/server`): owns audio playback, the channel catalog,
persisted state, MPRIS, and the tray icon. `spawnlock.go` ensures a single
instance. It runs until stopped explicitly unless `--idle-timeout` /
`server.idle_timeout` is set. `Run` accepts multiple listeners; `conn.go`
gates non-local connections behind auth when a PSK is configured.

**Client** (`internal/client`): protocol client shared by TUI and CLI. An
`Endpoint` (Unix socket, or TCP with optional `tls.Config` and PSK) is
resolved in `cmd/soma/endpoint.go` from flags, `$SOMAD_SERVER`, and the
config file. `spawn.go` auto-spawns a local daemon when none is running and
handles version skew: a daemon whose version differs from the client's is
restarted onto the new binary only at a moment that already interrupts
playback (channel change, pause, stop), never mid-song. Remote endpoints
are never spawned or restarted.

**TUI** (`internal/app` + `internal/ui`): Bubble Tea Elm architecture
(`model.go`, `update.go`, `view.go`, `commands.go`). The model holds no
playback state; it renders the latest server snapshot and sends commands
through its `Backend` interface (`commands.go`). `internal/ui` has the list
delegate and lipgloss styles.

**Supporting packages**:
- `internal/audio` — stream playback via oto: MP3 through go-mp3
  everywhere, AAC through macOS AudioToolbox (`aac_darwin.go` /
  `aac_other.go`), format preference in `PreferredFormats`, ICY metadata,
  jitter buffer, stall watchdog, reconnection
- `internal/channels` — SomaFM catalog fetch/cache, selection by ID or name
- `internal/state` — persisted user state; atomic writes, corrupt-file quarantine
- `internal/config` — strict YAML config file, supplies flag defaults
- `internal/security` — all outbound HTTP goes through
  `security.NewRequest` / `ValidateURL`, which allowlist SomaFM hosts and
  re-validate redirects; tests add hosts via `securitytest`
- `internal/tlsutil` — self-signed cert generation (persisted in the state
  dir) and client trust via CA file, pinned SHA-256 fingerprint, or system roots
- `internal/platform` — MPRIS (`mpris_linux.go` / `mpris_other.go`) and `tray/`
- `internal/atomicfile` — temp-file + rename writes used by state and cache
- `pkg/playlist` — PLS playlist parsing
