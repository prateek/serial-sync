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

This is optional because `sync` and `run once` will bootstrap automatically if the session is missing, but it is the clearest first-run command:

```sh
docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync auth bootstrap
```

The image includes Chromium and Xvfb. On Linux containers with no display, `serial-sync` starts a hidden Xvfb-backed headed browser only when bootstrap or reauth is needed.

## Discover Sources From Your Patreon Memberships

After auth succeeds, you can ask the container to suggest new `[[sources]]` and starter `[[rules]]` from the creators your account already follows:

```sh
docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync source discover --auth-profile patreon-default
```

To emit just the suggested TOML snippet:

```sh
docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync source discover --auth-profile patreon-default --format toml
```

If you already have a valid Patreon session bundle, you can import it instead:

```sh
docker run --rm \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  -v "$PWD/patreon-session.json:/tmp/patreon-session.json:ro" \
  serial-sync auth import-session /tmp/patreon-session.json
```

## Run One Sync Cycle

```sh
docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync run once
```

`run once` performs `sync` and then `publish`.

## Schedule It From Cron

Schedule the container itself. Do not rely on `docker exec` into a long-lived container.

```sh
docker run --rm \
  --env-file /opt/serial-sync/patreon.env \
  -v serial-sync-state:/state \
  -v /opt/serial-sync/config.toml:/config/config.toml:ro \
  serial-sync run once
```

## Run The Daemon

```sh
docker run -d \
  --name serial-sync \
  --restart unless-stopped \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync daemon
```

The daemon is a single-process scheduler. It runs the same `run once` pipeline on the configured interval.

If `health_addr` is set in the config, the daemon also exposes:

- `/healthz`
- `/status`
- `/metrics`
- `/discover/sources`
- `/discover/config`

The discovery endpoints use the same saved Patreon session as the CLI:

- `GET /discover/sources?auth_profile=patreon-default`
- `GET /discover/config?auth_profile=patreon-default`

A Compose example lives in `examples/docker-compose.yml`.

## Other Useful Commands

```sh
docker run --rm \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync config validate

docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync sync --source plum-parrot

docker run --rm \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync runs inspect <run-id>
```

Run logs land under `/state/logs` by default. Support bundles copy those logs automatically.
