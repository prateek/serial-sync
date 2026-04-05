---
name: serial-sync-rule-authoring
description: Build and iterate Patreon rule sets using a local source dump workspace and offline preview. Use when the user wants to author, tighten, or debug serial-sync rules for Patreon creators.
---

# serial-sync rule authoring

Use this workflow when the user wants to figure out which `[[rules]]` should classify Patreon posts into tracks.

The canonical loop is:

1. Ensure auth works.
2. Dump the creators to a local workspace once.
3. Inspect the dumped posts locally.
4. Edit `rules.toml` in that workspace.
5. Run the offline preview.
6. Iterate until fallback/unmatched looks acceptable.
7. Merge the resulting `[[sources]]` and `[[rules]]` into the real config.

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

Preview rules offline:

```sh
serial-sync --config ./config.toml setup preview \
  --workspace ./serial-sync-rule-workspace \
  --rules-file ./serial-sync-rule-workspace/rules.toml \
  --show-posts
```

## Workspace layout

The dump writes:

- `manifest.json`
- `sources.toml`
- `rules.toml`
- `creators/<source-id>/source.json`
- `creators/<source-id>/posts.ndjson`

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

## Rule drafting heuristics

Prefer these match types in roughly this order:

1. `collection`
2. `tag` when the tag is clearly series-specific
3. `title_regex`
4. `attachment_filename_regex`
5. `fallback`

Avoid generic tags like `Fantasy`, `Magic`, `story`, `update`, `news`, or anything that spans unrelated series.

Use these priority bands:

- `10-40` for specific series rules
- `100+` for broad source defaults
- `1000+` for cleanup fallbacks

Use `manual` on a fallback when you want unmatched posts visible but not materialized automatically.

## Iteration loop

After each edit to `rules.toml`:

1. Run `setup preview --show-posts`.
2. Check which posts still land in fallback.
3. Check whether any specific rule is too broad.
4. Tighten the match.
5. Re-run preview.

Stop iterating when:

- the intended series are grouped correctly
- fallback is only catching true miscellany
- materializable counts match what the user expects to publish

## Finalization

When the rules look right:

1. Copy the relevant `[[sources]]` from `sources.toml` into the real config.
2. Copy the final `[[rules]]` from the workspace `rules.toml` into the real config.
3. Run:

```sh
serial-sync --config ./config.toml run --dry-run --source <source-id>
serial-sync --config ./config.toml run --source <source-id> --target <publisher-id>
```
