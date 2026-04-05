# PRD Status

This repo now covers the first live Patreon vertical slice plus both built-in publisher shapes.

## Implemented

- one-shot CLI commands for `init`, config validation, inspect flows, `plan sync`, `sync`, `publish`, `auth bootstrap`, `run once`, and support-bundle export
- one-shot CLI commands for `wizard`, `source discover`, `auth import-session`, and publish-record inspection
- a single-process `daemon` that reuses the same sync/publish pipeline on an interval
- XDG-aware config loading, defaults, and container-first `/config` + `/state` roots
- live Patreon username/password bootstrap for the non-challenge case
- optional TOTP-assisted Patreon bootstrap when the challenge is an authenticator-app code
- persisted Patreon session reuse and explicit live auth state reporting
- session-bundle import validation as an operator convenience path
- live Patreon release discovery and normalization, with recent-id incremental steady-state syncs
- Patreon membership discovery with source and rule suggestions from active subscriptions
- Patreon collection-style source discovery through authenticated HTML
- fixture-backed Patreon demo inputs
- story-track rule classification with deterministic unmatched fallback behavior
- durable artifact planning and materialization for `text_post` and `attachment_preferred`, with lazy selected-attachment downloads
- idempotent SQLite-backed state, run records, event records, and publish records
- `filesystem` publisher
- `exec` publisher with stable environment variables and idempotent replay behavior
- Docker packaging with Chromium, Xvfb, and `tini` for first-run auth bootstrap inside the image
- static binary release packaging config via `.goreleaser.yml`
- Docker quickstart, config docs, discovery docs, observability docs, troubleshooting docs, hook docs, and developer docs
- Docker quickstart, config docs, discovery docs, observability docs, troubleshooting docs, rule-authoring docs, hook docs, and developer docs

## Partial

- support bundle export now includes redacted config, run data, release bundles, payload copies, and per-run text/JSON logs, but external log shipping is still future work
- observability now includes recent-run listing, filtered event inspection, run explanations, per-run text/JSON logs, event payload files, and daemon forensics endpoints, but richer metrics backends are still future work
- CUE config validation exists as an optional schema layer, not as the runtime control-plane source of truth
- daemon mode now includes source leases and local health/status endpoints, but deeper multi-replica coordination is still future work
- the project posture docs are mostly in place (`CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, provider notes, issue templates, discovery docs, observability docs, rule-authoring docs, and a first-source walkthrough)
- the store seam is generic at the repository boundary, but lease-specific store contracts and alternative backends are still future work

## Remaining

High-priority remaining work:

- richer challenge-provider handling beyond username/password plus TOTP
- broader observability and operator forensics beyond the current support-bundle/log surfaces
- deeper multi-replica daemon coordination beyond source-level leases

Secondary PRD gaps:

- anthology-mode publication behavior
