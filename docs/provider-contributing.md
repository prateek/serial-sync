# Provider Contribution Guide

New providers should fit the existing shape:

1. Implement `internal/provider.Client`.
2. Normalize upstream payloads into `domain.NormalizedRelease`.
3. Keep auth/bootstrap logic inside the provider package.
4. Avoid leaking provider-specific response types into `internal/app` or `internal/store`.

Capabilities that only some clients have are optional interfaces in
`internal/provider/provider.go`; the app finds them by type assertion
(`client.(provider.CapturedEnricher)`) and falls back to defaults. The
optional interfaces today:

- `CapturedEnricher` derives metadata from captured bytes without
  fetching or authenticating.
- `ProfileAuthenticator` bootstraps a saved session before a source is
  configured.
- `DumpWorkerLimit` states how many creator dumps the provider may run
  side by side; providers without it keep the app default, and the
  provider's own request budget is the rate control.

Before opening a PR for a provider:

- add raw fixtures under `testdata/fixtures/<provider>/`
- add or extend an integration test that covers normalization plus sync behavior
- document auth expectations and known failure modes

The goal is a small provider contract with a generic core, not a plugin SDK in v1.

For a first contribution, fix one normalization edge case with synthetic data;
a new provider needs prior scope discussion. Patreon parsing tests and
`testdata/fixtures/patreon/onboarding` provide a small starting point. Invent all
prose and use `example.invalid` URLs; follow [safe sharing](observability.md#safe-sharing).

Provider registration currently lives at three `provider.NewRegistry` calls:
offline preview and writable service startup in `cmd/serial-sync/main.go`, plus
membership listing in `cmd/serial-sync/memberships.go`. Keep all three consumers
consistent. A provider implementation should demonstrate:

- expired auth returning `reauth_required` rather than retrying indefinitely
- pagination resuming from the saved cursor without dropping or duplicating releases
- normalized capture replay without a network call through `CapturedEnricher`
- shared request-budget/cooldown handling for live HTTP, including throttled replies

Keep fixture normalization deterministic; use a local HTTP server for budget and
pagination contract tests. Never use a contributor's paid account in CI.
