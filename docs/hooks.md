# Hook Tutorial

`serial-sync` now ships an `exec` publisher for post-publish automation.

The model is:

1. `sync` materializes canonical artifacts into durable local storage.
2. `publish` replays those artifacts to one or more targets.
3. An `exec` publisher receives stable file paths and metadata sidecars after the artifact already exists on disk.

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
