# Developer Guide

This repo is a standalone Go sync utility with a generic core and a Patreon-first MVP.

## Code Shape

- `internal/app` owns orchestration.
- `internal/provider` defines provider seams.
- `internal/store` defines repository seams.
- `internal/store/sqlite` is the current persistence backend.
- `internal/config` compiles the authored rules, inputs, review rules and overrides into one read-only rule set behind `Config.Compiled`.
- `internal/classify`, `internal/discovery` and `internal/sequence` decide and detect sequence numbers; `internal/app/decisions.go` is the one release decider.
- `internal/artifact` handles canonical artifact materialization; every edit to a reading copy's package document runs in one package edit session.
- `internal/publish` hosts the filesystem and exec target adapters behind one `Target` seam; `internal/app/transitions.go` plans each target's delivery once.

## Working Locally

Use the Go version from `go.mod`. For native tests install poppler (`brew install
poppler` on macOS or `apt-get install poppler-utils` on Debian) and put a JDK 11+
with `java` and `javac` first on `PATH`. On macOS also install coreutils
(`brew install coreutils`) for the installer's `sha256sum`, plus `unzip` and `curl`
if unavailable. On Debian install `unzip` and `curl` alongside the JDK and poppler.
Then install the pinned EPUBCheck and
serial-sync's custom long-lived validator into a user-owned location:

```sh
BIN_DIR="$HOME/.local/bin" scripts/install-epubcheck "$HOME/.local/share/epubcheck"
export PATH="$HOME/.local/bin:$PATH"
go test ./...
go run ./cmd/serial-sync --config ./examples/config.demo.toml setup check
```

The script replaces its install directory; use a dedicated path. A distro
`epubcheck` command alone is insufficient: artifact tests invoke
`serial-sync-epubcheck`. PDF conversion uses poppler's `pdftohtml`; Calibre is
not a build or test prerequisite. CI installs a JDK, poppler and the same helper
in `golang:1.27-trixie`, then runs `go test -timeout 20m ./...` for the EPUB
workflows. Each EPUBCheck invocation retains its own timeout.

For a complete isolated output check use the
[Docker fixture walkthrough](docs/docker-quickstart.md). With the default
**full image** built,
`scripts/test-onboarding` also checks private-parent dump staging, host editing,
read-only workspace preview, explicit config merge/check, one EPUB and read-only
library comparison. It cross-compiles a Go test fixture adapter to the image's
architecture and runs every container with `--network none`. Membership discovery
is substituted; parsing, capture and the preview/check/publish logic execute.
The dumped source retains `fixture_dir`, so sync reads local fixtures and skips
authentication. This does not qualify live fetching or saved-session reuse.
The merged runtime config is a fresh immutable snapshot: the macOS OrbStack
validation engine returned stale bytes for some previously mounted host paths,
even after replacement or switching to a directory bind. The script compares the
host stream with the mounted snapshot before syncing and stops on a mismatch.
Set `SERIAL_SYNC_TEST_IMAGE` to a specific local test image if needed. The script
removes its disposable directory, or `SERIAL_SYNC_ONBOARDING_KEEP=1` retains it
for inspection. No account, credentials or provider HTTP are used. This
Python-assisted check requires the full image; the browser-free steady runtime is not its test runtime.

To exercise stored
classification after a native demo run:

```sh
go run ./cmd/serial-sync --config ./examples/config.demo.toml run
go run ./cmd/serial-sync --config ./examples/config.demo.toml setup preview --stored --show-posts
```

The native demo writes `./state` and `./publish`; do not point it at real operator
state. Local `go run` is for development and fixtures; Docker is the intended user
runtime. Ordinary `run --dry-run` still writes live diagnostics and may fetch data.

## Generated Assets

```sh
$(go env GOPATH)/bin/sqlc generate
$(go env GOPATH)/bin/cue vet experimental/cue/config.cue examples/config.demo.toml -d '#Config'
```

## Conventions

- Keep provider-specific logic inside `internal/provider/<provider>`.
- Keep SQLite-specific logic inside `internal/store/sqlite`.
- Avoid leaking provider response shapes into the app or store layers.
- Add an end-to-end test when behavior changes across sync or publish flows.

## Current Scope

- Patreon supports both live auth and the fixture demo flow.
- Patreon source dumping can enumerate paid memberships into a local series-authoring workspace.
- `filesystem` and `exec` publishing are implemented.
- the public CLI is organized around `setup`, `run`, and `debug`.
- `run`, `setup auth`, and the single-process `run daemon` are implemented.
- session-bundle import is implemented with `setup auth --import-session`; challenges can still require interactive bootstrap. Steady-state sync reuses saved HTTP sessions.

## Docs

- [Config reference](docs/config.md)
- [Architecture](docs/architecture.md)
- [Control plane notes](docs/control-plane.md)
- [Observability guide](docs/observability.md)
- [PRD status](docs/prd-status.md)
- [Provider notes](docs/patreon.md)
- [Provider contribution guide](docs/provider-contributing.md)
- [Hook tutorial](docs/hooks.md)
