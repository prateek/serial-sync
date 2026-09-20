# Series Authoring

Series decide how upstream releases become canonical serials, release roles, and output artifacts.

## Mental Model

Treat the config as three layers:

1. `[[sources]]`: how to fetch a creator feed or collection
2. `[[series]]`: the actual story/serial you care about
3. `[[series.inputs]]`: the source-specific matchers that feed that series

Each `[[series.inputs]]` matcher answers:

1. Which releases from this source belong to the series?
2. What role should those releases have?
3. Which content strategy should the sync use?

The series owns output behavior: `format`, `preface_mode`, and optional `bundling`.

Matchers are applied by ascending `priority`. The first matching input wins.

Prefer one `[[series]]` per reader-facing serial or franchise. Use multiple `[[series.inputs]]` when a creator splits that serial across Patreon-specific tags or collections like `Book 11`, `Book 12`, `AA1`, or `AA2`.

## Recommended Workflow

1. Run `setup dump` for the authors you care about.
   If Patreon login is blocked by Cloudflare or another interactive challenge, finish `setup auth` first in a visible browser session, the bundled noVNC Docker auth flow, or import a session bundle.
   That dump is now the canonical offline capture: normalized posts for authoring, raw post JSON, and downloaded attachments live together in the same workspace.
2. Edit `series.toml` inside the dump workspace.
3. Run `setup preview --show-posts` against that workspace.
4. Tighten source-specific matchers until the fallback bucket is acceptable.
5. Merge the resulting `[[series]]` and `[[sources]]` back into your main config.

Example:

```sh
serial-sync --config ./config.toml setup dump \
  --auth-profile patreon-default \
  --creator plumparrot \
  --path ./serial-sync-rule-workspace \
  --force

serial-sync --config ./config.toml setup preview \
  --workspace ./serial-sync-rule-workspace \
  --series-file series.toml \
  --show-posts
```

For agent-driven authoring, the repo also ships a local skill at `skills/serial-sync-rule-authoring/SKILL.md`.

## Series Shape

```toml
[[series]]
id = "the-sixth-school"
title = "The Sixth School"
authors = ["BlaQQuill"]

  [series.output]
  format = "epub"
  preface_mode = "prepend_post"

  [[series.inputs]]
  source = "blaqquill"
  priority = 10
  match_type = "title_regex"
  match_value = "^The Sixth School\\."
  release_role = "chapter"
  content_strategy = "attachment_preferred"
  attachment_glob = ["*.epub", "*.pdf"]
  attachment_priority = ["epub", "pdf"]

[[series]]
id = "unmatched-review"
title = "Unmatched Review"

  [series.output]
  format = "preserve"
  preface_mode = "none"

  [[series.inputs]]
  source = "blaqquill"
  priority = 1000
  match_type = "fallback"
  match_value = ""
  release_role = "announcement"
  content_strategy = "manual"
```

## Match Types

### `collection`

Best when the creator keeps a dedicated Patreon collection per serial.

```toml
[[series]]
id = "main-story"
title = "Main Story"

  [[series.inputs]]
  source = "example-creator"
  priority = 20
  match_type = "collection"
  match_value = "Main Story"
  release_role = "chapter"
  content_strategy = "text_post"
```

### `tag`

Best when Patreon posts carry stable author-defined tags that are actually series-specific.

When the tags are really book or arc markers for one larger serial, keep them as separate inputs under one shared `[[series]]` instead of creating one series per tag.

```toml
[[series]]
id = "andy-again"
title = "Andy, Again"

  [[series.inputs]]
  source = "plum-parrot"
  priority = 10
  match_type = "tag"
  match_value = "AA1"
  release_role = "chapter"
  content_strategy = "attachment_preferred"
  attachment_glob = ["*.epub", "*.pdf"]
  attachment_priority = ["epub", "pdf"]

  [[series.inputs]]
  source = "plum-parrot"
  priority = 11
  match_type = "tag"
  match_value = "AA2"
  release_role = "chapter"
  content_strategy = "attachment_preferred"
  attachment_glob = ["*.epub", "*.pdf"]
  attachment_priority = ["epub", "pdf"]
```

