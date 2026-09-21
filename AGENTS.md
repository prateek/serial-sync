# serial-sync agent notes

- This repo is a Patreon-first sync utility. Keep the public CLI small: `setup`, `run`, `debug`. Prefer `run` for normal execution.
- Prefer Docker/container execution for user-facing flows and all real end-to-end runs. The image is the intended runtime and includes Chromium, Xvfb, and Calibre. Assume config at `/config/config.toml` and mutable state at `/state`. Use direct `go run` only for local development, unit/integration tests, and small fixture-backed checks.
- Config checking and all preview modes are read-only: use the strict loader without store initialization or run records. Dump refresh installs a generation atomically and preserves authored files.
- Authoring is dump-first and offline: use `setup dump` + `setup preview`. Do not reintroduce wizard/live-discover-style primary flows.
- Treat config as source fetch + series mapping: `sources` define upstream access; `series` / `series.inputs` decide classification; output belongs at the series layer; sources may carry defaults their inputs inherit.
- Output modes are only `preserve` and `epub`. `preserve` keeps originals; `preface_mode = "prepend_post"` wraps existing EPUBs; `epub` may convert PDFs via Calibre. Published folders are user-facing only.
- Keep provider-specific logic inside `internal/provider/<provider>`, store-specific logic inside `internal/store/<backend>`, and query-layer SQL in SQLC-managed files. Handwritten SQL should stay limited to SQLite setup/migration glue.
- Book labels select identity, never coverage. Keep extended chapter and part values intact and out of fixed-range volumes until explicitly overridden. Applied-library comparisons must use the rebuild planner.
- Keep identity enrichment outside content hashes and bind durable metadata to its capture. Preview, sync, and rebuild must consume the same selected-content reference and holding decisions.
- Patreon is rate-limit-sensitive. Keep live HTTP behind the shared request-budget/cooldown path; do not add ad hoc concurrency or bypass the budget logic.
- Compile defaults, grouped selectors, guards, review rules, and overrides into the same ordered rule model. Preserve legacy routing; new grouped inputs have a 1,500-character guard unless overridden.
- Discovery is advisory and uses the same feature extractor in sync and replay. Keep candidate identities, member fingerprints, and operator dismissals behind the store boundary; never persist body text in candidate rows.
- SQLite stores durable catalog state and one row per run. Detailed events/logs/payloads/support bundles live on disk; do not move bulky per-event data back into SQLite.
- Browser automation is for bootstrap/reauth only. Steady-state sync should reuse saved HTTP sessions.
- If workflow/output behavior changes, update `README.md`, `docs/config.md`, `docs/rules.md`, and `skills/serial-sync-rule-authoring/SKILL.md` in the same change.

Verify with:
- `go test ./...`
- `go run ./cmd/serial-sync --config ./examples/config.demo.toml setup check`
- a fixture-backed `setup preview` or `run` when changing authoring/output behavior
- a Docker-based end-to-end run when changing browser/bootstrap, container runtime, or Calibre-backed conversion behavior
