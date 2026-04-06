---
name: serial-sync-rule-authoring
description: Build and iterate Patreon series definitions using a local source dump workspace and offline preview. Use when the user wants to author, tighten, or debug serial-sync series config for Patreon creators.
---

# serial-sync series authoring

Use this workflow when the user wants to figure out which `[[series]]` and `[[series.inputs]]` definitions should classify Patreon posts into series.

The canonical loop is:

1. Ensure auth works.
   If Patreon serves a Cloudflare or similar interactive challenge, complete `setup auth` in a visible browser session, the bundled noVNC Docker auth flow, or import a session bundle before trying to dump.
2. Dump the creators to a local workspace once.
3. Inspect the dumped posts locally.
4. Edit `series.toml` in that workspace.
5. Run the offline preview.
6. Iterate until fallback/unmatched looks acceptable.
7. Merge the resulting `[[sources]]` and `[[series]]` into the real config.

## Commands

Bootstrap auth if needed:

```sh
serial-sync --config ./config.toml setup auth --auth-profile patreon-default
```

Dump creators into a local workspace:

```sh
serial-sync --config ./config.toml setup dump \
  --auth-profile patreon-default \
  --path ./serial-sync-rule-workspace \
  --force
```

This defaults to all paid creators. Use `--creator <value>` only when you want to refresh or inspect a narrower subset.

The dump is the canonical local capture. It includes normalized posts for fast authoring, raw Patreon post JSON, and downloaded attachments in the same workspace.

Preview series definitions offline:

```sh
serial-sync --config ./config.toml setup preview \
  --workspace ./serial-sync-rule-workspace \
  --series-file ./serial-sync-rule-workspace/series.toml \
  --show-posts
```

## Workspace layout

The dump writes:

- `manifest.json`
- `sources.toml`
- `series.toml`
- `creators/<source-id>/source.json`
- `creators/<source-id>/posts.ndjson`
- `creators/<source-id>/posts/*.json`
- `creators/<source-id>/attachments/<post-id>/...`

`posts.ndjson` contains one normalized post per line. That is the primary inspection surface.

## How to inspect the dump

Prefer local inspection over more Patreon fetches.

Useful commands:

```sh
python3 - <<'PY' ./serial-sync-rule-workspace/creators/plumparrot/posts.ndjson
import json,sys
for idx,line in enumerate(open(sys.argv[1])):
    row=json.loads(line)
    print(row["normalized"]["title"])
    if idx >= 20:
        break
PY

rg -n "Aura Overload|Andy|AA3|AO2" ./serial-sync-rule-workspace/creators/plumparrot/posts.ndjson
```

## Series drafting heuristics

Prefer these match types in roughly this order:

1. `collection`
2. `tag` when the tag is clearly series-specific
3. `title_regex`
4. `attachment_filename_regex`
5. `fallback`

Avoid generic tags like `Fantasy`, `Magic`, `story`, `update`, `news`, or anything that spans unrelated series.

Prefer one `[[series]]` per actual franchise/serial, not one per upstream Patreon tag or one per book, unless the user explicitly wants separate publish buckets. If a creator uses tags or collections like `AA1`, `AA2`, `VOT 11`, and `VOT 12`, keep those as multiple `[[series.inputs]]` under a single series whenever they all belong to the same reader-facing serial.

Important current limitation: every input under one `[[series]]` compiles to the same `track_key`. That means book identity does not become first-class track metadata today. It still survives in the normalized release payload and artifact metadata via Patreon tags and collections, so downstream processors should read those fields when they need `Book 11` or `Book 12`.

Use these priority bands:

- `10-40` for specific series rules
- `100+` for broad source defaults
- `1000+` for cleanup fallbacks

Use `manual` on a fallback when you want unmatched posts visible but not materialized automatically.

By default, keep polls, Q&A posts, reflections, recaps, reference posts, changelogs, merch posts, and broad announcements in `manual` review buckets. Do not create series-extra buckets for those unless the user explicitly asks for them to materialize as part of the series.

For output settings:

- default story series to `format = "epub"` and `preface_mode = "prepend_post"`
- keep manual/review buckets at `format = "preserve"` and `preface_mode = "none"`
- `prepend_post` only matters when the release materializes from an attachment and the Patreon post has note text; plain text-post chapters stay plain converted content
- published artifact filenames are lowercase and dash-slugged, so sample output paths may normalize spaces and punctuation

## Iteration loop

After each edit to `series.toml`:

1. Run `setup preview --show-posts`.
2. Check which posts still land in fallback.
3. Check whether any specific matcher is too broad.
4. Tighten the match.
5. Re-run preview.

Stop iterating when:

- the intended series are grouped correctly
- fallback is only catching true miscellany
- materializable counts match what the user expects to publish

## Finalization

When the series config looks right:

1. Copy the relevant `[[sources]]` from `sources.toml` into the real config.
2. Copy the final `[[series]]` from the workspace `series.toml` into the real config.
3. Run:

```sh
serial-sync --config ./config.toml run --dry-run --source <source-id>
serial-sync --config ./config.toml run --source <source-id> --target <publisher-id>
```
