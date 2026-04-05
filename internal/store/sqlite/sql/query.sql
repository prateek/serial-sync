-- name: UpsertSource :exec
INSERT INTO sources (
  id, provider, source_url, source_type, creator_id, creator_name,
  auth_profile_id, enabled, sync_cursor, last_synced_at
) VALUES (
  sqlc.arg(id), sqlc.arg(provider), sqlc.arg(source_url), sqlc.arg(source_type),
  sqlc.arg(creator_id), sqlc.arg(creator_name), sqlc.arg(auth_profile_id),
  sqlc.arg(enabled), sqlc.arg(sync_cursor), sqlc.arg(last_synced_at)
)
ON CONFLICT(id) DO UPDATE SET
  provider = excluded.provider,
  source_url = excluded.source_url,
  source_type = excluded.source_type,
  creator_id = excluded.creator_id,
  creator_name = excluded.creator_name,
  auth_profile_id = excluded.auth_profile_id,
  enabled = excluded.enabled,
  sync_cursor = excluded.sync_cursor,
  last_synced_at = excluded.last_synced_at;

-- name: ListSources :many
SELECT id, provider, source_url, source_type, creator_id, creator_name, auth_profile_id, enabled, sync_cursor, last_synced_at
FROM sources
ORDER BY id;

-- name: GetSource :one
SELECT id, provider, source_url, source_type, creator_id, creator_name, auth_profile_id, enabled, sync_cursor, last_synced_at
FROM sources
WHERE id = sqlc.arg(id);

-- name: UpdateSourceLastSyncedAt :exec
UPDATE sources
SET last_synced_at = sqlc.arg(last_synced_at)
WHERE id = sqlc.arg(id);

-- name: UpsertTrack :exec
INSERT INTO story_tracks (
  id, source_id, track_key, track_name, canonical_author, series_meta,
  output_policy, created_at, updated_at
) VALUES (
  sqlc.arg(id), sqlc.arg(source_id), sqlc.arg(track_key), sqlc.arg(track_name),
  sqlc.arg(canonical_author), sqlc.arg(series_meta), sqlc.arg(output_policy),
  sqlc.arg(created_at), sqlc.arg(updated_at)
)
ON CONFLICT(source_id, track_key) DO UPDATE SET
  track_name = excluded.track_name,
  canonical_author = excluded.canonical_author,
  series_meta = excluded.series_meta,
  output_policy = excluded.output_policy,
  updated_at = excluded.updated_at;

-- name: GetTrackBySourceAndKey :one
SELECT id, source_id, track_key, track_name, canonical_author, series_meta, output_policy, created_at, updated_at
FROM story_tracks
WHERE source_id = sqlc.arg(source_id) AND track_key = sqlc.arg(track_key);

-- name: GetTrack :one
SELECT id, source_id, track_key, track_name, canonical_author, series_meta, output_policy, created_at, updated_at
FROM story_tracks
WHERE id = sqlc.arg(id);

-- name: ListTracks :many
SELECT id, source_id, track_key, track_name, canonical_author, series_meta, output_policy, created_at, updated_at
FROM story_tracks
ORDER BY source_id, track_key;

-- name: ListTracksBySource :many
SELECT id, source_id, track_key, track_name, canonical_author, series_meta, output_policy, created_at, updated_at
FROM story_tracks
WHERE source_id = sqlc.arg(source_id)
ORDER BY source_id, track_key;

-- name: UpsertRelease :exec
INSERT INTO releases (
  id, source_id, provider_release_id, url, title, published_at, edited_at,
  post_type, visibility_state, normalized_payload_ref, raw_payload_ref,
  content_hash, discovered_at, status
) VALUES (
  sqlc.arg(id), sqlc.arg(source_id), sqlc.arg(provider_release_id), sqlc.arg(url),
  sqlc.arg(title), sqlc.arg(published_at), sqlc.arg(edited_at), sqlc.arg(post_type),
  sqlc.arg(visibility_state), sqlc.arg(normalized_payload_ref), sqlc.arg(raw_payload_ref),
  sqlc.arg(content_hash), sqlc.arg(discovered_at), sqlc.arg(status)
)
ON CONFLICT(source_id, provider_release_id) DO UPDATE SET
  url = excluded.url,
  title = excluded.title,
  published_at = excluded.published_at,
  edited_at = excluded.edited_at,
  post_type = excluded.post_type,
  visibility_state = excluded.visibility_state,
  normalized_payload_ref = excluded.normalized_payload_ref,
  raw_payload_ref = excluded.raw_payload_ref,
  content_hash = excluded.content_hash,
  discovered_at = excluded.discovered_at,
  status = excluded.status;

