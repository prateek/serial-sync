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

Keep reading interest separate from classification. Muting, read-through and pending-reading markers belong in the reader; for BookOrbit, unfollow a series there to silence its new-chapter alerts while serial-sync keeps delivering it. Those preferences do not change routing or EPUB metadata.

Matchers are applied by ascending `priority`. The first matching input wins.

Prefer one `[[series]]` per reader-facing serial or franchise. Use multiple `[[series.inputs]]` when a creator splits that serial across Patreon-specific tags or collections like `Book 11`, `Book 12`, `AA1`, or `AA2`.

## Recommended Workflow

1. Run `setup dump` for the authors you care about.
   If Patreon login is blocked by Cloudflare or another interactive challenge, finish `setup auth` first in a visible browser session, the bundled noVNC Docker auth flow, or import a session bundle.
   That dump is now the canonical offline capture: normalized posts for authoring, raw post JSON, and downloaded attachments live together in the same workspace.
2. Edit `series.toml` inside the dump workspace.
3. Run `setup preview --workspace <path> --series-file series.toml --show-posts` against that workspace.
4. Tighten source-specific matchers until the fallback bucket is acceptable.
5. Merge the resulting `[[series]]` and `[[sources]]` back into your main config.

Example:

```sh
serial-sync --config ./config.toml setup dump \
  --auth-profile patreon-default \
  --creator plumparrot \
  --path ./serial-sync-rule-workspace

serial-sync --config ./config.toml setup preview \
  --workspace ./serial-sync-rule-workspace \
  --series-file series.toml \
  --show-posts
```

Refresh by repeating the dump command. It writes a new capture generation and
switches the manifest only when complete; failures preserve the prior capture.
`series.toml` and other authored files remain unchanged. An unrecognized existing
directory is refused. `--force` remains accepted for compatibility and has the
same safe refresh behavior.

Workspace references, including attachment paths, are relative. You can move the
whole workspace or mount it at a different container path. Preview also relocates
older absolute-path manifests, using the opened workspace rather than the old
location. Preview reads only the generation selected by `manifest.json`.

`setup check` and workspace preview run offline without initializing state. They
reject invalid rule fields, including standalone series files, before evaluating
posts. The `extra` role publishes fiction as singles without filling chapter slots.

For agent-driven authoring, the repo also ships a local skill at `skills/serial-sync-rule-authoring/SKILL.md`.

## Replay and compare the main config

Preview reads the global `--config` file by default, just like `run`. Use
`--series-file` explicitly while drafting a standalone workspace mapping.
`--stored` reads the current normalized payloads through the catalog; it does
not scan old artifact directories or contact a provider.

With the normal `/config` and `/state` mounts:

```sh
docker compose run --rm serial-sync --config /config/config.proposed.toml \
  setup preview --stored --compare /config/config.toml --format json
```

`--workspace /state/rules-workspace` can replace `--stored`. Both configs use the
same capture, including sources removed or disabled in the candidate config.
The report separates classification changes from output-policy changes, such as
author, format, preface, book, sequence, and publisher destinations. Per-post JSON
includes the deciding input, matched selectors, other matching inputs, selected
content, and eligibility. `--show-posts` adds details to text output.

Config hashes, the software version and binary hash, and a capture inventory bind
the report to its inputs. Standalone series files have a separate hash. Identical
configs have empty change lists. Preview never creates a directory, initializes
a schema, records a run, or modifies candidate state. An active, uncheckpointed
catalog must be stopped/checkpointed before read-only catalog replay.

With `--stored`, the applied-library section plans both configs against the same
read-only catalog using the planner behind `run --rebuild --dry-run`. It lists
additions, replacements, repairs, retirements, and blockers separately from
classification and policy changes. Both the dry run and the real rebuild report
the same blocked destinations and retirement-ownership conflicts with the same
messages, and a dry run fails on a cyclic or non-covering replacement set just
as the rebuild would. Mount destination folders read-only so ownership
checks can inspect them. Disabled or removed sources and publishers retain delivered
files. Creator filters limit the planned source scope; a partial mixed-source volume
can block replacement. Workspace-only replay reports applied state as unavailable.

These plans do not run conversions or publisher hooks. After reviewing and promoting
the candidate config, use `run --rebuild` to apply changes to stored output.
Ordinary sync preserves output for unchanged content.

## Discovery candidates

Sync and preview run the same advisory detector over captured posts, including
posts already claimed by a rule. It groups unknown collections, title families,
first-chapter markers, numbering resets, substantial review posts, book files,
and external chapter links. An anonymous first chapter can acquire subsequent
numbered chapters. Declared book transitions explain numbering resets.

The detector never creates a series or changes classification. `run` shows new or
changed candidates first, then up to five unchanged unresolved candidates and a
count. Its scope is captured posts; it makes no additional provider request.
Preparation or materialization failures do not hide the rest of a fetched batch.
Members not yet in the catalog remain uncertain during stored replay.

```sh
docker compose run --rm serial-sync setup candidates
docker compose run --rm serial-sync setup candidates dismiss <id> \
  --reason "This is an intentional repost"
```

Candidates retain their identity and first-observed time in SQLite. States are
`possible`, `needs_decision`, and `resolved`. A dismissal applies to its evidence
fingerprint: repeated lookbacks do not reopen it, but new members or changed
content do. A mapping edit resolves the members it explains; preview shows those
members without persisting a resolution. `setup candidates --format json` lists
resolved entries as well.

