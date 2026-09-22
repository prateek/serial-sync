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
