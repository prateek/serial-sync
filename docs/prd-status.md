# PRD Status

This repo now covers the first live Patreon vertical slice plus both built-in publisher shapes.

## Implemented

- one-shot CLI commands for `init`, config validation, inspect flows, `plan sync`, `sync`, `publish`, `auth bootstrap`, `run once`, and support-bundle export
- a single-process `daemon` that reuses the same sync/publish pipeline on an interval
- XDG-aware config loading, defaults, and container-first `/config` + `/state` roots
- live Patreon username/password bootstrap for the non-challenge case
- persisted Patreon session reuse and explicit live auth state reporting
- live Patreon release discovery and normalization, with recent-id incremental steady-state syncs
- fixture-backed Patreon demo inputs
- story-track rule classification with deterministic unmatched fallback behavior
- durable artifact planning and materialization for `text_post` and `attachment_preferred`, with lazy selected-attachment downloads
- idempotent SQLite-backed state, run records, event records, and publish records
- `filesystem` publisher
- `exec` publisher with stable environment variables and idempotent replay behavior
- Docker packaging with Chromium, Xvfb, and `tini` for first-run auth bootstrap inside the image
- Docker quickstart, config docs, troubleshooting docs, hook docs, and developer docs

## Partial

- support bundle export exists, but richer packing and redaction policy can still improve
- observability exists through run/event persistence, but structured log shipping and richer metrics are still future work
- CUE config validation exists as an optional schema layer, not as the runtime control-plane source of truth
- daemon mode exists, but source leases, richer health surfaces, and multi-replica coordination are still future work

## Remaining

- challenge-provider handling beyond clear `challenge_required` / `reauth_required` failures
- config wizard and guided rule/auth bootstrap flows
- session-bundle import
- optional local HTTP status/health surface for daemon mode
- richer support-bundle contents, including broader log and payload collection
