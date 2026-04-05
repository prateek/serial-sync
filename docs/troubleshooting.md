# Troubleshooting

## `live Patreon auth/discovery is not implemented yet`

The current MVP uses `fixture_dir` for Patreon inputs. Point the source at a directory containing:

- `posts/*.json`
- `attachments/<post-id>/<filename>`

Use the bundled demo fixtures under `testdata/fixtures`.

## No releases are publishing

Check:

- `serial-sync source inspect <source>`
- `serial-sync run inspect <run-id>`
- the `publish/` directory

If the canonical artifact hash has already been published to the same target, `publish` will skip it by design.

## A release matched the wrong track

Inspect the rules in your config. Rules are applied by ascending `priority`.
