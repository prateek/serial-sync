---
name: Provider regression
about: Report upstream source or normalization breakage with synthetic evidence
---

## Provider and last known good behavior

## Version and environment

Checkout commit or image digest/revision, host OS, container architecture, command.

## Expected versus actual behavior

## What changed upstream

## Synthetic payload and repro

Use invented text and example.invalid URLs while preserving the response shape
needed to reproduce the failure. Prefer a fixture-backed normalization test;
see docs/provider-contributing.md. Do not attach creator content or live payloads.

## Safe diagnostic excerpts

`serial-sync debug bundle <run-id>` can contain paid content and signed/private
URLs. Inspect locally and share only sanitized excerpts. Never post credentials,
env files, cookies, sessions, browser profiles or TOTP seeds. Follow
[the sharing checklist](https://github.com/prateek/serial-sync/blob/main/docs/observability.md#safe-sharing).
See SECURITY.md for sensitive security reports; public issues are not private.
