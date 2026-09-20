# Hook Tutorial

An `exec` publisher delivers artifacts through a command. The default protocol
retains the existing chapter payload. Opt into version 2 for volumes and retirement.

The model is:

1. `run` captures inputs and materializes artifacts in durable local storage.
2. Its publish phase delivers those artifacts to each selected target.
3. An `exec` publisher receives stable file paths plus internal metadata/normalized/raw JSON paths after the artifact already exists on disk.

Minimal config:

```toml
[[publishers]]
id = "post-publish-hook"
kind = "exec"
command = ["./examples/hooks/log-publish.sh"]
enabled = true
```

Each exec invocation receives stable environment variables such as:

- `SERIAL_SYNC_RUN_ID`
- `SERIAL_SYNC_TARGET_ID`
- `SERIAL_SYNC_TARGET_KIND`
- `SERIAL_SYNC_SOURCE_ID`
- `SERIAL_SYNC_SOURCE_URL`
- `SERIAL_SYNC_TRACK_ID`
- `SERIAL_SYNC_TRACK_KEY`
- `SERIAL_SYNC_TRACK_NAME`
- `SERIAL_SYNC_RELEASE_ID`
- `SERIAL_SYNC_RELEASE_PROVIDER_ID`
- `SERIAL_SYNC_RELEASE_URL`
- `SERIAL_SYNC_RELEASE_TITLE`
- `SERIAL_SYNC_RELEASE_ROLE`
- `SERIAL_SYNC_ARTIFACT_ID`
- `SERIAL_SYNC_ARTIFACT_KIND`
- `SERIAL_SYNC_ARTIFACT_MIME`
- `SERIAL_SYNC_ARTIFACT_FILENAME`
- `SERIAL_SYNC_ARTIFACT_PATH`
- `SERIAL_SYNC_METADATA_JSON_PATH`
- `SERIAL_SYNC_NORMALIZED_JSON_PATH`
- `SERIAL_SYNC_RAW_JSON_PATH`

The hook also receives a JSON payload on stdin with the source, track, release, assignment, artifact, target, and run ID.

Use [`examples/hooks/log-publish.sh`](../examples/hooks/log-publish.sh) as the minimal reference script.

## Version 2: publish and supersede

```toml
[[publishers]]
id = "reader-library"
kind = "exec"
command = ["/config/hooks/reader-library"]
protocol_version = 2
enabled = true
```

Version 2 hooks consume JSON on stdin. Each event has `version = 2`, `event_id`,
`action`, and `target_id`:

- `publish` includes `source`, `track`, and `artifact`. Ordinary chapters also
  carry their `release` and `assignment`. A volume has `volume.id`, `group_id`,
  expected `first`/`last`, and ordered `members` with release IDs, input hashes and
  series positions; its release fields are empty.
- `supersede` includes `previous` (the prior publish record and artifact) and
  `replacements` (the acknowledged artifacts that cover it). The prior record's
  `filename` is the delivered name, including any collision suffix.

Exit zero acknowledges an event. Other exit statuses fail the target and leave
work pending. Serial-sync sends supersede only after the replacement deliveries
for that target succeed. A saved pending plan retains its artifact bytes and event
IDs across retries, even if newer corrections arrive. Unchanged acknowledged
deliveries are skipped on retry.
Event IDs identify a particular delivery. Restoring an edition that was previously
retired produces a new event ID, even when its artifact identity and bytes match
an earlier edition. Deduplicate by `event_id`, not by artifact ID or content hash.

Delivery is at least once: a process can stop after your command changes the
downstream library but before serial-sync records its acknowledgement. Store each
`event_id` durably and treat a repeated event as success. Keep your own mapping
from artifact identity to downstream receipt or book ID. For supersede, verify
that the downstream object still matches the previous artifact's identity/hash
before removing it; an already-retired object is success. Preserve a new edition
that occupies the old path.

Pending work retains its target settings and scope. Retry those settings before
changing the target or narrowing the scope. Detailed saved plans stay on disk;
the catalog keeps compact pending references and delivery receipts.

Legacy hooks (`protocol_version` absent or `1`) continue to receive their original
release-oriented payload. Selecting volumes or a required rename/retirement with
one of these hooks fails before replacement delivery to any selected target, with
an instruction to upgrade. Serial-sync does not send a new action to a legacy hook.
