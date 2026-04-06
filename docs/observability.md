# Observability

`serial-sync` splits runtime observability into:

- SQLite `RunRecord` summaries for cheap listing and status lookup
- a human-readable text log for each run
- a JSONL event stream for each run, with stable fields for shipping or machine parsing

## Log Files

Each run writes:

- `<log_root>/<run-id>.log`
- `<log_root>/<run-id>.jsonl`

Default `log_root`:

- local install: XDG state under `serial-sync/logs`
- container image: `/state/logs`

Event payload files are stored under:

- `<log_root>/event-payloads/<run-id>/`

This is the authoritative per-event history. `serial-sync` no longer duplicates the full event stream into SQLite.

## What Is Logged

- run start and finish
- provider auth-state transitions
- release classification decisions
- release no-op vs sync outcomes
- publish planned, skipped, failed, and completed outcomes
- any event payload JSON attached to those decisions

## Inspect Commands

Useful operator commands:

```sh
serial-sync debug runs
serial-sync debug events <run-id>
serial-sync debug run <run-id>
serial-sync debug publishes
serial-sync debug bundle <run-id>
```

## Daemon Endpoints

The daemon exposes:

- `/healthz`
- `/status`
- `/metrics`

## Support Bundles

`debug bundle <run-id>` includes:

- redacted config
- run summary plus the JSONL-backed event history
- copied release and artifact payloads
- copied event payload files
- copied text and JSONL logs for that run
