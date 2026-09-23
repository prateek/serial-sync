# Architecture

The implementation follows the PRD’s ports-and-adapters shape:

- `internal/domain`: provider-agnostic core models
- `internal/provider`: source-system contract
- `internal/store`: repository contract
- `internal/store/sqlite`: SQLite backend
- `internal/config`: the authored document plus a compiled, read-only rule set
  built lazily behind one access path
- `internal/artifact`: canonical artifact planning and storage; the planned
  reading copy (name, type, validation) is one module shared by previews and
  materialization; every edit to a reading copy's package document runs in one
  edit session (unzip, container indirection, package-document parse, rezip)
  instead of each stage unzipping on its own
- `internal/publish`: replayable downstream publishers behind one target seam
  that states each target's capabilities (filesystem and exec adapters, plus an
  in-memory adapter for tests)
- `internal/filehash`: the one content hash shared by artifact and publish
- `internal/sequence`: the single chapter sequence detector
- `internal/observe`: run logs, structured events, and support-bundle inputs
- `internal/app`: orchestration shared by CLI commands; release intake plans and
  applies release actions, and one run-scoped decisions value keeps a single
  decision per release within a run
- `internal/runtime/display`: hidden-display helpers for containerized headed browser bootstrap
- `internal/runtime/daemon`: local health and metrics endpoints

The first runtime slice is:

`patreon fixture source -> normalized release -> track rule -> canonical artifact -> filesystem publish`

The seams are already generic enough for future work:

- broader daemon coordination and leases
- additional providers
- richer log shipping and metrics backends
