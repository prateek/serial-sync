# Observability

`serial-sync` splits runtime observability into:

- SQLite `RunRecord` summaries for cheap listing and status lookup
- a human-readable text log for each run
- a JSONL event stream for each run, with stable fields for shipping or machine parsing

## Log Files

Each run writes:

- `<log_root>/<run-id>.log`
- `<log_root>/<run-id>.jsonl`

Default `log_root`:

- local install: XDG state under `serial-sync/logs`
- container image: `/state/logs`

Event payload files are stored under:

- `<log_root>/event-payloads/<run-id>/`

JSONL is the authoritative per-event history for the retained window. SQLite
keeps one compact summary per run, not the full event stream. Informational
`release_unchanged`, `publish_held`, and `publish_skipped` events keep their kind,
message and entity identity in JSONL but omit redundant payload sidecars.
Warnings, errors and other event kinds retain their payloads. A missing
`payload_ref` on these informational events is intentional.

Each event carries a `kind`, a stable name for what the event records, such as
`release_synced`, `publish_completed` or `publish_failed`. A kind also names the
event's component, so the two always agree. `internal/observe` owns both writing
and reading this record: `debug explain` reports counts, phase timings and
progress highlights read back from the JSONL, keyed on `kind` rather than on
message text. Run logs written before kinds existed are still classified by
their message, a fallback that is scheduled for removal after 2026-12-01.

## What Is Logged

- run start and finish
- provider auth-state transitions
- release classification decisions
- release no-op vs sync outcomes
- publish planned, skipped, failed, and completed outcomes
- any event payload JSON attached to those decisions

## Inspect Commands

Useful operator commands:

```sh
serial-sync debug runs
serial-sync debug events <run-id>
serial-sync debug run <run-id>
serial-sync debug publishes
serial-sync debug bundle <run-id>
```

## Daemon Endpoints

The daemon exposes:

- `/healthz`
- `/status`
- `/metrics`

## Support Bundles

`debug bundle <run-id>` includes:

- redacted config
- run summary plus the JSONL-backed event history
- copied release and artifact payloads
- copied event payload files
- copied text and JSONL logs for that run

## Diagnostic retention and recovery

Retention is opt-in. The default `runtime.diagnostic_retention_days = 0` disables
expiry. Choose a window explicitly; for example, `10` keeps ten days of finished
run diagnostics. Failures use the same window, so export evidence you need before
it expires. Catalog run summaries, source/release/artifact/volume records,
captured content, publication metadata, reading copies and pending delivery
plans have no automatic expiry.

Unchanged/held-only delivery attempts without lifecycle hooks do not save
candidate snapshots. Lifecycle hooks keep their persisted delivery identity for
retry. Plans
that can change a destination remain recoverable on disk and in the pending
catalog until delivery and retirement succeed. Successful catalog completion
then best-effort atomically replaces the full snapshot with a typed receipt containing
its delivery ID, event scope, target, run ID and completion time. A crash or compaction failure may leave the larger completed snapshot available
for later cleanup. Compaction failure emits a warning and does not fail a completed
delivery or suppress its lifecycle completion hook.

After setting a positive window, stop the daemon/timer and other writers, then
preview and apply from the normal container with the same config and state mounts:

```sh
serial-sync --config /config/config.toml debug cleanup
serial-sync --config /config/config.toml debug cleanup --apply
```

The first command is read-only and lists selected paths. `--apply` permanently
removes finished runs' text logs, JSONL (including individual `.jsonl.gz` logs),
and event-payload directories older than the window, plus old non-pending JSON
plans/receipts in `artifact_root/deliveries`. Old orphan `run_*` logs/payload
directories with no catalog row and abandoned `.retention-*` temporary files are
also selected by mtime. New runs insert their catalog row before creating logs,
so a rejected catalog insertion leaves no diagnostic files. Finished-run age
comes from the catalog; delivery-file age comes from its original modification time. Preserve
mtimes when restoring archives. Pending plans are excluded by both ID and payload
path across **all** targets, including targets removed from config.

