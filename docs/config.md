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
- auth modes: `fixture`, `username_password`
- publisher kinds: `filesystem`, `exec`
- rule match types: `tag`, `collection`, `title_regex`, `attachment_filename_regex`, `fallback`

Live auth example:

```toml
[runtime]
store_dsn = "./state/state.db"
artifact_root = "./state/artifacts"

[[auth_profiles]]
id = "patreon-default"
provider = "patreon"
mode = "username_password"
username_env = "PATREON_USERNAME"
password_env = "PATREON_PASSWORD"
session_path = "./state/sessions/patreon-default.json"

[[sources]]
id = "example-creator"
provider = "patreon"
url = "https://www.patreon.com/c/ExampleCreator/posts"
auth_profile = "patreon-default"
enabled = true
```

Fixture demo example:

```toml
[[auth_profiles]]
id = "patreon-default"
provider = "patreon"
mode = "fixture"
session_path = "./state/sessions/patreon-default.json"

[[sources]]
id = "plum-parrot"
provider = "patreon"
url = "https://www.patreon.com/c/PlumParrot/posts"
auth_profile = "patreon-default"
fixture_dir = "./testdata/fixtures/patreon/plum-parrot"
enabled = true

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

Notes:

- `session_path` stores the persisted Patreon cookie bundle.
- live bootstrap also keeps a dedicated Chromium profile beside that session file for reauth and challenge retries.
- later runs reuse the saved session over plain HTTP unless Patreon forces a reauth.

For a full runnable example, use [config.demo.toml](/Users/prateek/code/experiments/2026-04-03-calibre-setup/serial-sync/examples/config.demo.toml).