A source may set `ignore_labels = ["fantasy", "chapter-notices"]`. This suppresses
those labels as discovery evidence without excluding any post from classification.
Title and content evidence can still raise a candidate.

New books, bonus scenes, reposts, and title-format changes can produce candidates.
Long non-fiction can look substantial. A second story with generic filenames,
the same labels, and continuing numbering can be missed. Candidate status is a
request for an operator decision, not a fiction score.

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

Keep one series identity across books. Declare `[[series.books]]` with a
`collection` or `tag` to select that series and book, or put `book_id` on an explicit
input. A label establishes identity; chapter boundaries stay explicitly declared. [The config reference](config.md#volume-output) shows complete book and
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
- `format = "epub"`: emit EPUB output for HTML/text sources and PDF attachments via Calibre; existing EPUB attachments are enriched while retaining original story resources and navigation

Set preface behavior once per series:

- `preface_mode = "none"`: no extra front matter
- `preface_mode = "prepend_post"`: render the Patreon post text as a leading EPUB page for attachment-backed EPUB output. With `format = "preserve"`, only existing EPUB attachments are wrapped; with `format = "epub"`, EPUB attachments are wrapped and PDF attachments are converted first, then wrapped.

Recommended default:

- for story series, start with `format = "epub"` and `preface_mode = "prepend_post"`
- keep `format = "preserve"` and `preface_mode = "none"` for manual/review buckets
- use book definitions and input `book_id` for reading order within a shared series
- published filenames are lowercase and dash-slugged, so shell use and URL/path handling stay predictable

EPUBs generated, converted, or wrapped by serial-sync must pass EPUBCheck before storage. Only `preserve` EPUB attachments without wrapping remain byte-preserving. `epub` output includes selected metadata without generated back matter on standalone releases. Only assembled books/volumes can receive a final author page; a book label or attachment role alone does not establish completeness.

For EPUB 2 attachments, wrapping retains author metadata and guide links while repairing empty guide sections. See the [output compatibility notes](config.md) before changing a series to pass-through output to avoid a validation failure.

For a full EPUBCheck sweep after publishing, run:

```sh
scripts/validate-epubs ~/.local/state/serial-sync/published/<source-id> ~/.local/state/serial-sync/support/epubcheck-<source-id>
```

That `prepend_post` mode is meant for the exact “author note / chapter intro” workflow you described for attachment-backed releases.

## Check reading order before bundling

Start with singles, then inspect `setup preview --show-posts`. Each materializable
post shows its chapter number, book mapping, scalar series position, reader
[series index](config.md#series-index) and filename.
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

## Shorter mappings and deliberate exceptions

Use `series.source` to avoid repeating a source on every input, and put shared
output and content settings in `[defaults]` or `[sources.defaults]`. Keep routing
in series inputs. See the [exact syntax](config.md#authoring-defaults-and-exceptions).

Start with specific title shapes, follow with guarded labels, and put broad
backstops last. Grouped `collections`, `tags`, `title_patterns`, and
`attachment_patterns` use OR semantics; all guards must pass. A chapter number
is never required. A body guard distinguishes short notices from substantial
posts; long nonfiction needs an exclusion guard or a reasoned override.

Use `[[review]]` for deliberate administrative routing and `[[overrides]]` for
one release. Preview reports substantial reviewed posts even without a chapter
number. An override settles that release's discovery evidence. Existing review
series and top-level rules remain supported.

`setup preview --workspace /workspace --suggest` includes a draft config fragment
for candidates. Review its name, selectors, strategy, and body threshold before
copying it into your config. Anonymous and external-content candidates get an
inspection note. The command never installs a suggestion.

### Identity, conflicts, and selected files

Prefer a pinned collection selector such as
`collections = [{ id = "fictional-collection-1", name = "Harbor" }]` when preview
shows an identity. The name documents the mapping; the ID survives renames.
Name-only selectors remain case-insensitive. Possible drift reports describe the
captured scope and recent sample, not deleted collections.

Preview reports when a passing label selector and a passing title or filename
selector identify different series. The first matching input still wins. Resolve
the conflict with rule order, guards, or a reasoned release override.

A filename selector pins the matched attachment. Preview, materialization, and
rebuild use that same file, even if another attachment has a preferred extension.
If that selected file is unavailable, materialization fails; inspect its reference
in preview before running. Body strategies still select the body explicitly.

A broad input may set `hold_candidates = true` to route first chapters, numbering
resets, or unreferenced collections to review. This opt-in hold is reported with
its reason and uses chronological captured evidence. Add a specific mapping or
an explicit override after reviewing the candidate.

## Curating publication metadata

Keep identity/routing rules separate from author profiles and artwork. Configure durable profiles under `[[author_profiles]]`, select them with source `author_profile` or series `author_profiles`, and put descriptions/covers in series or book `metadata`. See the [metadata configuration](config.md#portable-publication-metadata).

Compare embedded titles and creators with the source posts when curating attachments. For a series whose embedded identity is unreliable, select `metadata.identity_source = "release"` to use each post's title and the canonical author. Books can override the inherited policy with `"embedded"`. Review the affected copies through explicit rebuild preview; a valid EPUB package can still contain the wrong chapter title.

Use captured creator identity or a reviewed public-web identity link. Do not promote a campaign banner, portrait, or loosely matched search image to a book cover. Keep selected asset bytes in the offline workspace and record their source URL. Preview and rebuild use the same metadata resolver as sync. Metadata is excluded from upstream content hashes; editing it changes new publications immediately and existing publications only through explicit rebuild.
