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

The series itself owns output behavior. That is where `format` and `preface_mode` live.

Matchers are applied by ascending `priority`. The first matching input wins.

## Recommended Workflow

1. Run `setup dump` for the authors you care about.
   If Patreon login is blocked by Cloudflare or another interactive challenge, finish `setup auth` first in a visible browser session, the bundled noVNC Docker auth flow, or import a session bundle.
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
  --series-file ./serial-sync-rule-workspace/series.toml \
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
  format = "preserve"
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

```toml
[[series]]
id = "andy-again-3"
title = "Andy, Again 3"

  [[series.inputs]]
  source = "plum-parrot"
  priority = 10
  match_type = "tag"
  match_value = "AA3"
  release_role = "chapter"
  content_strategy = "attachment_preferred"
  attachment_glob = ["*.epub", "*.pdf"]
  attachment_priority = ["epub", "pdf"]
```

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

## Output Options

Set output policy once per series:

- `format = "preserve"`: keep the source format when possible
- `format = "epub"`: emit EPUB output, including PDF-to-EPUB conversion via Calibre

Set preface behavior once per series:

- `preface_mode = "none"`: no extra front matter
- `preface_mode = "prepend_post"`: when the release materializes from an EPUB attachment, render the Patreon post text as a leading EPUB page before the chapter content while leaving non-EPUB attachments in their original format

Recommended default:

- if the creator already uploads good EPUBs, use `format = "preserve"` with `preface_mode = "prepend_post"`
- if the creator mainly uploads PDFs and you want reader-friendly EPUB output, use `format = "epub"`

That `prepend_post` mode is meant for the exact “author note / chapter intro” workflow you described for attachment-backed releases.

## Choosing Priorities

Recommended pattern:

- `10-40`: specific series matchers
- `100+`: broad source defaults
- `1000+`: explicit cleanup fallbacks

Keep related matchers spaced apart so inserting a more specific one later does not force a full renumber.

## Debugging Misclassified Releases

Useful commands:

```sh
serial-sync --config ./config.toml setup preview --workspace ./serial-sync-rule-workspace --series-file ./serial-sync-rule-workspace/series.toml --show-posts
serial-sync --config ./config.toml run --dry-run --source example-creator
serial-sync --config ./config.toml debug run <run-id>
serial-sync --config ./config.toml debug events <run-id> --component classify
```

Look for:

- repeated unmatched fallback hits
- titles or collections that suggest a tighter matcher
- attachment-only matchers that hit posts with no valid attachment
- fallback matchers that are placed too early
