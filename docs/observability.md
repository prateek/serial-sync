# Observability

`serial-sync` now records the same run in three places:

- SQLite `RunRecord` and `EventRecord`
- a human-readable text log
- a JSONL log with stable fields for shipping or machine parsing

## Log Files

Each run writes:

- `<log_root>/<run-id>.log`
- `<log_root>/<run-id>.jsonl`

Default `log_root`:

- local install: XDG state under `serial-sync/logs`
- container image: `/state/logs`

Event payload files are stored under:

- `<log_root>/event-payloads/<run-id>/`

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
serial-sync runs inspect <run-id>
serial-sync source inspect <source>
serial-sync publish-record list
serial-sync support bundle <run-id>
```

## Daemon Endpoints

The daemon exposes:

- `/healthz`
- `/status`
- `/metrics`
- `/discover/sources`
- `/discover/config`

## Support Bundles

`support bundle <run-id>` includes:

- redacted config
- run and event summaries
- copied release and artifact payloads
- copied event payload files
- copied text and JSONL logs for that run