-- name: GetReleaseByProviderID :one
SELECT id, source_id, provider_release_id, url, title, published_at, edited_at, post_type, visibility_state, normalized_payload_ref, raw_payload_ref, content_hash, discovered_at, status
FROM releases
WHERE source_id = sqlc.arg(source_id) AND provider_release_id = sqlc.arg(provider_release_id);

-- name: GetRelease :one
SELECT id, source_id, provider_release_id, url, title, published_at, edited_at, post_type, visibility_state, normalized_payload_ref, raw_payload_ref, content_hash, discovered_at, status
FROM releases
WHERE id = sqlc.arg(id);

-- name: ListReleasesBySource :many
SELECT id, source_id, provider_release_id, url, title, published_at, edited_at, post_type, visibility_state, normalized_payload_ref, raw_payload_ref, content_hash, discovered_at, status
FROM releases
WHERE source_id = sqlc.arg(source_id)
ORDER BY published_at DESC;

-- name: UpsertReleaseAssignment :exec
INSERT INTO release_assignments (release_id, track_id, rule_id, release_role, confidence)
VALUES (
  sqlc.arg(release_id), sqlc.arg(track_id), sqlc.arg(rule_id), sqlc.arg(release_role), sqlc.arg(confidence)
)
ON CONFLICT(release_id) DO UPDATE SET
  track_id = excluded.track_id,
  rule_id = excluded.rule_id,
  release_role = excluded.release_role,
  confidence = excluded.confidence;

-- name: GetReleaseAssignment :one
SELECT release_id, track_id, rule_id, release_role, confidence
FROM release_assignments
WHERE release_id = sqlc.arg(release_id);

-- name: ClearCanonicalArtifactsForRelease :exec
UPDATE artifacts
SET is_canonical = 0
WHERE release_id = sqlc.arg(release_id);

-- name: UpsertArtifact :exec
INSERT INTO artifacts (
  id, release_id, track_id, artifact_kind, is_canonical, filename, mime_type,
  sha256, storage_ref, built_at, state, metadata_ref, normalized_ref, raw_ref
) VALUES (
  sqlc.arg(id), sqlc.arg(release_id), sqlc.arg(track_id), sqlc.arg(artifact_kind),
  sqlc.arg(is_canonical), sqlc.arg(filename), sqlc.arg(mime_type), sqlc.arg(sha256),
  sqlc.arg(storage_ref), sqlc.arg(built_at), sqlc.arg(state), sqlc.arg(metadata_ref),
  sqlc.arg(normalized_ref), sqlc.arg(raw_ref)
)
ON CONFLICT(release_id, sha256, artifact_kind) DO UPDATE SET
  track_id = excluded.track_id,
  is_canonical = excluded.is_canonical,
  filename = excluded.filename,
  mime_type = excluded.mime_type,
  storage_ref = excluded.storage_ref,
  built_at = excluded.built_at,
  state = excluded.state,
  metadata_ref = excluded.metadata_ref,
  normalized_ref = excluded.normalized_ref,
  raw_ref = excluded.raw_ref;

-- name: GetCanonicalArtifactByReleaseID :one
SELECT id, release_id, track_id, artifact_kind, is_canonical, filename, mime_type, sha256, storage_ref, built_at, state, metadata_ref, normalized_ref, raw_ref
FROM artifacts
WHERE release_id = sqlc.arg(release_id) AND is_canonical = 1
ORDER BY built_at DESC
LIMIT 1;

-- name: GetArtifact :one
SELECT id, release_id, track_id, artifact_kind, is_canonical, filename, mime_type, sha256, storage_ref, built_at, state, metadata_ref, normalized_ref, raw_ref
FROM artifacts
WHERE id = sqlc.arg(id);

-- name: ListArtifactsByReleaseID :many
SELECT id, release_id, track_id, artifact_kind, is_canonical, filename, mime_type, sha256, storage_ref, built_at, state, metadata_ref, normalized_ref, raw_ref
FROM artifacts
WHERE release_id = sqlc.arg(release_id)
ORDER BY built_at DESC;

