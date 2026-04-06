# First Source Walkthrough

Use this when you want the fastest path from zero config to a working Patreon source and an offline series-authoring workspace.

## 1. Start with a normal config

Use `serial-sync setup init` or write a config manually. The minimum useful config is:

- one Patreon auth profile
- one filesystem publisher
- at least one enabled source
- a series file you will refine after inspecting a dump

## 2. Provide auth

Default bootstrap path:

```sh
export PATREON_USERNAME='you@example.com'
export PATREON_PASSWORD='your-password'
export PATREON_TOTP_SECRET='OPTIONAL-BASE32-SECRET'
serial-sync --config ./config.toml setup auth --source example-creator
```

If you already have a valid session bundle:

```sh
serial-sync --config ./config.toml setup auth --import-session /path/to/patreon-session.json --auth-profile patreon-default
```

## 3. Dump posts for offline series authoring

Start by dumping all paid memberships into a local workspace:

```sh
serial-sync --config ./config.toml setup dump \
  --auth-profile patreon-default \
  --path ./serial-sync-rule-workspace \
  --force
```

If you only want one or two creators, add `--creator` filters later.

This writes:

- `manifest.json`
- `sources.toml`
- `series.toml`
- `creators/<source-id>/posts.ndjson`

Preview that series file offline:

```sh
serial-sync --config ./config.toml setup preview \
  --workspace ./serial-sync-rule-workspace \
  --series-file ./serial-sync-rule-workspace/series.toml \
  --show-posts
```

Use the dump workspace to:

- copy the suggested `[[sources]]` from `sources.toml` into your real config
- edit `series.toml`
- iterate locally with `setup preview` until the grouped output looks right

## 4. Merge the dumped sources and sample safely

```sh
serial-sync --config ./config.toml run --dry-run --source example-creator
```

Use the output to confirm:

- releases are being discovered
- the fallback track is catching unmatched posts
- attachments are available when expected

## 5. Run one full cycle

```sh
serial-sync --config ./config.toml run --source example-creator --target local-files
```

## 6. Inspect what happened

```sh
serial-sync --config ./config.toml debug runs
serial-sync --config ./config.toml debug run <run-id>
serial-sync --config ./config.toml debug events <run-id> --component classify
serial-sync --config ./config.toml debug publishes --source example-creator
serial-sync --config ./config.toml debug bundle <run-id>
```

Every run also writes:

- a text log under `runtime.log_root/<run-id>.log`
- a JSONL log under `runtime.log_root/<run-id>.jsonl`

## 7. Tighten the series matchers

Replace the draft series matchers in the dump workspace with source-specific matching:

- `tag`
- `collection`
- `title_regex`
- `attachment_filename_regex`

Use the dedicated guide for examples and debugging flow:

- [Series authoring guide](rules.md)

Then rerun:

```sh
serial-sync --config ./config.toml setup preview \
  --workspace ./serial-sync-rule-workspace \
  --series-file ./serial-sync-rule-workspace/series.toml \
  --show-posts
```
