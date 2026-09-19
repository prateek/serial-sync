# Config Reference

`serial-sync` uses a single declarative TOML file.

Core sections:

- `[runtime]`: logs, store, and artifact roots
- `[scheduler]`: daemon interval, lease, and health settings
- `[[auth_profiles]]`: auth bootstrap and session persistence references
- `[[publishers]]`: downstream targets
- `[[sources]]`: upstream sources
- `[[series]]`: canonical story/serial definitions
- `[[series.inputs]]`: source-specific matchers that feed each series
- `[series.output]`: preferred output format and preface behavior for a series

The current MVP supports:

- provider: `patreon`
- auth modes: `fixture`, `username_password`
- publisher kinds: `filesystem`, `exec`
- matcher types: `tag`, `collection`, `title_regex`, `attachment_filename_regex`, `fallback`

Live auth example:

```toml
[runtime]
log_root = "/state/logs"
store_dsn = "/state/state.db"
artifact_root = "/state/artifacts"

[scheduler]
mode = "interval"
poll_interval = "1h"
lease_ttl = "30m"
health_addr = "127.0.0.1:8099"

[[auth_profiles]]
id = "patreon-default"
provider = "patreon"
mode = "username_password"
username_env = "PATREON_USERNAME"
password_env = "PATREON_PASSWORD"
totp_secret_env = "PATREON_TOTP_SECRET"
session_path = "/state/sessions/patreon-default.json"

[[sources]]
id = "example-creator"
provider = "patreon"
url = "https://www.patreon.com/c/ExampleCreator/posts"
auth_profile = "patreon-default"
enabled = true

[[sources]]
id = "example-collection"
provider = "patreon"
url = "https://www.patreon.com/collection/123456"
auth_profile = "patreon-default"
enabled = false

[[series]]
id = "main-story"
title = "Main Story"
authors = ["Example Creator"]

  [series.output]
  format = "epub"
  preface_mode = "prepend_post"

  [[series.inputs]]
  source = "example-creator"
  priority = 10
  match_type = "collection"
  match_value = "Main Story"
  release_role = "chapter"
  content_strategy = "attachment_preferred"
  attachment_glob = ["*.epub", "*.pdf"]
  attachment_priority = ["epub", "pdf"]
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
- `log_root` stores per-run text logs, JSONL logs, and event payload files.
- `totp_secret_env` is optional and only needed when Patreon asks for an authenticator-app code that can be satisfied with TOTP. The variable can hold the bare base32 secret or a full `otpauth://totp/...` URI, such as the one `op read` returns for a 1Password one-time password field. A URI's `period`, `digits`, and `algorithm` parameters are honored.
- live bootstrap also keeps a dedicated Chromium profile beside that session file for reauth and challenge retries.
- `lease_ttl` controls how long a daemon source lease survives if the worker crashes before it can release it.
- `health_addr` controls the daemon’s local `/healthz`, `/status`, and `/metrics` listener.
- in the Docker image, `/config/config.toml` and `/state` are the default roots.
- later runs reuse the saved session over plain HTTP unless Patreon forces a reauth.
- `setup auth --import-session` can seed `session_path` from an externally generated session bundle.
- `setup dump` writes additive `sources.toml` plus a local `series.toml` scaffold based on the Patreon memberships tied to the selected auth profile.
- each dump creator directory now contains `posts.ndjson`, raw Patreon post JSON in `posts/`, and downloaded attachments in `attachments/`.
- those creator directories are fixture-compatible captures for later offline replay/materialization work, even though the generated `sources.toml` snippet still points at the live Patreon sources.
- if Patreon presents a Cloudflare or other interactive challenge, complete `setup auth` in a visible browser session, the bundled noVNC Docker auth flow, or import a session bundle before returning to the Docker run path.
- `format = "preserve"` keeps the source format when possible.
- `format = "preserve"` plus `preface_mode = "prepend_post"` wraps existing EPUB attachments with a front-matter page while leaving non-EPUB attachments in their original format.
- `format = "epub"` emits EPUB output for HTML/text sources and PDF attachments via Calibre's `ebook-convert`; existing EPUB attachments are passed through unless `preface_mode = "prepend_post"` wraps them.
- `format = "epub"` plus `preface_mode = "prepend_post"` adds the Patreon post text to EPUB attachments and to PDF attachments after conversion.
- EPUBs generated, converted, or wrapped by serial-sync are checked as ZIP/OCF/package documents during planning and must pass EPUBCheck before they are stored. Unchanged pass-through attachments stay byte-preserving. Native, non-Docker runs that produce EPUB output need `epubcheck` on `PATH`. Use `scripts/validate-epubs <published-root> <report-dir>` when you want a full EPUBCheck pass over a published folder.
- published artifact filenames are lowercase, dash-slugged, and derived from track name, sequence/date, and release id.

For a full runnable example, use [config.demo.toml](../examples/config.demo.toml).

For real-world rule patterns, use [rules.md](rules.md).
