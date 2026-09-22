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
  --path ./serial-sync-rule-workspace
```

This defaults to all paid creators. Use `--creator <value>` only when you want to refresh or inspect a narrower subset.

Reuse an existing dump for offline authoring. Refresh only when new upstream
content is needed; repeat the dump command without `--force`. A refresh preserves
authored files and keeps the old capture until the new one is complete.
`setup check` and workspace preview are read-only and reject unknown keys,
invalid enums, regexes, globs, and source/book references.

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
- `captures/<generation>/sources.toml`
- `series.toml`
- `captures/<generation>/creators/<source-id>/source.json`
- `captures/<generation>/creators/<source-id>/posts.ndjson`
- `captures/<generation>/creators/<source-id>/posts/*.json`
- `captures/<generation>/creators/<source-id>/attachments/<post-id>/...`

Resolve capture files through `manifest.json`; do not glob all generations.
`posts.ndjson` contains one normalized post per line. That is the primary inspection surface.

## How to inspect the dump

Prefer local inspection over more Patreon fetches.

Useful commands:

```sh
python3 - ./serial-sync-rule-workspace <<'PY'
import json
from pathlib import Path
import sys
root = Path(sys.argv[1])
manifest = json.loads((root / "manifest.json").read_text())
for creator in manifest["creators"]:
    print(creator["source_id"], root / creator["posts_file"])
PY

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

Keep book identity within the shared series: declare `[[series.books]]` with a
`collection` or `tag`, or use input `book_id` for custom guards and priority.
Book labels require `series.source`; they select series and book but never infer
chapter boundaries. When authoring book
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
- generated, converted, wrapped, and enriched EPUBs must pass EPUBCheck before storage; unwrapped `preserve` EPUB attachments stay byte-preserving
- when an EPUB 2 attachment fails wrapping, check the [output compatibility notes](../../docs/config.md) and preserve author attributes and populated guide links when repairing it
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

1. Copy the relevant `[[sources]]` from the capture file printed as `sources=` by `setup dump` into the real config.
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

## Replay and discovery

- Preview uses the global main config by default. Pass `--series-file series.toml`
  explicitly for a standalone draft in a dump workspace.
- For an existing library, use `setup preview --stored --compare <baseline-config>`
  under the candidate `--config`. Read the classification and output-policy parts,
  including disabled/removed sources. Its applied-library section uses the rebuild
  planner for both configs against the same catalog, each against its own
  sources and publishers. Inspect actual actions and blockers; workspace-only
  replay has no applied state. A cyclic or non-covering replacement set, an
  edited delivered file or a foreign file at a destination blocks the plan with
  the same message a real rebuild would fail with. Disabled sources and
  destinations retain delivered files.
- Catalog replay is offline and read-only. It reads only current catalog payload
  references and never initializes a schema or records a run. It requires a
  checkpointed catalog; do not copy or mutate live state merely to bypass a read error.
- Inspect candidates even when every post was classified. They group evidence;
  they do not enroll or publish inferred series. A config edit can resolve members.
- `setup candidates dismiss <id> --reason <explanation>` is an explicit durable
  operator decision. New evidence reopens it; repeated lookbacks do not.
- Use source `ignore_labels` for genre/per-chapter label noise, without changing
  classification. Generic filenames and uninterrupted numbering can hide a new story;
  do not claim the detector proves a capture contains no unmapped series.
- Reports bind config hashes, binary/version, and capture inventory. Preserve those
  with private validation evidence; never commit real paid-feed titles or bodies.

### Grouped authoring

- Prefer `series.source` plus grouped `collections`, `tags`, `title_patterns`, or
  `attachment_patterns`. Lists are ORed; all guards must pass. Do not mix these
  with `match_type` / `match_value` on one input.
- Inheritance is `[defaults]`, `[sources.defaults]`, series output, then input.
  `sources.author` supplies an omitted series author.
- New grouped inputs default to 1,500 visible Unicode body characters. Set
  `min_body_chars = 0` for attachment-only or short-fiction inputs and title
  backstops when needed. Legacy inputs have no new implicit guard.
- Use `unless_tags` and `unless_title_patterns` for long nonfiction. Body length
  alone does not establish fiction. Inspect guard explanations in preview.
- `[[review]]` uses source, priority, selectors, guards, and a required reason.
  It has no implicit body guard. `[[overrides]]` uses source, release_id, either
  series or review=true, and a required reason; it runs before inputs.
- `setup preview --suggest` prints an uninstalled draft. Check every selector and
  strategy before copying it into the main config.

- Pin collection IDs from preview using `collections = [{ id = "...", name = "..." }]`.
  The name is descriptive when an ID is present. Inspect possible drift and
  label/title conflicts before accepting a mapping.
- Filename selectors pin the selected attachment across preview, sync, and rebuild.
  Verify the chosen file and eligibility, especially on posts with multiple files.
- Opt broad backstops into `hold_candidates = true` when first chapters, numbering
  resets, or unknown collections need review. Explicit overrides settle exceptions.
- `setup memberships` uses saved-session metadata only; authenticate explicitly if
  needed. `setup enrich` persists identities from captured raw JSON without
  refetching or republishing; preview can perform the same enrichment in memory.

- Decimal, suffixed, negative, and part numbering stays single unless a positive
  sequence override assigns a declared slot. Do not truncate these values or infer
  book completion from a label. Book labels compile at priority 10 before explicit
  inputs at the same priority; use explicit `book_id` inputs for other ordering.

## Publication curation

Use source `author_profile` or series `author_profiles` to select stable `[[author_profiles]]`. Put descriptions, language, cover assets, and source links under series/book `metadata`. Asset paths are relative to the owning file; retain local PNG/JPEG/GIF bytes and provenance. See [portable metadata](../../docs/config.md#portable-publication-metadata).

Prefer explicit curated values, then suitable original embedded fields, then directly linked creator/collection fallbacks. Never equate portraits, campaign banners, and book covers. Review ambiguous web identities offline. Keep original captured attachments unchanged. `epub` produces enriched standalone copies without generated About pages. Assembled books/volumes may carry one author page; book labels and attachment roles alone do not establish completeness. Preserve original author back matter and configured post-prefaces. Source URLs and author biographies remain embedded metadata. `preserve` retains its byte-preserving/wrapping contract.

When embedded titles or creators conflict with reviewed source posts, select series/book `metadata.identity_source = "release"` and verify each resulting title and canonical author in the rebuilt EPUBs. The default `"embedded"` policy preserves original identity; author profiles alone do not change it. Books can override the series policy. See the metadata reference for inheritance and replacement details.

A metadata refresh must not silently replace an existing reading copy. Inspect offline preview, then an explicit rebuild preview for already published files. Review multi-chapter attachment navigation, absence of generated chapter back matter, and at most one final author page in assembled volumes. Older chapter About pages require an explicit rebuild even without config edits; library reconciliation and redownloading offline reader copies are separate checks. Pending deliveries must keep their saved bytes. For BookOrbit, use the bundled exec v2 adapter and lifecycle hook, disable overlapping library watchers/scans, and keep initial historical imports quiet.

Keep user reading interest outside series classification and EPUB metadata. For notification-only exclusions, set `muted_series` in the adapter JSON using configured series IDs; fetching and publishing continue. BookOrbit owns reading status and locators. Snapshot existing state and exact chapter coverage before bulk read-through changes, and leave future arrivals unread. See the [adapter guide](../../integrations/bookorbit/README.md) for muting and historical completion dates.
