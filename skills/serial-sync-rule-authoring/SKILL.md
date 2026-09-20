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
  --series-file series.toml \
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

Keep book identity within the shared series: declare `[[series.books]]` and use
input `book_id` when tags or collections establish the book. When authoring book
ranges, volume grouping, intentional gaps or per-release overrides, read
[`docs/config.md`](../../docs/config.md#volume-output) for the exact syntax and
[`docs/rules.md`](../../docs/rules.md#check-reading-order-before-bundling) for the
preview workflow.

Use these priority bands:

- `10-40` for specific series rules
- `100+` for broad source defaults
- `1000+` for cleanup fallbacks

Use `manual` on a fallback when you want unmatched posts visible but not materialized automatically.

By default, keep polls, Q&A posts, reflections, recaps, reference posts, changelogs, merch posts, and broad announcements in `manual` review buckets. Do not create series-extra buckets for those unless the user explicitly asks for them to materialize as part of the series.

For output settings:

- default story series to `format = "epub"` and `preface_mode = "prepend_post"`
- keep manual/review buckets at `format = "preserve"` and `preface_mode = "none"`
- `prepend_post` only matters when the release materializes from an attachment and the Patreon post has note text; in `format = "epub"` it wraps EPUB attachments and PDF attachments after Calibre conversion, while plain text-post chapters stay plain converted content
- published artifact filenames are lowercase and dash-slugged, so sample output paths may normalize spaces and punctuation
- generated, converted, and wrapped EPUBs must pass EPUBCheck before storage; unchanged pass-through EPUB attachments stay byte-preserving
- after publishing EPUB output, run `scripts/validate-epubs <published-source-root> <report-dir>` when you need a report over the whole published folder

## Iteration loop

After each edit to `series.toml`:

1. Run `setup preview --show-posts`.
2. Check which posts still land in fallback.
3. Check whether any specific matcher is too broad.
4. Check chapter numbers, book identities and series positions. For volumes,
   inspect expected ranges and every missing slot. Keep ambiguous duplicates and
   unnumbered interludes as singles until an explicit mapping resolves them.
   Check that `final_chapter` leaves later chapters as singles and that open books
   do not overlap a later book's explicit starting position.
5. Tighten the match or sequence mapping. Declare intentional gaps only with an
   author-backed reason; a later chapter alone is insufficient.
6. Re-run preview.

Stop iterating when:

- the intended series are grouped correctly
- fallback is only catching true miscellany
- materializable counts match what the user expects to publish
- numbered posts have the intended order; open volumes explain their gaps or missing boundaries

## Finalization

When the series config looks right:

1. Copy the relevant `[[sources]]` from `sources.toml` into the real config.
2. Copy the final `[[series]]` from the workspace `series.toml` into the real config.
3. Run:

```sh
serial-sync --config ./config.toml run --dry-run --source <source-id>
serial-sync --config ./config.toml run --source <source-id> --target <publisher-id>
```

For an existing library, use the containerized offline rebuild commands in
[`docs/first-source.md`](../../docs/first-source.md#rebuild-stored-output). Preview
the selected source/series/target with read-only mounts before applying output
changes. Completed volumes and legacy output stay unchanged on normal runs.
Rebuild and its dry-run exclude disabled sources; enable every source needed by
the selected volume before rebuilding it.
Validate a small Calibre and reader sample before a large migration. Exec hooks
participating in volume replacement require protocol version 2 as documented in
[`docs/hooks.md`](../../docs/hooks.md#version-2-publish-and-supersede).
