# Architecture

The implementation follows the PRD’s ports-and-adapters shape:

- `internal/domain`: provider-agnostic core models
- `internal/provider`: source-system contract
- `internal/store`: repository contract
- `internal/store/sqlite`: SQLite backend
- `internal/artifact`: canonical artifact planning and storage
- `internal/publish`: replayable downstream publishers
- `internal/app`: orchestration shared by CLI commands
- `internal/runtime/display`: hidden-display helpers for containerized headed browser bootstrap

The first runtime slice is:

`patreon fixture source -> normalized release -> track rule -> canonical artifact -> filesystem publish`

The seams are already generic enough for future work:

- broader daemon coordination and leases
- additional providers
