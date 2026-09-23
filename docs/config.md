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

`setup check`, `setup preview`, and `run` use the same strict config loader.
Unknown keys, invalid enum values, malformed regexes and attachment globs, and
unknown source/series/book references fail with a file and field diagnostic. Standalone
series files receive the same validation. Check and all preview modes create no
state, logs, or database; they need no provider connection. Warnings identify
fallbacks above other inputs and identical selectors; the first input still wins.

`setup preview` uses this main config unless `--series-file` is supplied. Compare
it with another complete config using `--compare <path>` and either `--stored`
or `--workspace <path>`; see [replay and comparison](rules.md#replay-and-compare-the-main-config).
Sources may declare `ignore_labels = ["fantasy", "news"]` to suppress matching
label names from discovery. It has no effect on routing or publication.

Release roles are `chapter`, `extra`, `release_attachment`, `announcement`,
`schedule`, `preview_bundle`, and `unknown`. `extra` is fiction that occupies no
chapter slot and therefore remains a single. Each legacy input or rule must set
`release_role` and `content_strategy`; see [content strategies](rules.md#content-strategies).

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
- `setup dump` installs a complete capture generation and atomically points `manifest.json` at it. It creates `series.toml` once; refreshes preserve authored files and earlier generations.
- each dump creator directory now contains `posts.ndjson`, raw Patreon post JSON in `posts/`, and downloaded attachments in `attachments/`.
- those creator directories are fixture-compatible captures for later offline replay/materialization work, even though the generated `sources.toml` snippet still points at the live Patreon sources.
- if Patreon presents a Cloudflare or other interactive challenge, complete `setup auth` in a visible browser session, the bundled noVNC Docker auth flow, or import a session bundle before returning to the Docker run path.
- `format = "preserve"` keeps the source format when possible.
- `format = "preserve"` plus `preface_mode = "prepend_post"` wraps existing EPUB attachments with a front-matter page while leaving non-EPUB attachments in their original format.
- `format = "epub"` emits EPUB output for HTML/text sources and PDF attachments via Calibre's `ebook-convert`; existing EPUB attachments become enriched publication copies while retaining story resources, author metadata, and navigation. `preface_mode = "prepend_post"` additionally wraps them.
- `format = "epub"` plus `preface_mode = "prepend_post"` adds the Patreon post text to EPUB attachments and to PDF attachments after conversion.
- EPUB 2 wrapping preserves namespaced author attributes and populated guide links. Empty guide sections are removed before validation; chapter content and NCX navigation remain intact.
- EPUBs generated, converted, or wrapped by serial-sync are checked as ZIP/OCF/package documents during planning and must pass EPUBCheck before they are stored. `preserve` attachments without wrapping stay byte-preserving. New `epub` publications include selected metadata. Standalone releases have no generated back matter; assembled books/volumes can include one author page. Native, non-Docker runs that produce EPUB output need `epubcheck` on `PATH`. Use `scripts/validate-epubs <published-root> <report-dir>` when you want a full EPUBCheck pass over a published folder.
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

When numbering restarts at 1, declared prior spans determine the series position
that volume coverage uses. In this example Book Two chapter 1 is position 81.
`series_position_start` can supply the position when earlier books are missing
from the archive. Unknown positions are explained in preview and block volume
completion. Intentional gaps reserve their positions. The reader-facing
[series index](#series-index) is separate and needs no declared spans.
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

## Series index

Every EPUB carries a series index (`calibre:series_index` and EPUB 3
`group-position`) that readers sort by. It follows the author's numbering:

- the chapter number (`1369`), or a half chapter as written (`11.5`);
- `book.chapter` (`5.25`) when numbering restarts per book, detected from
  `Book 2`, `B2 Chapter 4`, `B2C4` or the author's `Title 3.1` shorthand. A
  chapter without a book marker belongs to the most recent book;
- a release without its own number (interlude, bonus, epilogue, a half chapter
  inside a book, a repeated number) repeats the index of the release published
  before it, and the reader orders the tie by publish date. A prologue that
  opens a new book gets `book.0`;
- part numbers for serials told in `[Part N]` posts, and release order
  (`1`, `2`, `3`) for series where fewer than half the posts carry a number.

BookOrbit accepts only digits with one optional decimal point and sorts the
fraction as a whole number (`5.3` before `5.25`), so the index never carries
letters. An anthology whose stories each carry their own numbers should follow
release order instead:

```toml
[series.output]
series_index = "release"
```

A release's index depends on the releases published before it in the same
series, so a new release normally leaves earlier indexes alone. Two series-wide
choices can change them: the first time a serial's numbering restarts, its
earlier chapters move from `25` to `1.25`, and a young series can switch
between release order and chapter numbers as posts accumulate. Sync rebuilds
the recent chapters it sees; run `run --rebuild` for the series to update the
rest.

## Authoring defaults and exceptions

`[defaults]` and `[sources.defaults]` accept `format`, `preface_mode`,
`release_role`, `content_strategy`, `attachment_glob`, `attachment_priority`,
and `min_body_chars`. Settings inherit in that order, then from
`[series.output]` (format and preface), then from the individual input. Explicit
empty attachment lists and `min_body_chars = 0` clear the inherited restriction.
Without configured defaults, role is `chapter`, strategy is `text_post`, format
is `preserve`, and preface is `none`.

A series may set `source`; each input can override it. Authors come from
`series.authors`, then `sources.author`, then the provider's creator name.
For a series spanning sources, declare its output explicitly when bundling.

An input uses either the legacy `match_type` / `match_value` pair or selector
lists. Lists are ORed, including across types. Patterns are Go regular expressions;
collection and tag names match case-insensitively. A matching input must also
pass every guard:

```toml
[[series]]
id = "glass-harbor"
title = "The Glass Harbor"
source = "fictional-author"

[[series.inputs]]
priority = 10
title_patterns = ['^Harbor (Chapter|Epilogue)']
min_body_chars = 0

[[series.inputs]]
priority = 20
collections = ["Harbor", "Harbor archive"]
tags = ["harbor-fiction"]
min_body_chars = 1500
unless_tags = ["news"]
unless_title_patterns = ['(?i)^Questions and answers']
```

`attachment_patterns` matches filenames. `min_body_chars` counts decoded,
whitespace-normalized Unicode characters in visible HTML, or plain text when HTML
is absent. It defaults to 1,500 on grouped inputs; legacy matchers acquire no new
implicit guard. Use zero for short fiction, title backstops, or attachment-only
inputs. Failed guards continue to the next input and appear in preview explanations.

Built-in review is unpublished. Review rules participate in the same priority
order as series inputs; they have no implicit body guard:

```toml
[[review]]
source = "fictional-author"
priority = 5
tags = ["administrative"]
reason = "Administrative announcements"

[[overrides]]
source = "fictional-author"
release_id = "example-release"
series = "glass-harbor"
reason = "Confirmed fiction with an incorrect upstream label"
```

An override runs before inputs. Set exactly one of `series` or `review = true`,
and provide a nonempty reason. A series override inherits source defaults and
series output; selector guards do not apply. Review also requires a reason.
Review and override entries are validated like series inputs when the config
loads: selector patterns must compile and guards must be well formed, reported
as `review[N]` or `overrides[N]`. Unmatched posts need no fallback series.

### Collection identity and holding

A collection selector can use a name or a provider ID with an optional display
name. When an ID is present, only the ID determines the match:

```toml
[[series.inputs]]
collections = [{ id = "fictional-collection-1", name = "Harbor" }]

[[series.inputs]]
priority = 1000
title_patterns = ['^Harbor']
min_body_chars = 0
hold_candidates = true
```

`hold_candidates` defaults to false. On the winning input or review rule it
holds first-chapter markers, chronological numbering resets, and unreferenced
collections in review. Explicit release overrides take precedence. Both preview
and sync analyze the captured source history; missing history blocks a rebuild.
Discovery remains advisory on every other input.

`setup preview` reports scoped collection identities, observed names, and possible
drift. `setup enrich [--source ID]` persists identity metadata from stored raw JSON
without refetching or changing content hashes or publication. Preview performs
that enrichment in memory when needed. The durable metadata is bound to its
capture version so a failed fetch cannot relabel older captured content.

An auth-profile-only config is valid for `setup auth --auth-profile ID` and
`setup memberships --auth-profile ID [--format json]`. Membership inspection uses
the saved session for one metadata request and reports configured, enabled, and
mapped status. An expired session requires explicit authentication; no browser
opens during inspection.

### Books selected by labels

Set `series.source` when a book uses `collection` or `tag`. These selectors
inherit the series output and source/global rule defaults, including the grouped
input body guard. A collection accepts the same name or `{ id, name }` forms as
input collection lists. Both labels on one book are ORed.

```toml
[[series]]
id = "harbor"
title = "Harbor"
source = "fictional-author"

[[series.books]]
id = "arrival"
number = 1
collection = { id = "fictional-book-1", name = "Arrival" }
first_chapter = 1
last_chapter = 80

[[series.books]]
id = "return"
number = 2
tag = "Return"
```

Book selectors become ordinary inputs at priority 10, before explicit inputs with
the same priority. Lower-numbered inputs and release overrides take precedence.
Book labels can supply all inputs for a series. Use explicit inputs with `book_id`
when a book needs different guards, content selection, or priority.

The second book above remains open until its endpoint is declared. A collection
never implies completion. Unknown book markers are reported as candidates and
cannot complete a fixed-range volume.

Sequence parsing preserves `3.60`, `24D`, negative chapter numbers, and `[Part K8]`
in `chapter_label` or `part` fields. They remain singles without a scalar reading
position; an explicit positive `sequence_overrides.chapter` can assign a volume
slot. Part markers also remain attached to an otherwise integral chapter, including
when the selected filename supplies the part. `B6C57`, `Chaptger 204`, `Chapeter 480`,
and a leading `737 - Title` yield integral chapters. Existing spelled-out chapter
numbers remain supported. A filename separator such as `chapter-50.epub` means
chapter 50; `Chapter -50` preserves the negative sign.

## Portable publication metadata

`epub` output embeds descriptions, selected cover artwork, language, series order, and source/author metadata. Standalone releases end with their original content, without a generated About page. This applies to text posts, EPUB attachments, and converted PDFs, including extras and releases assigned to a book. Book labels and attachment roles do not establish complete-book coverage.

Assembled books/volumes retain member navigation and may add one final author page with biographies, portraits, and descriptive link labels. Assembly removes serial-sync-generated member pages, identified by `serial-sync:about`. Original author back matter and configured post-prefaces survive. Captured originals are unchanged. `preserve` retains its existing byte-preserving/wrapping contract.

The source post URL is embedded in `dc:source`; curated links and author URLs use `dc:relation`. Author profiles, including biography and an embedded portrait reference, are retained in `serial-sync:author` metadata. Those custom fields travel with the EPUB but reader support for displaying them varies. The publication synopsis remains in `dc:description`; a biography does not replace it. These fields follow the [EPUB metadata model](https://www.w3.org/TR/epub-33/#sec-pkg-metadata) and [Dublin Core meanings](https://www.dublincore.org/specifications/dublin-core/usageguide/elements/).

Use stable author-profile IDs across sources. A source's `author_profile` is the default; `series.author_profiles` selects explicit profiles for that series. Omit them to use the creator directly linked in captured Patreon metadata. A campaign banner or author portrait is never guessed to be a series cover. A configured book collection can supply its captured description and cover.

```toml
[[author_profiles]]
id = "harbor-author"
name = "Ada Harbor"
biography = "Writes maritime fantasy."
url = "https://author.example/about"
portrait = { path = "assets/ada.jpg", source_url = "https://author.example/about" }

# Within an existing [[sources]]:
# author_profile = "harbor-author"

# Within an existing [[series]]:
# author_profiles = ["harbor-author"]
# [series.metadata]
# description = "A city built above a sleeping sea."
# language = "en"
# cover = { path = "assets/harbor.jpg", source_url = "https://author.example/harbor" }
# links = ["https://author.example/harbor"]
```

Books accept the same fields in `[series.books.metadata]`, overriding the series defaults. Explicit descriptions/covers override embedded ones; otherwise suitable embedded metadata is preserved before captured creator/collection fallbacks fill gaps. Public-web profiles and artwork must be selected explicitly; a matching display name alone does not establish identity.

Attachment titles and creators are retained by default (`identity_source = "embedded"`). If a series has unreliable embedded identity, set `identity_source = "release"` in its metadata: published `epub` chapter copies use each source post's title and the configured canonical author, falling back to the captured creator. This replaces old title/creator entries and their obsolete identity refinements while retaining story resources and unrelated metadata. Author profiles enrich embedded metadata and assembled author pages; they alone do not override embedded creators. Books inherit the series policy and can explicitly select `"embedded"` again. These are the only accepted nonempty values. Volumes keep their assembled volume title.

For example, an attachment titled `Unknown` or carrying the wrong chapter number can use its reviewed post identity:

```toml
# Within the affected existing [[series]]:
# [series.metadata]
# identity_source = "release"
```

Asset paths are relative to their owning config/series file. Select local PNG, JPEG, or GIF files up to 16 MiB and retain their source URL. Serial-sync snapshots chosen bytes by SHA-256 with the edition. `source_url` records provenance; it does not trigger a preview-time download. Missing optional upstream artwork does not block a chapter. An explicitly configured missing/invalid asset is an actionable configuration error for new publications or rebuilds.

Live Patreon capture downloads linked artwork through the shared request budget, with a daily URL cache and a one-hour failure cooldown. Dumps retain referenced image bytes for offline use. `setup enrich` can recover profile fields from stored JSON but cannot invent uncaptured image bytes.

Review selected metadata with offline `setup preview --show-posts --format json`. Normal sync pins existing publication inputs, even when curation changes. Use `run --rebuild --dry-run` to see affected reading copies, then `run --rebuild` to apply the reviewed change. Rebuild is offline and repeated unchanged rebuilds do not republish. Existing pending deliveries retain their saved editions.

Older EPUB editions with per-chapter About pages require the same explicit rebuild, even without a config edit. With the normal mounts, preview one series using `docker compose run --rm serial-sync run --rebuild --dry-run --series <series-id>`, then remove `--dry-run` to apply it. The upgrade retains filenames and original chapter resources. Rescan/reconcile the library through its configured publisher after replacement, and redownload existing offline copies in Readest. A local rebuild does not update a phone download or prove that a reader retained saved progress; verify those separately before expanding the rebuild.

The [BookOrbit integration](../integrations/bookorbit/README.md) documents controlled import, readiness receipts, grouped ntfy notifications with reading links, and the optional reader patch. Configure its private notification topic in the adapter JSON after subscribing on the phone. The adapter is separate from the portable metadata model.

For notification-only exclusions, set `muted_series` to a JSON array of configured `series.id` values in the BookOrbit adapter JSON. This setting is not a TOML series field: it leaves source fetching, output, and reader progress unchanged. See the integration guide for queued-notification compatibility and bulk reading-status updates.