-- name: InsertRunRecord :exec
INSERT INTO run_records (id, command, started_at, finished_at, status, summary, source_scope, dry_run)
VALUES (
  sqlc.arg(id), sqlc.arg(command), sqlc.arg(started_at), sqlc.arg(finished_at),
  sqlc.arg(status), sqlc.arg(summary), sqlc.arg(source_scope), sqlc.arg(dry_run)
);

-- name: UpdateRunRecord :exec
UPDATE run_records
SET finished_at = sqlc.arg(finished_at), status = sqlc.arg(status), summary = sqlc.arg(summary)
WHERE id = sqlc.arg(id);

-- name: GetRunRecord :one
SELECT id, command, started_at, finished_at, status, summary, source_scope, dry_run
FROM run_records
WHERE id = sqlc.arg(id);

-- name: ListRunRecordsRecent :many
SELECT id, command, started_at, finished_at, status, summary, source_scope, dry_run
FROM run_records
ORDER BY started_at DESC, id DESC
LIMIT sqlc.arg(limit);

-- name: InsertEventRecord :exec
INSERT INTO event_records (id, run_id, timestamp, level, component, message, entity_kind, entity_id, payload_ref)
VALUES (
  sqlc.arg(id), sqlc.arg(run_id), sqlc.arg(timestamp), sqlc.arg(level),
  sqlc.arg(component), sqlc.arg(message), sqlc.arg(entity_kind), sqlc.arg(entity_id),
  sqlc.arg(payload_ref)
);

-- name: ListEventsByRunID :many
SELECT id, run_id, timestamp, level, component, message, entity_kind, entity_id, payload_ref
FROM event_records
WHERE run_id = sqlc.arg(run_id)
ORDER BY timestamp;

-- name: CountSuccessfulPublishRecords :one
SELECT COUNT(1)
FROM publish_records
WHERE artifact_id = sqlc.arg(artifact_id)
  AND target_id = sqlc.arg(target_id)
  AND publish_hash = sqlc.arg(publish_hash)
  AND status = sqlc.arg(status);

-- name: UpsertPublishRecord :exec
INSERT INTO publish_records (id, artifact_id, target_id, target_kind, target_ref, publish_hash, published_at, status, message)
VALUES (
  sqlc.arg(id), sqlc.arg(artifact_id), sqlc.arg(target_id), sqlc.arg(target_kind),
  sqlc.arg(target_ref), sqlc.arg(publish_hash), sqlc.arg(published_at), sqlc.arg(status),
  sqlc.arg(message)
)
ON CONFLICT(artifact_id, target_id, publish_hash) DO UPDATE SET
  target_kind = excluded.target_kind,
  target_ref = excluded.target_ref,
  published_at = excluded.published_at,
  status = excluded.status,
  message = excluded.message;

-- name: ListPublishRecords :many
SELECT
  pr.id, pr.artifact_id, pr.target_id, pr.target_kind, pr.target_ref, pr.publish_hash, pr.published_at, pr.status, pr.message,
  art.id, art.release_id, art.track_id, art.artifact_kind, art.is_canonical, art.filename, art.mime_type, art.sha256, art.storage_ref, art.built_at, art.state, art.metadata_ref, art.normalized_ref, art.raw_ref,
  r.id, r.source_id, r.provider_release_id, r.url, r.title, r.published_at, r.edited_at, r.post_type, r.visibility_state, r.normalized_payload_ref, r.raw_payload_ref, r.content_hash, r.discovered_at, r.status,
  s.id, s.provider, s.source_url, s.source_type, s.creator_id, s.creator_name, s.auth_profile_id, s.enabled, s.sync_cursor, s.last_synced_at,
  t.id, t.source_id, t.track_key, t.track_name, t.canonical_author, t.series_meta, t.output_policy, t.created_at, t.updated_at
FROM publish_records pr
JOIN artifacts art ON art.id = pr.artifact_id
JOIN releases r ON r.id = art.release_id
JOIN sources s ON s.id = r.source_id
LEFT JOIN story_tracks t ON t.id = art.track_id
ORDER BY pr.published_at DESC, pr.id DESC;