Apply holds a SQLite write reservation from catalog inspection through deletion;
new run/plan catalog writes cannot race that snapshot. Any run still marked
`running` blocks cleanup, even after a crash. Inspect it with `debug run` and use
the [abandoned-run procedure](#reconciling-an-abandoned-run) below only after
confirming every writer is stopped. Age never establishes that a worker is dead.
The application layer makes this refusal; the store owns only the locked snapshot.

Each `log_root` and `artifact_root/deliveries` directory must belong to one
catalog; independent catalogs must not share these retained directories.

There is **no built-in filesystem/operator lock**. Stop the daemon and pause its
host timers, external retention scripts and all one-off writers before cleanup.
From the directory containing your Compose file:

```sh
docker compose stop serial-sync
docker compose ps --all
docker compose run --rm --no-deps serial-sync --config /config/config.toml debug cleanup
docker compose run --rm --no-deps serial-sync --config /config/config.toml debug cleanup --apply
docker compose up -d serial-sync
```

Verify that no other container/project/host or native process uses the same
state volume; `compose ps` alone cannot establish that. For scheduled Linux
maintenance, you may supply a host lock with `flock /path/serial-sync.operator.lock
<command>`, but then **every** sync, repair and external maintenance entry point
must use that same lock. This is an operator-provided convention, not a lock
implemented or automatically acquired by serial-sync.

The single `log_root/retention.json` horizon is fsynced before deletion. Interrupted
cleanup is safe to retry: pending plans remain protected and already absent files
are skipped. Schedule cleanup between stopped runs (or under your shared host lock) to bound history
by the selected window plus the cleanup interval. Setting a window alone does
not schedule deletion. Inspect `df -h` **and** `df -i`; free bytes do not imply
free inodes. Monitor cleanup failures and leave space for SQLite WAL files and
atomic replacements. There is no built-in archive budget or offload service.

`debug run`, `debug events`, and support bundle JSON report `history`:

| State | Meaning |
| --- | --- |
| `retained` | Raw JSONL is present. |
| `archived` | Raw JSONL is absent; `<run-id>.jsonl.gz` was read directly. |
| `legacy` | Events came from the compatibility fallback; the current SQLite store does not supply it. |
| `expired` | JSONL is absent and the finished run predates the recorded cleanup horizon. |
| `unavailable` | No JSONL or fallback is available; it may be missing or externally archived. |

`missing_payloads` lists referenced sidecars that are absent or inaccessible, including partially
restored or externally archived payloads. Counts/timings then describe only the
available evidence. Malformed raw or gzip JSONL remains an error; debug never
silently substitutes a good archive for a corrupt raw log. Existing event IDs,
synthetic IDs, ordering, long-line parsing and legacy kind classification remain
unchanged. JSONL gzip lookup does not unpack tar archives or reconstruct expired
payloads. Restore external archives to the original paths first. Cleanup does not
create archives. Manually exported support bundles are outside automatic cleanup;
keep them private and remove them explicitly when no longer needed.

Before first expiry, take a consistent backup with all writers stopped: include
config and authored dump files, sessions (stored privately), the SQLite catalog
and any WAL/journal companions, the entire artifact root (especially pending
plans and captures), and any logs/retention horizon you want to keep. Prefer a
checkpointed SQLite backup; copying a live database file alone is insufficient.
Back up published libraries and reading progress through their own tools.

Restore into a separate empty state volume, preserving the original container
paths (`/state` by default) and mtimes: catalog payload references can be absolute.
Verify SQLite `PRAGMA integrity_check`, inspect `debug runs` and a pending plan's
payload path, then use `setup preview --stored` offline to check captured content
before resuming. Never test a restore by overwriting the active deployment.
Resume with the same target configuration so pending deliveries use their saved
ID/event scope and reconcile against the current destination. Diagnostic expiry
cannot recover files from an expired window; it does not affect rebuild inputs.

### Reconciling an abandoned run

Use this only for a named run you have inspected after stopping **all** writers
and taking the consistent backup described above. Do not decide from age, an
expired lease, or a missing PID alone. Confirm the container/process that owned
that run has stopped; a paused or disconnected worker still counts as live.
There is no automatic stale-run inference or native reconcile command.

First inspect the named row with `debug run <run-id>`. Then, with the same Compose
state mount and the daemon/timers still stopped, replace the placeholder with
that exact ID and run this explicit catalog repair. Substitute the actual database
file path for `/state/state.db` if `runtime.store_dsn` uses another location. For
a `file:` URI DSN, pass its underlying filesystem path, not the URI or its query
parameters. The procedure only opens an existing on-disk catalog. Run it with the full
serial-sync image, which includes Python 3 and SQLite bindings; a browser-free
steady image may omit Python. Select the full image for this one-off Compose
service before using the command below. It changes only that row if it is still unfinished
and `running`, preserves its existing summary, records the operator action, and
sets a fresh finish time so diagnostics receive a full retention window:

```sh
docker compose run --rm -T --no-deps --entrypoint python3 serial-sync - /state/state.db run_REPLACE_WITH_INSPECTED_ID <<'PYRECONCILE'
import datetime
from pathlib import Path
import re
import sqlite3
import sys

path, run_id = sys.argv[1:]
if not re.fullmatch(r"run_[A-Za-z0-9_-]+", run_id):
    raise SystemExit("invalid run ID")
if path.startswith("file:"):
    raise SystemExit("pass the filesystem path from store_dsn, not its file: URI")
catalog_uri = Path(path).resolve().as_uri() + "?mode=rw"
with sqlite3.connect(catalog_uri, uri=True, timeout=5) as db:
    db.execute("BEGIN IMMEDIATE")
    row = db.execute(
        "SELECT status, finished_at FROM run_records WHERE id = ?", (run_id,)
    ).fetchone()
    if row != ("running", None):
        raise SystemExit("row is missing or no longer unfinished/running; changed nothing")
    finished = datetime.datetime.now(datetime.timezone.utc).isoformat()
    db.execute(
        "UPDATE run_records SET status = 'failed', finished_at = ?, "
        "summary = summary || ? WHERE id = ? AND status = 'running' AND finished_at IS NULL",
        (finished, "\nOperator reconciled abandoned run after stopping all writers.", run_id),
    )
print("Marked", run_id, "failed; pending plans and content were not changed")
PYRECONCILE
```

Inspect `debug run <run-id>` again, preview cleanup, and verify pending plans
remain available. Repeat only for other individually confirmed abandoned runs.
Keep writers stopped until maintenance finishes; then restart the daemon.
If you cannot establish that the owning worker stopped, leave the row unchanged
and keep cleanup blocked.

### Image rollback

An older image's strict loader will reject `diagnostic_retention_days`. Remove
that key from the runtime config before rolling back to an image without this
feature. Expired diagnostics require a backup to restore; an image rollback does
not restore them. Pending plan files retain their previous JSON format.
