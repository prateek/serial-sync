---
name: Bug report
about: Report a sync, publish, or provider regression using a safe synthetic repro
---

## Summary

## Version and environment

Checkout commit or image digest/revision, host OS, container architecture, command.
Is this the core CLI or an optional runner/reader integration?

## Expected behavior

## Actual behavior

## Synthetic reproduction

Prefer the credential-free fixture in docs/docker-quickstart.md. Include invented
prose and example.invalid URLs. No paid account should be needed for the repro.

## Config notes

Include only relevant sections with secrets removed.

## Diagnostic excerpts

`serial-sync debug bundle <run-id>` creates private evidence. Inspect every file
before sharing any excerpt: bundles can contain paid content and signed/private
URLs even with redacted config. Do not upload live bundles, env files, cookies,
sessions, browser profiles, passwords or TOTP seeds. Follow
[the sharing checklist](https://github.com/prateek/serial-sync/blob/main/docs/observability.md#safe-sharing).
For vulnerabilities, see SECURITY.md and withhold sensitive details from issues.
