# Rule Authoring

Rules decide how upstream releases become story tracks, release roles, and canonical artifacts.

## Mental Model

Each `[[rules]]` block answers three questions:

1. Which releases does this rule match?
2. Which track should those releases land in?
3. What canonical artifact strategy should the sync choose?

Rules are applied by ascending `priority`. The first matching rule wins.

## Recommended Workflow

1. Run `setup dump` for the authors you care about.
2. Edit `rules.toml` inside the dump workspace.
3. Run `setup preview --show-posts` against that workspace.
4. Tighten source-specific rules until the fallback bucket is acceptable.
5. Merge the resulting `[[rules]]` and `[[sources]]` back into your main config.

Example:

```sh
serial-sync --config ./config.toml setup dump \
  --auth-profile patreon-default \
  --creator plumparrot \
  --path ./serial-sync-rule-workspace \
  --force

serial-sync --config ./config.toml setup preview \
  --workspace ./serial-sync-rule-workspace \
  --rules-file ./serial-sync-rule-workspace/rules.toml \
  --show-posts
```

For agent-driven rule work, the repo also ships a local skill at `skills/serial-sync-rule-authoring/SKILL.md`.

## Match Types

### `tag`

Best when Patreon posts carry stable author-defined tags.

```toml
[[rules]]
source = "plum-parrot"
priority = 10
match_type = "tag"
match_value = "AA3"
track_key = "andy-again-3"
track_name = "Andy, Again 3"
release_role = "chapter"
content_strategy = "attachment_preferred"
attachment_glob = ["*.epub", "*.pdf"]
attachment_priority = ["epub", "pdf"]
```

### `collection`

Best when the creator keeps a dedicated Patreon collection per serial.

```toml
[[rules]]
source = "example-creator"
priority = 20
match_type = "collection"
match_value = "Main Story"
track_key = "main-story"
track_name = "Main Story"
release_role = "chapter"
content_strategy = "text_post"
```

### `title_regex`

Best when releases follow a stable prefix.

```toml
[[rules]]
source = "actus"
priority = 10
match_type = "title_regex"
match_value = "^Nightmare Realm Summoner\\s+-\\s+Chapter\\s+"
track_key = "nightmare-realm-summoner"
track_name = "Nightmare Realm Summoner"
release_role = "chapter"
content_strategy = "text_post"
```

### `attachment_filename_regex`

Best when post titles are noisy but attachment filenames are stable.

```toml
[[rules]]
source = "example-creator"
priority = 30
match_type = "attachment_filename_regex"
match_value = "(?i)side[-_ ]quest.*\\.epub$"
track_key = "side-quest"
track_name = "Side Quest"
release_role = "chapter"
content_strategy = "attachment_only"
attachment_glob = ["*.epub"]
attachment_priority = ["epub"]
```

### `fallback`

Use this sparingly. It matches everything that reached it.

Good use:

- a deliberate source-level default track while you bootstrap

Bad use:

- a broad fallback above more specific rules

```toml
[[rules]]
source = "example-creator"
priority = 100
match_type = "fallback"
track_key = "main-series"
track_name = "Main Series"
release_role = "chapter"
content_strategy = "attachment_preferred"
attachment_glob = ["*.epub", "*.pdf"]
attachment_priority = ["epub", "pdf"]
```

If no configured rule matches, `serial-sync` still lands the release in the built-in unmatched/manual state instead of dropping it.

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

## Choosing Priorities

Recommended pattern:

- `10-40`: specific serial rules
- `100+`: broad source defaults
- `1000+`: explicit cleanup fallbacks

Keep related rules spaced apart so inserting a more specific rule later does not force a full renumber.

## Debugging Misclassified Releases

Useful commands:

```sh
serial-sync --config ./config.toml setup preview --workspace ./serial-sync-rule-workspace --rules-file ./serial-sync-rule-workspace/rules.toml --show-posts
serial-sync --config ./config.toml run --dry-run --source example-creator
serial-sync --config ./config.toml debug run <run-id>
serial-sync --config ./config.toml debug events <run-id> --component classify
```

Look for:

- repeated unmatched fallback hits
- titles or tags that suggest a tighter regex or tag rule
- attachment-only rules that match posts with no valid attachment
- fallback rules that are placed too early
