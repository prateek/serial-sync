# Source Discovery

`serial-sync` can inspect the Patreon account behind a saved auth profile and suggest starter config for the creators that account already follows.

## CLI

Text summary:

```sh
serial-sync --config ./config.toml source discover --auth-profile patreon-default
```

Additive TOML snippet:

```sh
serial-sync --config ./config.toml source discover --auth-profile patreon-default --format toml
```

JSON:

```sh
serial-sync --config ./config.toml source discover --auth-profile patreon-default --format json
```

## What It Does

- validates or bootstraps the Patreon session for the selected auth profile
- inspects active Patreon memberships through the current-user API
- derives creator-feed `[[sources]]`
- samples recent posts for each creator
- suggests starter `[[rules]]` from recurring tags, collections, or title prefixes

## How To Use The Output

Recommended flow:

1. `auth bootstrap`
2. `source discover --format toml`
3. merge the suggested `[[sources]]` and `[[rules]]` into your config
4. run `plan sync`
5. tighten rules before relying on unattended publishing

The suggestions are intentionally conservative:

- already-configured creators are shown but omitted from the TOML snippet by default
- unmatched releases still fall back to the built-in unmatched state unless you add a broader fallback rule
- title-prefix rules are heuristics and should be reviewed before long-term use

## Daemon Endpoints

When the daemon is running and `health_addr` is configured:

- `GET /discover/sources?auth_profile=patreon-default`
- `GET /discover/config?auth_profile=patreon-default`

Query parameters:

- `auth_profile`
- `sample`
- `include_configured`