Keep one series identity across books. Declare `[[series.books]]` and put a
`book_id` on each book-specific input when its tag or collection establishes the
boundary. [The config reference](config.md#volume-output) shows complete book and
sequence-override syntax.

### `title_regex`

Best when releases follow a stable title prefix.

```toml
[[series]]
id = "nightmare-realm-summoner"
title = "Nightmare Realm Summoner"

  [[series.inputs]]
  source = "actus"
  priority = 10
  match_type = "title_regex"
  match_value = "^Nightmare Realm Summoner\\s+-\\s+Chapter\\s+"
  release_role = "chapter"
  content_strategy = "text_post"
```

### `attachment_filename_regex`

Best when post titles are noisy but attachment filenames are stable.

```toml
[[series]]
id = "side-quest"
title = "Side Quest"

  [[series.inputs]]
  source = "example-creator"
  priority = 30
  match_type = "attachment_filename_regex"
  match_value = "(?i)side[-_ ]quest.*\\.epub$"
  release_role = "chapter"
  content_strategy = "attachment_only"
  attachment_glob = ["*.epub"]
  attachment_priority = ["epub"]
```

### `fallback`

Use this sparingly. It matches everything that reached it.

Good use:

- a deliberate source-level review bucket while you bootstrap

Bad use:

- a broad matcher above more specific inputs

```toml
[[series]]
id = "main-series"
title = "Main Series"

  [[series.inputs]]
  source = "example-creator"
  priority = 100
  match_type = "fallback"
  match_value = ""
  release_role = "chapter"
  content_strategy = "attachment_preferred"
  attachment_glob = ["*.epub", "*.pdf"]
  attachment_priority = ["epub", "pdf"]
```

If no configured matcher hits, `serial-sync` still lands the release in the built-in unmatched/manual state instead of dropping it.

## Content Strategies

### `text_post`

Use when the canonical artifact should come from the post body.

### `attachment_preferred`

Use when the creator usually uploads an EPUB or PDF, but a text-post fallback is acceptable.

### `attachment_only`

Use when a release is only valid if the preferred attachment exists.

### `text_plus_attachments`

Use when either post text or attachments are acceptable canonical sources.

### `manual`

Use when a release should be observed and recorded but not materialized automatically.

Recommended default uses:

- polls and audience votes
- Q&A, reflections, recaps, and planning posts
- reference posts and changelogs
- merch, scheduling, and general creator announcements

Do not split those into separate series-extra buckets unless you explicitly want them to publish as part of the reading experience.

## Output Options

Set output policy once per series:

- `format = "preserve"`: keep the source format when possible
- `format = "epub"`: emit EPUB output for HTML/text sources and PDF attachments via Calibre; existing EPUB attachments are passed through unless wrapped

Set preface behavior once per series:

- `preface_mode = "none"`: no extra front matter
- `preface_mode = "prepend_post"`: render the Patreon post text as a leading EPUB page for attachment-backed EPUB output. With `format = "preserve"`, only existing EPUB attachments are wrapped; with `format = "epub"`, EPUB attachments are wrapped and PDF attachments are converted first, then wrapped.

Recommended default:

- for story series, start with `format = "epub"` and `preface_mode = "prepend_post"`
- keep `format = "preserve"` and `preface_mode = "none"` for manual/review buckets
- use book definitions and input `book_id` for reading order within a shared series
- published filenames are lowercase and dash-slugged, so shell use and URL/path handling stay predictable

EPUBs generated, converted, or wrapped by serial-sync must pass EPUBCheck before storage. Unchanged pass-through EPUB attachments stay byte-preserving.

For a full EPUBCheck sweep after publishing, run:

```sh
scripts/validate-epubs ~/.local/state/serial-sync/published/<source-id> ~/.local/state/serial-sync/support/epubcheck-<source-id>
```

That `prepend_post` mode is meant for the exact “author note / chapter intro” workflow you described for attachment-backed releases.

## Check reading order before bundling

Start with singles, then inspect `setup preview --show-posts`. Each materializable
post shows its chapter number, book mapping, scalar series position and filename.
The matched text and its origin explain where each chapter number came from.
Supported chapter markers include `chapter`, `chap`, `ch`, and `chaper`; number
words such as `Chapter Twelve` also work. An attachment filename can supply a
missing number. Detection affects ordering, not whether a post is classified as
a chapter.

With `bundling = "volume"`, preview also lists each expected range, present
chapters, missing slots and intentional gaps. Resolve duplicate numbers with a
sequence override; keep the extra post as a single. Mark an intentional gap only
when the author's numbering justifies it, with a reason. A later chapter or an
upstream fetch error does not close a gap.
Chapters beyond `final_chapter` stay as singles; check that the endpoint matches
the intended end of the serial.

Known author books take precedence over fixed ranges. Supply an endpoint to close
a book, and prior spans or `series_position_start` when numbers restart. Mixed
mapped and unassigned chapters stay as singles until their book mapping is clear.
An open book cannot reuse a later book's explicit series positions. Correct the
range or starting position when preview reports an overlap.

Normal sync preserves completed volumes. A correction to an existing member is
captured and reported as pending rebuild; a late chapter absent from the frozen
membership remains a single. Use the [offline rebuild workflow](first-source.md#rebuild-stored-output)
to apply corrections or regroup an existing library.

## Choosing Priorities

Recommended pattern:

- `10-40`: specific series matchers
- `100+`: broad source defaults
- `1000+`: explicit cleanup fallbacks

Keep related matchers spaced apart so inserting a more specific one later does not force a full renumber.

## Debugging Misclassified Releases

Useful commands:

```sh
serial-sync --config ./config.toml setup preview --workspace ./serial-sync-rule-workspace --series-file series.toml --show-posts
serial-sync --config ./config.toml run --dry-run --source example-creator
serial-sync --config ./config.toml debug run <run-id>
serial-sync --config ./config.toml debug events <run-id> --component classify
```

Look for:

- repeated unmatched fallback hits
- titles or collections that suggest a tighter matcher
- attachment-only matchers that hit posts with no valid attachment
- fallback matchers that are placed too early
