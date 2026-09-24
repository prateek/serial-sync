# BookOrbit library

Serial-sync writes finished books into a BookOrbit library folder and stops there. BookOrbit's folder watcher imports each file, may rename it, and may write metadata back into it. Serial-sync makes no BookOrbit API calls and stores no BookOrbit IDs. The optional [reader experiment](reader/README.md) is a separate BookOrbit patch.

## Setup

Create a BookOrbit library with **one book per file** whose folder is the mount serial-sync writes to, and turn on its folder watcher. File renaming and metadata write-back can stay on. Mount the same folder into the serial-sync container, for example at `/library`, and point a `drop` publisher at it:

```toml
[[publishers]]
id = "bookorbit"
kind = "drop"
path = "/library"
enabled = true
```

Every series delivered to this library must use single-chapter output. `drop` rejects `bundling = "volume"`, because it can never replace or retire a file after BookOrbit owns it.

## What a run does

- A new chapter lands at `<path>/<source>/<series>/<file>`. It is copied to a dot-prefixed temporary name in that folder, synced, checked against the artifact hash, and renamed into place. BookOrbit's watcher skips dot-prefixed paths, so it never sees a partial file.
- The catalog records each hand-off by source and Patreon post ID. After that, serial-sync ignores the file: BookOrbit can move it or rewrite its bytes without causing a second drop.
- A revised post, including a metadata-only rebuild, is **held**. It is not dropped again, because a second file would become a second book. The run reports it as `held`, and `debug run <id>` counts held revisions. To publish a revision, replace the book in BookOrbit by hand.
- If the library root is missing, delivery fails instead of creating it, since a missing root usually means the volume is not mounted.

## Alerts

BookOrbit sends new-chapter notifications, and readers unfollow series in BookOrbit. Serial-sync reports only run health. Set `SERIAL_SYNC_HEALTHCHECK_URL` to a Healthchecks.io ping URL, and each non-dry-run `run` pings `<url>/start`, then `<url>` on success or `<url>/fail` with a short sanitized reason. Unset, no pings are sent. The URL is a credential: keep it in a private env file, not in config or Git. Set the check's period and grace to match your schedule.

## Hourly operation

The [systemd units](systemd/) run the Docker workflow once per hour under a file lock. Install them under the service account's `~/.config/systemd/user`, keep the Compose project at `~/serial-sync`, and enable user lingering. Manual runs should take the same `run.lock`. Inspect runs with `systemctl --user status serial-sync.service` and `journalctl --user -u serial-sync.service`.
