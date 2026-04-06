# Troubleshooting

## `challenge_required`

Patreon presented an interactive step the current MVP does not solve automatically, for example:

- CAPTCHA / Cloudflare challenge
- device verification
- SMS / email one-time code
- passwordless or social-login-only auth flow

If the challenge is an authenticator-app code, set `totp_secret_env` in the auth profile and retry. For Cloudflare or other interactive gates, use a visible host browser session or the bundled noVNC Docker auth flow for `serial-sync setup auth` so you can finish the challenge by hand, then reuse that saved session from Docker. You can also import a fresh session bundle with `serial-sync setup auth --import-session`.

## `reauth_required`

Check:

- `PATREON_USERNAME` and `PATREON_PASSWORD` are set to the env vars named in your config
- the configured Patreon creator URL is correct
- the saved session file under `session_path` is still valid

If the saved session is stale, remove the session file and its sibling profile directory, then run `serial-sync run --dry-run` again.

## I already have a Patreon session bundle

Import it directly:

```sh
serial-sync setup auth --import-session /path/to/patreon-session.json --auth-profile patreon-default
```

The command copies the bundle into `session_path` and validates it against the matching source configuration.

## Chrome / Chromium is missing

Live Patreon bootstrap uses a dedicated browser profile in a headed browser session. In Docker on Linux, `serial-sync` starts Xvfb automatically when no display is available. For headless hosts, the bundled noVNC auth wrapper exposes that browser so you can finish Cloudflare-style challenges remotely before you go back to normal Docker runs. The image uses Google Chrome on `amd64` and Chromium on `arm64`. If you are not using the bundled image, install Chrome or Chromium and `Xvfb`, then rerun `serial-sync setup auth` or `serial-sync run`.

The bundled container defaults `SERIAL_SYNC_CHROME_NO_SANDBOX=true` because many Docker and homelab runtimes block Chromium's namespace sandbox even for an unprivileged browser user. If your runtime supports the sandbox, set `SERIAL_SYNC_CHROME_NO_SANDBOX=false` and retry.

## I want a fully offline demo

The bundled fixture demo still works. Point the source at a directory containing:

- `posts/*.json`
- `attachments/<post-id>/<filename>`

Use the bundled fixtures under `testdata/fixtures` or `examples/config.demo.toml`.

## No releases are publishing

Check:

- `serial-sync setup dump --auth-profile <profile> --path ./serial-sync-rule-workspace --force`
- `serial-sync debug run <run-id>`
- `serial-sync debug events <run-id> --component publish`
- `serial-sync debug publishes`
- `serial-sync debug publish <publish-record-id>`
- the `publish/` directory

If the canonical artifact hash has already been published to the same target, `publish` will skip it by design.

## A release matched the wrong track

Inspect the rules in your config. Rules are applied by ascending `priority`.

## I need to know what happened during a run

Each run now writes:

- `<log_root>/<run-id>.log`
- `<log_root>/<run-id>.jsonl`

By default, `log_root` is under your state directory. `debug bundle <run-id>` copies those logs alongside the run summary and payload references.
