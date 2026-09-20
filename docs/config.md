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
- `[series.output]`: format, preface behavior and optional volume grouping
- `[[series.books]]`: author-book identity, expected chapter ranges and reading positions
- `[[series.sequence_overrides]]`: explicit sequence choices for individual source releases

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
- published chapter names use the series slug, optional `bkNN`, and `chNNNN` (minimum four digits). Unnumbered posts use date and title. Only colliding names receive a stable identity suffix; every member of a collision receives one.

## Volume output

Within a series, set:

```toml
[series.output]
format = "epub"
preface_mode = "prepend_post"
bundling = "volume"
chapters_per_volume = 50
intentional_gaps = [{ chapter = 17, reason = "The author skipped this number" }]
```

`bundling` defaults to `"none"`; its other value is `"volume"`, which requires
`format = "epub"`. `chapters_per_volume` defaults to 50 and must be positive.
Without author books, expected ranges are 1–50, 51–100, and so on. All expected
slots must be available before a volume completes. To close a short final range,
set `final_chapter` to its inclusive endpoint. Omit it while the ending is unknown.
Chapters beyond that endpoint remain singles until the endpoint or mapping changes.

Declare author books under the same series when their boundaries are known:

```toml
[[series.books]]
id = "book-one"
number = 1
title = "Main Story: Book One"
first_chapter = 1
last_chapter = 80

[[series.books]]
id = "book-two"
number = 2
first_chapter = 1
last_chapter = 60
series_position_start = 81
intentional_gaps = [{ chapter = 12, reason = "Intentional author numbering gap" }]
```

Each author book becomes one volume, regardless of `chapters_per_volume`.
`id` is stable; `number` defines book order. `first_chapter` defaults to 1.
Omitting `last_chapter` keeps the book open. Detected book numbers match these
definitions; an input's `book_id = "book-two"` overrides title detection.
Keep book-specific gaps on the book definition.

When numbering restarts at 1, declared prior spans determine the series position.
In this example Book Two chapter 1 is position 81. `series_position_start` can
supply the position when earlier books are missing from the archive. Unknown
positions are explained in preview and omitted from chapter metadata; they block
volume completion. Intentional gaps reserve their positions.
Known position overlaps are rejected even when an earlier book has no endpoint.
If a chapter in an open book reaches a later book's explicit starting position,
it remains a single with no scalar position until the conflicting mapping is fixed.

Use source-native release IDs for exceptions:

```toml
[[series.sequence_overrides]]
source = "example-creator"
release_id = "154807100"
book_id = "book-two"
chapter = 4

[[series.sequence_overrides]]
source = "example-creator"
release_id = "154807101"
keep_single = true
```

An override can map an interlude or ambiguous title to a slot. Use `keep_single`
on the other release when two posts share a chapter number; duplicates otherwise
block the group. Available chapters stay as singles until the mapping is resolved.

Completed editions keep their membership and bytes during normal runs. Changed
inputs, mappings or volume size require `run --rebuild`; disabling bundling also
requires rebuild to replace existing volumes with singles. Missing inputs block
the affected transition. Delivery and retirement are tracked separately per target,
and user-modified or unrelated destination files are preserved with a conflict.
Rebuild and its dry-run select enabled sources only. A replacement requiring a
disabled source is blocked; enable that source before rebuilding the shared volume.

`anthology_mode` is deprecated in both series inputs and legacy rules. An explicit
`false` emits a warning; remove the field. `true` is rejected: use series-level
`bundling = "volume"` instead. Exec targets need [protocol version 2](hooks.md)
for volumes or retirement.

For a full runnable example, use [config.demo.toml](../examples/config.demo.toml).

For real-world rule patterns, use [rules.md](rules.md).