-- name: ListPublishRecordsBySource :many
SELECT
  pr.id, pr.artifact_id, pr.target_id, pr.target_kind, pr.target_ref, pr.publish_hash, pr.published_at, pr.status, pr.message,
  art.id, art.release_id, art.track_id, art.artifact_kind, art.is_canonical, art.filename, art.mime_type, art.sha256, art.storage_ref, art.built_at, art.state, art.metadata_ref, art.normalized_ref, art.raw_ref,
  r.id, r.source_id, r.provider_release_id, r.url, r.title, r.published_at, r.edited_at, r.post_type, r.visibility_state, r.normalized_payload_ref, r.raw_payload_ref, r.content_hash, r.discovered_at, r.status,
  s.id, s.provider, s.source_url, s.source_type, s.creator_id, s.creator_name, s.auth_profile_id, s.enabled, s.sync_cursor, s.last_synced_at,
  t.id, t.source_id, t.track_key, t.track_name, t.canonical_author, t.series_meta, t.output_policy, t.created_at, t.updated_at
FROM publish_records pr
JOIN artifacts art ON art.id = pr.artifact_id
JOIN releases r ON r.id = art.release_id
JOIN sources s ON s.id = r.source_id
LEFT JOIN story_tracks t ON t.id = art.track_id
WHERE s.id = sqlc.arg(source_id)
ORDER BY pr.published_at DESC, pr.id DESC;

-- name: ListPublishRecordsByTarget :many
SELECT
  pr.id, pr.artifact_id, pr.target_id, pr.target_kind, pr.target_ref, pr.publish_hash, pr.published_at, pr.status, pr.message,
  art.id, art.release_id, art.track_id, art.artifact_kind, art.is_canonical, art.filename, art.mime_type, art.sha256, art.storage_ref, art.built_at, art.state, art.metadata_ref, art.normalized_ref, art.raw_ref,
  r.id, r.source_id, r.provider_release_id, r.url, r.title, r.published_at, r.edited_at, r.post_type, r.visibility_state, r.normalized_payload_ref, r.raw_payload_ref, r.content_hash, r.discovered_at, r.status,
  s.id, s.provider, s.source_url, s.source_type, s.creator_id, s.creator_name, s.auth_profile_id, s.enabled, s.sync_cursor, s.last_synced_at,
  t.id, t.source_id, t.track_key, t.track_name, t.canonical_author, t.series_meta, t.output_policy, t.created_at, t.updated_at
FROM publish_records pr
JOIN artifacts art ON art.id = pr.artifact_id
JOIN releases r ON r.id = art.release_id
JOIN sources s ON s.id = r.source_id
LEFT JOIN story_tracks t ON t.id = art.track_id
WHERE pr.target_id = sqlc.arg(target_id)
ORDER BY pr.published_at DESC, pr.id DESC;

-- name: ListPublishRecordsBySourceAndTarget :many
SELECT
  pr.id, pr.artifact_id, pr.target_id, pr.target_kind, pr.target_ref, pr.publish_hash, pr.published_at, pr.status, pr.message,
  art.id, art.release_id, art.track_id, art.artifact_kind, art.is_canonical, art.filename, art.mime_type, art.sha256, art.storage_ref, art.built_at, art.state, art.metadata_ref, art.normalized_ref, art.raw_ref,
  r.id, r.source_id, r.provider_release_id, r.url, r.title, r.published_at, r.edited_at, r.post_type, r.visibility_state, r.normalized_payload_ref, r.raw_payload_ref, r.content_hash, r.discovered_at, r.status,
  s.id, s.provider, s.source_url, s.source_type, s.creator_id, s.creator_name, s.auth_profile_id, s.enabled, s.sync_cursor, s.last_synced_at,
  t.id, t.source_id, t.track_key, t.track_name, t.canonical_author, t.series_meta, t.output_policy, t.created_at, t.updated_at
FROM publish_records pr
JOIN artifacts art ON art.id = pr.artifact_id
JOIN releases r ON r.id = art.release_id
JOIN sources s ON s.id = r.source_id
LEFT JOIN story_tracks t ON t.id = art.track_id
WHERE s.id = sqlc.arg(source_id)
  AND pr.target_id = sqlc.arg(target_id)
ORDER BY pr.published_at DESC, pr.id DESC;

