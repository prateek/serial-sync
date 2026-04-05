# Config Reference

`serial-sync` uses a single declarative TOML file.

Core sections:

- `[runtime]`: store and artifact roots
- `[[auth_profiles]]`: auth bootstrap and session persistence references
- `[[publishers]]`: downstream targets
- `[[sources]]`: upstream sources
- `[[rules]]`: story-track classification rules

The current MVP supports:

- provider: `patreon`
- auth mode: `fixture`
- publisher kinds: `filesystem`, `exec`
- rule match types: `tag`, `collection`, `title_regex`, `attachment_filename_regex`, `fallback`

Example:

```toml
[runtime]
store_dsn = "./state/state.db"
artifact_root = "./state/artifacts"

[[auth_profiles]]
id = "patreon-default"
provider = "patreon"
mode = "fixture"

[[publishers]]
id = "local-files"
kind = "filesystem"
path = "./publish"
enabled = true

[[publishers]]
id = "post-publish-hook"
kind = "exec"
command = ["./examples/hooks/log-publish.sh"]
enabled = false
```

For a full runnable example, use [config.demo.toml](/Users/prateek/code/experiments/2026-04-03-calibre-setup/serial-sync/examples/config.demo.toml).
