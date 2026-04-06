# Docker Quickstart

The Docker image is the default deployment target.

`serial-sync` keeps its config under `/config/config.toml` and all mutable state under `/state`.

## Build

```sh
docker build -t serial-sync .
```

## Create Config And Credentials

Write a config file on the host, then mount it read-only into `/config/config.toml`.

Put Patreon credentials in an env file:

```sh
printf 'PATREON_USERNAME=%s\nPATREON_PASSWORD=%s\n' \
  "you@example.com" \
  "your-password" > patreon.env
```

## Bootstrap Auth Explicitly

This is optional because `run` will bootstrap automatically if the session is missing, but it is the clearest first-run command:

```sh
docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync setup auth
```

The image includes Chromium and Xvfb. On Linux containers with no display, `serial-sync` starts a hidden Xvfb-backed headed browser only when bootstrap or reauth is needed.

The browser still runs as the unprivileged `serialsync` user inside the container. By default the bundled image keeps Chromium on `--no-sandbox` because many container runtimes block the namespace sandbox. If your runtime supports Chromium's sandbox cleanly, override `SERIAL_SYNC_CHROME_NO_SANDBOX=false`.

If Patreon presents a Cloudflare or other interactive challenge, you now have
three bootstrap paths:

- run `setup auth` from a visible host browser session
- run `setup auth` through the bundled noVNC wrapper in Docker
- import a session bundle

After auth succeeds once, return to the normal Docker `setup dump`, `run`, and
`run daemon` flow.

## Headless Server Bootstrap Via noVNC

For a headless Linux box, expose a temporary noVNC session and complete Patreon
auth remotely:

```sh
docker compose -f ./examples/docker-compose.yml --profile auth-ui up --build serial-sync-auth
```

Then open:

```text
http://<host>:8080/vnc.html?autoconnect=true&resize=scale
```

This runs Chrome on Xvfb inside the container, exposes it through noVNC, and
executes:

```sh
serial-sync setup auth --auth-profile patreon-default --force
```

Once auth completes and the session is saved under `/state`, stop the auth
service and run the normal daemon or one-shot sync container.

## Dump Sources For Series Authoring

After auth succeeds, dump the creators you care about into a local workspace. By default this uses paid memberships.

```sh
docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  -v "$PWD:/work" \
  serial-sync setup dump --auth-profile patreon-default --path /work/serial-sync-rule-workspace --force

docker run --rm \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  -v "$PWD/serial-sync-rule-workspace:/work" \
  serial-sync setup preview --workspace /work --series-file /work/series.toml --show-posts
```

If you already have a valid Patreon session bundle, you can import it instead:

```sh
docker run --rm \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  -v "$PWD/patreon-session.json:/tmp/patreon-session.json:ro" \
  serial-sync setup auth --import-session /tmp/patreon-session.json
```

## Run One Sync Cycle

```sh
docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync run
```

`run` performs sync and then publish. Use `run --dry-run` to preview classification and materialization without mutating state or publishing.

## Schedule It From Cron

Schedule the container itself. Do not rely on `docker exec` into a long-lived container.

```sh
docker run --rm \
  --env-file /opt/serial-sync/patreon.env \
  -v serial-sync-state:/state \
  -v /opt/serial-sync/config.toml:/config/config.toml:ro \
  serial-sync run
```

## Run The Daemon

```sh
docker run -d \
  --name serial-sync \
  --restart unless-stopped \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync run daemon
```

The daemon is a single-process scheduler. It runs the same `run` pipeline on the configured interval.

If `health_addr` is set in the config, the daemon also exposes:

- `/healthz`
- `/status`
- `/metrics`

A Compose example lives in `examples/docker-compose.yml`.

## Other Useful Commands

```sh
docker run --rm \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync setup check

docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync run --source plum-parrot

docker run --rm \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync debug run <run-id>
```

Run logs land under `/state/logs` by default. Support bundles copy those logs automatically.
