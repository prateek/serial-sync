# serial-sync agent notes

- This repo is a Patreon-first sync utility. Keep the public CLI small: `setup`, `run`, `debug`. Prefer `run` for normal execution.
- Prefer Docker/container execution for user-facing flows and all real end-to-end runs. The image is the intended runtime and includes Chromium, Xvfb, and Calibre. Assume config at `/config/config.toml` and mutable state at `/state`. Use direct `go run` only for local development, unit/integration tests, and small fixture-backed checks.
- Authoring is dump-first and offline: use `setup dump` + `setup preview`. Do not reintroduce wizard/live-discover-style primary flows.
- Treat config as source fetch + series mapping: `sources` define upstream access; `series` / `series.inputs` decide classification; output belongs at the series layer.
- Output modes are only `preserve` and `epub`. `preserve` keeps originals; `preface_mode = "prepend_post"` wraps existing EPUBs; `epub` may convert PDFs via Calibre. Published folders are user-facing only.
- Keep provider-specific logic inside `internal/provider/<provider>`, store-specific logic inside `internal/store/<backend>`, and query-layer SQL in SQLC-managed files. Handwritten SQL should stay limited to SQLite setup/migration glue.
- Patreon is rate-limit-sensitive. Keep live HTTP behind the shared request-budget/cooldown path; do not add ad hoc concurrency or bypass the budget logic.
- SQLite stores durable catalog state and one row per run. Detailed events/logs/payloads/support bundles live on disk; do not move bulky per-event data back into SQLite.
- Browser automation is for bootstrap/reauth only. Steady-state sync should reuse saved HTTP sessions.
- If workflow/output behavior changes, update `README.md`, `docs/config.md`, `docs/rules.md`, and `skills/serial-sync-rule-authoring/SKILL.md` in the same change.

Verify with:
- `go test ./...`
- `go run ./cmd/serial-sync --config ./examples/config.demo.toml setup check`
- a fixture-backed `setup preview` or `run` when changing authoring/output behavior
- a Docker-based end-to-end run when changing browser/bootstrap, container runtime, or Calibre-backed conversion behavior
