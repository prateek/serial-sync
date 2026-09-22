# BookOrbit publication

This adapter implements serial-sync's exec v2 delivery protocol and optional batch lifecycle. It is included in the serial-sync image at `/opt/serial-sync/bookorbit/hook.py`. Use stock BookOrbit 3.0.0 by default. The optional [reader experiment](reader/README.md) adds in-reader Previous/Next and explicit correction handling; those features are absent from the stock deployment.

Create a BookOrbit library using **one book per file**, with its watcher, scheduled scans, file renaming, and metadata writeback disabled. The adapter owns the scan sequence and published files. Give its account access to this library and permission to scan and delete files. Keep reader credentials in a private JSON file containing `username` and `password`.

Mount the serial-sync destination into both containers. `library_root` is the path visible to serial-sync; `reader_root` is the corresponding path visible to BookOrbit. Copy [example.json](example.json) to `/config/bookorbit.json` and set the URLs, library ID, paths, and a random ntfy topic in the empty `notification.topic` field. Keep it and the credentials outside Git.

```toml
[[publishers]]
id = "bookorbit"
kind = "exec"
protocol_version = 2
command = ["python3", "/opt/serial-sync/bookorbit/hook.py", "/config/bookorbit.json"]
lifecycle_command = ["python3", "/opt/serial-sync/bookorbit/hook.py", "/config/bookorbit.json"]
enabled = true
```

Every selected series must publish EPUB. Start with `announce = false` for the initial import. Enable it after the baseline is acknowledged; this prevents thousands of historical chapter notifications. `adopt_existing = true` permits adopting an existing file only when its bytes match the saved artifact exactly. Use this deliberately during migration, then turn it off.

The adapter stages new paths, requests one batch scan, waits for completion, finds the exact imported paths, and downloads each new version to verify its SHA-256. A successful per-artifact acknowledgement means BookOrbit serves those bytes. Ordinary sync overwrites existing paths at their ordered delivery step. An explicit rebuild can batch replacements of stable singles when the lifecycle event includes previous publications proving the same path and release identity, and their artifact ID/hash match the adapter's ownership receipt. It checks every destination and required saved artifact before staging, then scans once and verifies every changed file before acknowledging publications. Missing predecessor proof, ambiguous identities, and volumes retain per-artifact delivery. Saved write intents and readiness receipts let an interrupted batch resume without adopting unknown files.

Every overwrite requires the same release-ID set as the owned reading copy. A volume that shrinks, grows, or changes membership must use a distinct filename and an ordered migration that verifies replacements before retiring the old copy. Legacy receipts without coverage require an explicit rebuild with matching single-release predecessor proof; unsupported volume replacements fail before changing the file. Maintenance also upgrades matching unchanged single receipts, so later corrections can use ordinary sync.

Retirement requires all replacement receipts and unchanged owned files. Its completion record is saved before ownership cleanup, so restarting after deletion can acknowledge the same event. Unrelated files, symlinks, and user-modified copies cause a retryable conflict. Retirement of a file partly read by the publishing account is blocked until its position is migrated or the copy is finished. This adapter targets a personal library; additional readers need a progress audit before retirement. Use stable singles for ongoing series; arbitrary regrouping into a volume cannot preserve a locator automatically.

Receipts and the notification outbox live under `state_dir`; include them in backups alongside serial-sync state. Pending work uses saved artifact bytes. A failed import never announces a ready chapter. A failed notification retains readable publication receipts and retries the outbox. Notification batch membership remains fixed until all its outbox entries are drained, so a restart during cleanup cannot announce the remaining subset again. Do not delete receipts as a way to retry a run.

Notifications are grouped by series and deduplicated by release coverage. Metadata rebuilds and unchanged runs are quiet. Multi-chapter source releases count as one new release in the summary; the adapter does not guess how many internal chapters a release contains. Use hosted ntfy for phone alerts that open the reading URL:

```json
{"kind":"ntfy","url":"https://ntfy.sh","topic":"an-unguessable-random-topic"}
```

Use a fresh random topic and keep it outside the repository. Anyone who knows an unprotected hosted topic can read or publish its notifications; send only release summaries and reader URLs that still require authentication. In the ntfy phone app, subscribe to that topic on `https://ntfy.sh` before enabling it. The adapter sends the reading URL as ntfy's `click` field; the [iOS notification handler](https://github.com/binwiederhier/ntfy-ios/blob/893bae9cce985c6272ac70f9e75874bf8fc67a27/ntfy/App/AppDelegate.swift#L152) opens it when tapped. Verify an actual backgrounded-phone tap before treating setup as complete.

Neither transport provides this adapter with an idempotency key. A crash after the service accepts a notification but before the local receipt is saved can repeat it. Both exe.dev tests reached the phone, but tapping the fresh test opened only the exe.dev app. The current exe payload does not satisfy reading-link navigation. ntfy passed Docker publication and API readback of the exact `click` URL, and the user confirmed its notification arrived and opened BookOrbit. It is now the active deployed transport. See the trial record below for deployment status.

Run adapter checks with `python3 -m unittest discover -s integrations/bookorbit -v`. The [trial record](../../docs/research/reader-experience-validation.md) records runtime evidence and rollout status.

For hourly VM operation, the [systemd units](systemd/) run the Docker workflow once per hour with a file lock. Install them under the service account's `~/.config/systemd/user`, keep the Compose project at `~/serial-sync`, and enable user lingering. Finish the baseline and pilot before enabling `serial-sync.timer`. Use `systemctl --user status serial-sync.service` and `journalctl --user -u serial-sync.service` to inspect delivery failures. The run itself emits the configured failure notification. Manual operational runs should take the same `run.lock`.

When migrating already published files, retain a manifest of `source/series/filename` mapped to the predecessor's `sha256` and `artifact_id`, proven against successful old publisher receipts and the backup bytes. `python3 /opt/serial-sync/bookorbit/adopt.py /config/bookorbit.json /config/adoption-manifest.json` validates every file before adopting ownership. It does not grant readiness or notify. This permits a corrected new edition to replace its known predecessor during the first BookOrbit delivery. Unknown or changed files still block migration. Retain the manifest with the rollback snapshot.