-- name: GetPublishRecordBundle :one
SELECT
  pr.id, pr.artifact_id, pr.target_id, pr.target_kind, pr.target_ref, pr.publish_hash, pr.published_at, pr.status, pr.message,
  art.id, art.release_id, art.track_id, art.artifact_kind, art.is_canonical, art.filename, art.mime_type, art.sha256, art.storage_ref, art.built_at, art.state, art.metadata_ref, art.normalized_ref, art.raw_ref,
  r.id, r.source_id, r.provider_release_id, r.url, r.title, r.published_at, r.edited_at, r.post_type, r.visibility_state, r.normalized_payload_ref, r.raw_payload_ref, r.content_hash, r.discovered_at, r.status,
  s.id, s.provider, s.source_url, s.source_type, s.creator_id, s.creator_name, s.auth_profile_id, s.enabled, s.sync_cursor, s.last_synced_at,
  t.id, t.source_id, t.track_key, t.track_name, t.canonical_author, t.series_meta, t.output_policy, t.created_at, t.updated_at
FROM publish_records pr
JOIN artifacts art ON art.id = pr.artifact_id
JOIN releases r ON r.id = art.release_id
JOIN sources s ON s.id = r.source_id
LEFT JOIN story_tracks t ON t.id = art.track_id
WHERE pr.id = sqlc.arg(id);

-- name: AcquireLease :execrows
INSERT INTO leases (key, holder, expires_at, updated_at)
VALUES (sqlc.arg(key), sqlc.arg(holder), sqlc.arg(expires_at), sqlc.arg(updated_at))
ON CONFLICT(key) DO UPDATE SET
  holder = excluded.holder,
  expires_at = excluded.expires_at,
  updated_at = excluded.updated_at
WHERE leases.expires_at <= excluded.updated_at
   OR leases.holder = excluded.holder;

-- name: ReleaseLease :exec
DELETE FROM leases
WHERE key = sqlc.arg(key) AND holder = sqlc.arg(holder);

-- name: ListPublishCandidates :many
SELECT
  s.id, s.provider, s.source_url, s.source_type, s.creator_id, s.creator_name, s.auth_profile_id, s.enabled, s.sync_cursor, s.last_synced_at,
  t.id, t.source_id, t.track_key, t.track_name, t.canonical_author, t.series_meta, t.output_policy, t.created_at, t.updated_at,
  r.id, r.source_id, r.provider_release_id, r.url, r.title, r.published_at, r.edited_at, r.post_type, r.visibility_state, r.normalized_payload_ref, r.raw_payload_ref, r.content_hash, r.discovered_at, r.status,
  a.release_id, a.track_id, a.rule_id, a.release_role, a.confidence,
  art.id, art.release_id, art.track_id, art.artifact_kind, art.is_canonical, art.filename, art.mime_type, art.sha256, art.storage_ref, art.built_at, art.state, art.metadata_ref, art.normalized_ref, art.raw_ref
FROM artifacts art
JOIN releases r ON r.id = art.release_id
JOIN sources s ON s.id = r.source_id
LEFT JOIN release_assignments a ON a.release_id = r.id
LEFT JOIN story_tracks t ON t.id = a.track_id
WHERE art.is_canonical = 1
ORDER BY s.id, t.track_key, r.published_at;

-- name: ListPublishCandidatesBySource :many
SELECT
  s.id, s.provider, s.source_url, s.source_type, s.creator_id, s.creator_name, s.auth_profile_id, s.enabled, s.sync_cursor, s.last_synced_at,
  t.id, t.source_id, t.track_key, t.track_name, t.canonical_author, t.series_meta, t.output_policy, t.created_at, t.updated_at,
  r.id, r.source_id, r.provider_release_id, r.url, r.title, r.published_at, r.edited_at, r.post_type, r.visibility_state, r.normalized_payload_ref, r.raw_payload_ref, r.content_hash, r.discovered_at, r.status,
  a.release_id, a.track_id, a.rule_id, a.release_role, a.confidence,
  art.id, art.release_id, art.track_id, art.artifact_kind, art.is_canonical, art.filename, art.mime_type, art.sha256, art.storage_ref, art.built_at, art.state, art.metadata_ref, art.normalized_ref, art.raw_ref
FROM artifacts art
JOIN releases r ON r.id = art.release_id
JOIN sources s ON s.id = r.source_id
LEFT JOIN release_assignments a ON a.release_id = r.id
LEFT JOIN story_tracks t ON t.id = a.track_id
WHERE art.is_canonical = 1 AND s.id = sqlc.arg(source_id)
ORDER BY s.id, t.track_key, r.published_at;
