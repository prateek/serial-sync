# Full and steady container runtimes

The default image (`full`) includes Chrome on amd64 or Chromium on arm64, Xvfb,
and the optional noVNC wrapper. It retains automatic bootstrap when `run` or
`setup dump` needs a new session. Browser automation ends after login; subsequent
requests use saved cookies and the captured browser user agent through the shared
Go HTTP request budget.

Build an optional browser-free runtime from the same source:

```sh
docker build --target full -t serial-sync:full .
docker build --target steady -t serial-sync:steady .
```

Both targets include poppler and JRE/EPUBCheck, use `/config/config.toml` and
`/state`, and have the same CLI and session bundle format. The `steady` target
omits the browser, Xvfb, window manager, VNC and noVNC services. It can run with a
usable saved session and perform offline preview/rebuild. It cannot perform
browser login. Missing or expired sessions stop with a full-image reauthentication
handoff. An upstream interactive challenge still stops for human attention.

## Reauthenticate

Stop the steady container before opening the full auth container. Use the same
config and state mounts, including any separate output mounts your config needs:

```sh
docker run --rm --env-file ./patreon.env \
  -p 127.0.0.1:8080:8080 \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  --entrypoint /usr/bin/tini serial-sync:full \
  -- /usr/local/bin/serial-sync-with-novnc setup auth --auth-profile patreon-default
```

Open `http://127.0.0.1:8080/vnc.html?autoconnect=true&resize=scale` for human
verification or MFA. Keep this port bound to loopback. The wrapper owns its
remote-display services; the login code borrows that display and owns Chromium.
For bootstrap without the wrapper, the login code owns both Chromium and its
private Xvfb display. Cancellation closes owned processes; it leaves a borrowed
display to its caller.

Alternatively, import an exported serial-sync session bundle using the existing
contract (cookies plus the actual browser user agent). This bundle is the JSON
file at the auth profile's `session_path`, written by a successful full-image
`setup auth`; retain its `0600` permissions when copying it:


```sh
docker run --rm \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  -v "$PWD/patreon-session.json:/import/session.json:ro" \
  serial-sync:steady setup auth --auth-profile patreon-default \
  --import-session /import/session.json
```

Raw browser profiles, Playwright storage state and `cookies.txt` are not this
bundle format. After full-image auth or import succeeds, resume the steady
container with the same mounts:

```sh
docker run --rm \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync:steady run
```

Image sizes depend on architecture and package versions. Measure both local
images with `docker image inspect --format '{{.RepoTags}} {{.Size}}'` before
estimating savings. Synthetic fixture checks establish local session handoff and
process cleanup; real Patreon login, challenge completion, and continued HTTP
session acceptance require separate human qualification.

## Synthetic runtime checks

After building both targets, run:

```sh
FULL_IMAGE=serial-sync:full STEADY_IMAGE=serial-sync:steady scripts/test-browser-runtime
```

This compiles Linux test executables for the images' architecture and uses
throwaway, network-disabled containers. A local TLS server answers the CLI's
Patreon requests, checks cookie/user-agent reuse, and verifies EPUB publication,
expired-session failure, import and recovery in the steady image. Full-image
checks cover staged email/password/TOTP forms, trusted submit handlers and native
validation, a controlled layout shift, synthetic human MFA, cookie export,
challenge timeout, browser
cancel/clean restart, and Xvfb ownership. The noVNC fixture checks HTTP and WebSocket/RFB availability,
exit status, signal cleanup, clean restart and MFA on the borrowed display. It replaces the CLI
only inside its disposable test container. Test output ends with local image
sizes; no real account or production session is used. Fresh fixture directories
are never modified after mounting. Every container verifies SHA-256 digests of
the executable and noVNC fixture against the recorded host digests before running.

For another architecture, build both tags with `--platform linux/amd64` or
`--platform linux/arm64` and pass those tags to the same script. An emulated build
or fixture run establishes packaging and synthetic behavior for that platform;
human-led Patreon authentication remains a separate check.

These checks cover clean shutdown and restart. Recovery after SIGKILL or a host
crash is unqualified: a stale fixed-display socket can still prevent the noVNC
wrapper from starting its display. No crash-recovery guarantee is made.

`setup auth --force` bypasses session reuse without deleting the prior bundle
before bootstrap starts. In the steady image it returns the full-image handoff
while retaining that bundle.
