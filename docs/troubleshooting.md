# Troubleshooting

## `challenge_required`

Patreon presented an interactive step the current MVP does not solve automatically, for example:

- CAPTCHA / Cloudflare challenge
- device verification
- TOTP / SMS / email one-time code
- passwordless or social-login-only auth flow

The tool stops intentionally in this case. Re-run after completing auth in the dedicated Chromium profile next to your configured `session_path`.

## `reauth_required`

Check:

- `PATREON_USERNAME` and `PATREON_PASSWORD` are set to the env vars named in your config
- the configured Patreon creator URL is correct
- the saved session file under `session_path` is still valid

If the saved session is stale, remove the session file and its sibling profile directory, then run `serial-sync sync --dry-run` again.

## Chrome / Chromium is missing

Live Patreon bootstrap uses a dedicated headless Chromium profile. Install Chrome or Chromium on the machine, then rerun the sync.

## I want a fully offline demo

The bundled fixture demo still works. Point the source at a directory containing:

- `posts/*.json`
- `attachments/<post-id>/<filename>`

Use the bundled fixtures under `testdata/fixtures` or `examples/config.demo.toml`.

## No releases are publishing

Check:

- `serial-sync source inspect <source>`
- `serial-sync run inspect <run-id>`
- the `publish/` directory

If the canonical artifact hash has already been published to the same target, `publish` will skip it by design.

## A release matched the wrong track

Inspect the rules in your config. Rules are applied by ascending `priority`.
