# Contributing

Serial-sync is a small Patreon-first utility. Maintenance and responses are best
effort, including fixes after upstream changes. Core contributions should work
without BookOrbit or a paid account. Reader fork support is outside the default
scope; optional integrations have their own guides and obligations.

Start with the [Docker fixture](docs/docker-quickstart.md), then read the
[developer prerequisites](DEVELOPER.md#working-locally) and
[provider contribution guide](docs/provider-contributing.md).

A small first code contribution is a synthetic Patreon normalization regression:
add one fictional `posts/*.json` input under a test-local fixture and assert the
normalized chapter title, author and body in `internal/provider/patreon`. Keep it
offline, with an `example.invalid` URL and invented prose. Show it fails before
the fix and passes afterward. Do not submit paid chapters or session bundles.
Documentation corrections with exact repro commands are also welcome.

Discuss new providers, schema migrations, output/identity changes, CLI expansion
or runner/reader architecture before a large PR. No plugin SDK or governance
framework is required to contribute. For existing behavior:

- Keep provider logic in `internal/provider/<provider>` and store logic behind
  `internal/store`; SQLite queries belong in SQLC-managed files.
- Preserve the `setup`, `run`, `debug` CLI and dump-first offline authoring.
- Keep legacy routing compatible and test outcomes through stable boundaries.
- Include a regression for sync/publish behavior, plus a fixture run or preview.
- Update README, config/rules docs and the rule-authoring skill together for
  workflow/output changes. State platform/live paths you could not validate.

Before sending changes:

```sh
go test ./...
go run ./cmd/serial-sync --config ./examples/config.demo.toml setup check
```

Use the Docker fixture for a complete reading-copy check. Native tests need
poppler, a JDK and the custom `serial-sync-epubcheck` helper; see the developer
guide for exact install commands. If changing the optional runner, also run:

```sh
python3 -m unittest discover -s integrations/bookorbit/runner -v
```

Reports and PRs must follow the [safe-sharing checklist](docs/observability.md#safe-sharing).
Use a synthetic repro in public; a support bundle is private until inspected.
Core contributions follow the [root Apache-2.0 license](LICENSE); preserve any
file-specific and optional reader terms.
