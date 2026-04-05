# First Source Walkthrough

Use this when you want the fastest path from zero config to a working Patreon source.

## 1. Generate a starter config

Interactive:

```sh
serial-sync wizard
```

Non-interactive:

```sh
serial-sync wizard \
  --non-interactive \
  --path ./config.toml \
  --source-url https://www.patreon.com/c/ExampleCreator/posts \
  --source-id example-creator \
  --publisher-path ./publish
```

The wizard writes:

- one Patreon auth profile
- one filesystem publisher
- one enabled source
- one fallback rule you can refine later

## 2. Provide auth

Default bootstrap path:

```sh
export PATREON_USERNAME='you@example.com'
export PATREON_PASSWORD='your-password'
export PATREON_TOTP_SECRET='OPTIONAL-BASE32-SECRET'
serial-sync --config ./config.toml auth bootstrap --source example-creator
```

If you already have a valid session bundle:

```sh
serial-sync --config ./config.toml auth import-session /path/to/patreon-session.json --auth-profile patreon-default
```

## 3. Ask Patreon what you already follow

If you want suggestions instead of hand-authoring every source:

```sh
serial-sync --config ./config.toml source discover --auth-profile patreon-default
```

To emit just the additive TOML:

```sh
serial-sync --config ./config.toml source discover --auth-profile patreon-default --format toml
```

The discovery flow:

- inspects your active Patreon memberships
- suggests creator-feed `[[sources]]`
- samples recent posts for each creator
- suggests starter `[[rules]]` from recurring tags, collections, or title prefixes

## 4. Sample the source safely

```sh
serial-sync --config ./config.toml plan sync --source example-creator
```

Use the output to confirm:

- releases are being discovered
- the fallback track is catching unmatched posts
- attachments are available when expected

## 5. Run one full cycle

```sh
serial-sync --config ./config.toml run once --source example-creator --target local-files
```

## 6. Inspect what happened

```sh
serial-sync --config ./config.toml source inspect example-creator
serial-sync --config ./config.toml runs inspect <run-id>
serial-sync --config ./config.toml publish-record list --source example-creator
serial-sync --config ./config.toml support bundle <run-id>
```

Every run also writes:

- a text log under `runtime.log_root/<run-id>.log`
- a JSONL log under `runtime.log_root/<run-id>.jsonl`

## 7. Tighten the rules

Replace the fallback-only rule with source-specific matching:

- `tag`
- `collection`
- `title_regex`
- `attachment_filename_regex`

Use the dedicated guide for examples and debugging flow:

- [Rule authoring guide](rules.md)

Then rerun:

```sh
serial-sync --config ./config.toml plan sync --source example-creator
```
