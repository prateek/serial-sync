# Docker quickstart

Start from the repository root with Docker running. The image is the intended
runtime: it includes the browser for bootstrap, Xvfb, poppler and EPUBCheck.
The CLI uses `/config/config.toml` and keeps mutable state in `/state`. These
first-run/auth and Python-assisted test/recovery instructions use the default
**full image**. The optional browser-free steady image needs an existing usable
session; reauthenticate with full and keep the same config/state mounts. See the
[full/steady roles](https://github.com/prateek/serial-sync/blob/main/docs/support.md#full-and-steady-image-roles).

## First reading copy, without an account

This uses one synthetic chapter with no credentials, provider traffic or paid
content. It writes only to the disposable `onboarding-demo` directory on the host.

```sh
docker build -t serial-sync .
mkdir -p onboarding-demo

docker run --rm --network none \
  -v "$PWD/examples/config.fixture.toml:/config/config.toml:ro" \
  -v "$PWD/testdata/fixtures/patreon/onboarding:/fixtures:ro" \
  -v "$PWD/onboarding-demo:/state" \
  serial-sync setup check

docker run --rm --network none \
  -v "$PWD/examples/config.fixture.toml:/config/config.toml:ro" \
  -v "$PWD/testdata/fixtures/patreon/onboarding:/fixtures:ro" \
  -v "$PWD/onboarding-demo:/state" \
  serial-sync run

find onboarding-demo/published -type f -name '*.epub'
```

Expect `config ok: 1 source(s), 1 series, 1 publisher(s)` and **one EPUB**
at `onboarding-demo/published/example-author/harbor/harbor-ch0001.epub`.
Open it in your EPUB reader: it contains the synthetic Harbor chapter by Example
Author. State, logs and captures are also visible
under `onboarding-demo`, separate from the published reading copy.

Repeat the same `run` command: the unchanged release is skipped and the published
file count stays one. To inspect classification offline, with no writable state:

```sh
docker run --rm --network none --read-only \
  -v "$PWD/examples/config.fixture.toml:/config/config.toml:ro" \
  -v "$PWD/testdata/fixtures/patreon/onboarding:/fixtures:ro" \
  -v "$PWD/onboarding-demo:/state:ro" \
  serial-sync setup preview --stored --show-posts
```

After inspecting the output, remove only this disposable fixture directory:

```sh
docker run --rm --network none --entrypoint /bin/sh \
  -v "$PWD/onboarding-demo:/demo" serial-sync -c 'find /demo -mindepth 1 -delete'
rmdir onboarding-demo
```

The container cleanup handles root-owned files on Linux. It does not touch your
live named volume. The original multi-series fixture remains available at
`examples/config.demo.toml` for local development.

## Your first creator

Follow the [first-source walkthrough](https://github.com/prateek/serial-sync/blob/main/docs/first-source.md): initialize a minimal
config, authenticate, dump one creator, edit grouped series inputs, preview
offline, merge sources and series, check, then run once. Keep scheduling, custom
hooks, volumes and reader integrations until after that reading copy works.

## Compose and private remote authentication

Existing Compose operators: read the
[Compose upgrade note](https://github.com/prateek/serial-sync/blob/main/docs/support.md#upgrading-from-the-earlier-compose-example)
before adopting this example. Retain your actual old volume (commonly
`examples_serial-sync-state`), move the config/env files to the root, and restore
the daemon command/restart policy if you need its existing schedule. **Do not
switch an existing library to a new empty volume.** The commands below describe
a new installation; an existing operator substitutes the recorded state volume.

The example resolves `../config.toml` and `../patreon.env` relative to
`examples/docker-compose.yml`, so create both at the repository root as the
first-source guide describes. Its normal service runs one cycle:

```sh
docker compose -f examples/docker-compose.yml run --rm serial-sync
```

It uses the Compose-managed `serial-sync-state` volume, which is distinct from a
volume named exactly `serial-sync-state` used by standalone `docker run`.
Use one command family for live auth, dump and sync; do not accidentally create
two catalogs. To use the same standalone volume, replace the example's volume
section with:

```yaml
volumes:
  serial-sync-state:
    external: true
    name: serial-sync-state
```

Create it first with `docker volume create serial-sync-state`. Then the noVNC
service and the standalone walkthrough share the saved session and catalog.
For interactive bootstrap on a server:

```sh
docker compose -f examples/docker-compose.yml --profile auth-ui up --build serial-sync-auth
```

The auth service binds **127.0.0.1:8080** on the Docker host. Its browser session
has no VNC password; keep this temporary listener private. For a remote Docker
host, open a tunnel from your own computer (replace `user@server`):

```sh
ssh -N -L 127.0.0.1:8080:127.0.0.1:8080 user@server
```

Open `http://127.0.0.1:8080/vnc.html?autoconnect=true&resize=scale` locally.
Complete the login, then stop the auth service with Ctrl-C (or
`docker compose -f examples/docker-compose.yml stop serial-sync-auth`) and close
the tunnel. Do not expose port 8080 through a public bind or proxy.
Steady-state dumps and runs reuse the saved HTTP session. See
[Patreon notes](https://github.com/prateek/serial-sync/blob/main/docs/patreon.md) for challenge limitations.

## Optional scheduling

After a successful one-shot run, choose one scheduler. Cron/systemd can invoke
the same container command under a host lock; alternatively use `run daemon`.
For Compose, override the normal command to `["run", "daemon"]` and add
`restart: unless-stopped` before using `up -d serial-sync`. Avoid concurrent runs
against one state directory. See [config](https://github.com/prateek/serial-sync/blob/main/docs/config.md) for interval and health
settings, [hooks](https://github.com/prateek/serial-sync/blob/main/docs/hooks.md) for custom targets, and the optional
[BookOrbit integration](https://github.com/prateek/serial-sync/blob/main/integrations/bookorbit/README.md) for a file handoff.
The [support and upgrade contract](https://github.com/prateek/serial-sync/blob/main/docs/support.md) describes packaging, backups and
platform limits.
