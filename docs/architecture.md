# Architecture

The implementation follows the PRD’s ports-and-adapters shape:

- `internal/domain`: provider-agnostic core models
- `internal/provider`: source-system contract
- `internal/store`: repository contract
- `internal/store/sqlite`: SQLite backend
- `internal/config`: the authored document plus a compiled, read-only rule set
  built lazily behind one access path
- `internal/artifact`: canonical artifact planning and storage
- `internal/publish`: replayable downstream publishers behind one target seam
  (filesystem and exec adapters)
- `internal/sequence`: the single chapter sequence detector
- `internal/observe`: run logs, structured events, and support-bundle inputs
- `internal/app`: orchestration shared by CLI commands
- `internal/runtime/display`: hidden-display helpers for containerized headed browser bootstrap
- `internal/runtime/daemon`: local health and metrics endpoints

The first runtime slice is:

`patreon fixture source -> normalized release -> track rule -> canonical artifact -> filesystem publish`

The seams are already generic enough for future work:

- broader daemon coordination and leases
- additional providers
- richer log shipping and metrics backends
