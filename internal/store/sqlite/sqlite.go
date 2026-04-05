package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
)

type Store struct {
	db *sql.DB
}

type querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	schema := `
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
CREATE TABLE IF NOT EXISTS sources (
  id TEXT PRIMARY KEY,
  provider TEXT NOT NULL,
  source_url TEXT NOT NULL,
  source_type TEXT NOT NULL,
  creator_id TEXT NOT NULL,
  creator_name TEXT NOT NULL,
  auth_profile_id TEXT NOT NULL,
  enabled INTEGER NOT NULL,
  sync_cursor TEXT NOT NULL,
  last_synced_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS story_tracks (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL,
  track_key TEXT NOT NULL,
  track_name TEXT NOT NULL,
  canonical_author TEXT NOT NULL,
  series_meta TEXT NOT NULL,
  output_policy TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(source_id, track_key)
);
CREATE TABLE IF NOT EXISTS releases (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL,
  provider_release_id TEXT NOT NULL,
  url TEXT NOT NULL,
  title TEXT NOT NULL,
  published_at TEXT NOT NULL,
  edited_at TEXT NOT NULL,
  post_type TEXT NOT NULL,
  visibility_state TEXT NOT NULL,
  normalized_payload_ref TEXT NOT NULL,
  raw_payload_ref TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  discovered_at TEXT NOT NULL,
  status TEXT NOT NULL,
  UNIQUE(source_id, provider_release_id)
);
CREATE TABLE IF NOT EXISTS release_assignments (
  release_id TEXT PRIMARY KEY,
  track_id TEXT NOT NULL,
  rule_id TEXT NOT NULL,
  release_role TEXT NOT NULL,
  confidence REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS artifacts (
  id TEXT PRIMARY KEY,
  release_id TEXT NOT NULL,
  track_id TEXT NOT NULL,
  artifact_kind TEXT NOT NULL,
  is_canonical INTEGER NOT NULL,
  filename TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  storage_ref TEXT NOT NULL,
  built_at TEXT NOT NULL,
  state TEXT NOT NULL,
  metadata_ref TEXT NOT NULL,
  normalized_ref TEXT NOT NULL,
  raw_ref TEXT NOT NULL,
  UNIQUE(release_id, sha256, artifact_kind)
);
CREATE TABLE IF NOT EXISTS publish_records (
  id TEXT PRIMARY KEY,
  artifact_id TEXT NOT NULL,
  target_id TEXT NOT NULL,
  target_kind TEXT NOT NULL,
  target_ref TEXT NOT NULL,
  publish_hash TEXT NOT NULL,
  published_at TEXT NOT NULL,
  status TEXT NOT NULL,
  message TEXT NOT NULL,
  UNIQUE(artifact_id, target_id, publish_hash)
);
CREATE TABLE IF NOT EXISTS run_records (
  id TEXT PRIMARY KEY,
  command TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  status TEXT NOT NULL,
  summary TEXT NOT NULL,
  source_scope TEXT NOT NULL,
  dry_run INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS event_records (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL,
  timestamp TEXT NOT NULL,
  level TEXT NOT NULL,
  component TEXT NOT NULL,
  message TEXT NOT NULL,
  entity_kind TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  payload_ref TEXT NOT NULL
);`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *Store) UpsertSource(ctx context.Context, source domain.Source) error {
	return upsertSource(ctx, s.db, source)
}

func (s *Store) ListSources(ctx context.Context) ([]domain.Source, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, provider, source_url, source_type, creator_id, creator_name, auth_profile_id, enabled, sync_cursor, last_synced_at FROM sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.Source
	for rows.Next() {
		item, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetSource(ctx context.Context, id string) (*domain.Source, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, provider, source_url, source_type, creator_id, creator_name, auth_profile_id, enabled, sync_cursor, last_synced_at FROM sources WHERE id = ?`, id)
	item, err := scanSourceRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) UpsertTrack(ctx context.Context, track domain.StoryTrack) (*domain.StoryTrack, error) {
	return upsertTrack(ctx, s.db, track)
}

func (s *Store) GetTrackBySourceAndKey(ctx context.Context, sourceID, trackKey string) (*domain.StoryTrack, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, source_id, track_key, track_name, canonical_author, series_meta, output_policy, created_at, updated_at FROM story_tracks WHERE source_id = ? AND track_key = ?`, sourceID, trackKey)
	item, err := scanTrackRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) GetTrack(ctx context.Context, id string) (*domain.StoryTrack, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, source_id, track_key, track_name, canonical_author, series_meta, output_policy, created_at, updated_at FROM story_tracks WHERE id = ?`, id)
	item, err := scanTrackRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) ListTracks(ctx context.Context, sourceID string) ([]domain.StoryTrack, error) {
	query := `SELECT id, source_id, track_key, track_name, canonical_author, series_meta, output_policy, created_at, updated_at FROM story_tracks`
	args := []any{}
	if sourceID != "" {
		query += ` WHERE source_id = ?`
		args = append(args, sourceID)
	}
	query += ` ORDER BY source_id, track_key`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.StoryTrack
	for rows.Next() {
		item, err := scanTrack(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetReleaseByProviderID(ctx context.Context, sourceID, providerReleaseID string) (*domain.Release, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, source_id, provider_release_id, url, title, published_at, edited_at, post_type, visibility_state, normalized_payload_ref, raw_payload_ref, content_hash, discovered_at, status FROM releases WHERE source_id = ? AND provider_release_id = ?`, sourceID, providerReleaseID)
	item, err := scanReleaseRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) GetReleaseBundle(ctx context.Context, id string) (*domain.ReleaseBundle, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, source_id, provider_release_id, url, title, published_at, edited_at, post_type, visibility_state, normalized_payload_ref, raw_payload_ref, content_hash, discovered_at, status FROM releases WHERE id = ?`, id)
	release, err := scanReleaseRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	source, err := s.GetSource(ctx, release.SourceID)
	if err != nil {
		return nil, err
	}
	assignment, err := s.getAssignment(ctx, release.ID)
	if err != nil {
		return nil, err
	}
	var track domain.StoryTrack
	if assignment != nil {
		trackPtr, err := s.GetTrack(ctx, assignment.TrackID)
		if err != nil {
			return nil, err
		}
		if trackPtr != nil {
			track = *trackPtr
		}
	}
	artifacts, err := s.ListArtifactsByReleaseID(ctx, release.ID)
	if err != nil {
		return nil, err
	}
	result := &domain.ReleaseBundle{
		Release:   release,
		Artifacts: artifacts,
	}
	if source != nil {
		result.Source = *source
	}
	if assignment != nil {
		result.Assignment = *assignment
	}
	result.Track = track
	return result, nil
}

func (s *Store) ListReleases(ctx context.Context, sourceID string) ([]domain.Release, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, source_id, provider_release_id, url, title, published_at, edited_at, post_type, visibility_state, normalized_payload_ref, raw_payload_ref, content_hash, discovered_at, status FROM releases WHERE source_id = ? ORDER BY published_at DESC`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.Release
	for rows.Next() {
		item, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetCanonicalArtifactByReleaseID(ctx context.Context, releaseID string) (*domain.Artifact, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, release_id, track_id, artifact_kind, is_canonical, filename, mime_type, sha256, storage_ref, built_at, state, metadata_ref, normalized_ref, raw_ref FROM artifacts WHERE release_id = ? AND is_canonical = 1 ORDER BY built_at DESC LIMIT 1`, releaseID)
	item, err := scanArtifactRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) GetArtifact(ctx context.Context, id string) (*domain.Artifact, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, release_id, track_id, artifact_kind, is_canonical, filename, mime_type, sha256, storage_ref, built_at, state, metadata_ref, normalized_ref, raw_ref FROM artifacts WHERE id = ?`, id)
	item, err := scanArtifactRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) ListArtifactsByReleaseID(ctx context.Context, releaseID string) ([]domain.Artifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, release_id, track_id, artifact_kind, is_canonical, filename, mime_type, sha256, storage_ref, built_at, state, metadata_ref, normalized_ref, raw_ref FROM artifacts WHERE release_id = ? ORDER BY built_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.Artifact
	for rows.Next() {
		item, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) SaveSyncSnapshot(ctx context.Context, snapshot store.SyncSnapshot) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := upsertSource(ctx, tx, snapshot.Source); err != nil {
		return err
	}
	track, err := upsertTrack(ctx, tx, snapshot.Track)
	if err != nil {
		return err
	}
	snapshot.Assignment.TrackID = track.ID
	snapshot.Artifact.TrackID = track.ID
	if _, err := tx.ExecContext(ctx, `
INSERT INTO releases (id, source_id, provider_release_id, url, title, published_at, edited_at, post_type, visibility_state, normalized_payload_ref, raw_payload_ref, content_hash, discovered_at, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
  status = excluded.status`,
		snapshot.Release.ID, snapshot.Release.SourceID, snapshot.Release.ProviderReleaseID, snapshot.Release.URL, snapshot.Release.Title,
		formatTime(snapshot.Release.PublishedAt), formatTime(snapshot.Release.EditedAt), snapshot.Release.PostType, snapshot.Release.VisibilityState,
		snapshot.Release.NormalizedPayloadRef, snapshot.Release.RawPayloadRef, snapshot.Release.ContentHash,
		formatTime(snapshot.Release.DiscoveredAt), snapshot.Release.Status,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO release_assignments (release_id, track_id, rule_id, release_role, confidence)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(release_id) DO UPDATE SET
  track_id = excluded.track_id,
  rule_id = excluded.rule_id,
  release_role = excluded.release_role,
  confidence = excluded.confidence`,
		snapshot.Assignment.ReleaseID, snapshot.Assignment.TrackID, snapshot.Assignment.RuleID, string(snapshot.Assignment.ReleaseRole), snapshot.Assignment.Confidence,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE artifacts SET is_canonical = 0 WHERE release_id = ?`, snapshot.Artifact.ReleaseID); err != nil {
		return err
	}
	if snapshot.Artifact.ID != "" {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO artifacts (id, release_id, track_id, artifact_kind, is_canonical, filename, mime_type, sha256, storage_ref, built_at, state, metadata_ref, normalized_ref, raw_ref)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
  raw_ref = excluded.raw_ref`,
			snapshot.Artifact.ID, snapshot.Artifact.ReleaseID, snapshot.Artifact.TrackID, snapshot.Artifact.ArtifactKind,
			boolInt(snapshot.Artifact.IsCanonical), snapshot.Artifact.Filename, snapshot.Artifact.MIMEType, snapshot.Artifact.SHA256,
			snapshot.Artifact.StorageRef, formatTime(snapshot.Artifact.BuiltAt), string(snapshot.Artifact.State),
			snapshot.Artifact.MetadataRef, snapshot.Artifact.NormalizedRef, snapshot.Artifact.RawRef,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sources SET last_synced_at = ? WHERE id = ?`, formatTime(time.Now().UTC()), snapshot.Source.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) StartRun(ctx context.Context, run domain.RunRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_records (id, command, started_at, finished_at, status, summary, source_scope, dry_run) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.Command, formatTime(run.StartedAt), nilStringPtr(run.FinishedAt), string(run.Status), run.Summary, run.SourceScope, boolInt(run.DryRun))
	return err
}

func (s *Store) FinishRun(ctx context.Context, runID string, status domain.RunStatus, summary string) error {
	finished := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `UPDATE run_records SET finished_at = ?, status = ?, summary = ? WHERE id = ?`, formatTime(finished), string(status), summary, runID)
	return err
}

func (s *Store) AddEvent(ctx context.Context, event domain.EventRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO event_records (id, run_id, timestamp, level, component, message, entity_kind, entity_id, payload_ref) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.RunID, formatTime(event.Timestamp), event.Level, event.Component, event.Message, event.EntityKind, event.EntityID, event.PayloadRef)
	return err
}

func (s *Store) GetRunBundle(ctx context.Context, runID string) (*domain.RunBundle, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, command, started_at, finished_at, status, summary, source_scope, dry_run FROM run_records WHERE id = ?`, runID)
	run, err := scanRunRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, run_id, timestamp, level, component, message, entity_kind, entity_id, payload_ref FROM event_records WHERE run_id = ? ORDER BY timestamp`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []domain.EventRecord
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return &domain.RunBundle{Run: run, Events: events}, rows.Err()
}

func (s *Store) ListPublishCandidates(ctx context.Context, sourceID string) ([]domain.PublishCandidate, error) {
	query := `
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
WHERE art.is_canonical = 1`
	args := []any{}
	if sourceID != "" {
		query += ` AND s.id = ?`
		args = append(args, sourceID)
	}
	query += ` ORDER BY s.id, t.track_key, r.published_at`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []domain.PublishCandidate
	for rows.Next() {
		var source domain.Source
		var track domain.StoryTrack
		var release domain.Release
		var assignment domain.ReleaseAssignment
		var artifact domain.Artifact
		var enabled int
		var isCanonical int
		var confidence float64
		var sourceLastSynced, trackCreated, trackUpdated, releasePublished, releaseEdited, releaseDiscovered, artifactBuilt string
		if err := rows.Scan(
			&source.ID, &source.Provider, &source.SourceURL, &source.SourceType, &source.CreatorID, &source.CreatorName, &source.AuthProfileID, &enabled, &source.SyncCursor, &sourceLastSynced,
			&track.ID, &track.SourceID, &track.TrackKey, &track.TrackName, &track.CanonicalAuthor, &track.SeriesMeta, &track.OutputPolicy, &trackCreated, &trackUpdated,
			&release.ID, &release.SourceID, &release.ProviderReleaseID, &release.URL, &release.Title, &releasePublished, &releaseEdited, &release.PostType, &release.VisibilityState, &release.NormalizedPayloadRef, &release.RawPayloadRef, &release.ContentHash, &releaseDiscovered, &release.Status,
			&assignment.ReleaseID, &assignment.TrackID, &assignment.RuleID, &assignment.ReleaseRole, &confidence,
			&artifact.ID, &artifact.ReleaseID, &artifact.TrackID, &artifact.ArtifactKind, &isCanonical, &artifact.Filename, &artifact.MIMEType, &artifact.SHA256, &artifact.StorageRef, &artifactBuilt, &artifact.State, &artifact.MetadataRef, &artifact.NormalizedRef, &artifact.RawRef,
		); err != nil {
			return nil, err
		}
		source.Enabled = enabled == 1
		source.LastSyncedAt = parseTime(sourceLastSynced)
		track.CreatedAt = parseTime(trackCreated)
		track.UpdatedAt = parseTime(trackUpdated)
		release.PublishedAt = parseTime(releasePublished)
		release.EditedAt = parseTime(releaseEdited)
		release.DiscoveredAt = parseTime(releaseDiscovered)
		assignment.Confidence = confidence
		artifact.IsCanonical = isCanonical == 1
		artifact.BuiltAt = parseTime(artifactBuilt)
		items = append(items, domain.PublishCandidate{
			Source:     source,
			Track:      track,
			Release:    release,
			Assignment: assignment,
			Artifact:   artifact,
		})
	}
	return items, rows.Err()
}

func (s *Store) HasSuccessfulPublish(ctx context.Context, artifactID, targetID, publishHash string) (bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM publish_records WHERE artifact_id = ? AND target_id = ? AND publish_hash = ? AND status = ?`, artifactID, targetID, publishHash, string(domain.PublishStatusPublished))
	var count int
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) UpsertPublishRecord(ctx context.Context, record domain.PublishRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO publish_records (id, artifact_id, target_id, target_kind, target_ref, publish_hash, published_at, status, message)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(artifact_id, target_id, publish_hash) DO UPDATE SET
  target_kind = excluded.target_kind,
  target_ref = excluded.target_ref,
  published_at = excluded.published_at,
  status = excluded.status,
  message = excluded.message`,
		record.ID, record.ArtifactID, record.TargetID, record.TargetKind, record.TargetRef, record.PublishHash,
		formatTime(record.PublishedAt), string(record.Status), record.Message,
	)
	return err
}

func (s *Store) getAssignment(ctx context.Context, releaseID string) (*domain.ReleaseAssignment, error) {
	row := s.db.QueryRowContext(ctx, `SELECT release_id, track_id, rule_id, release_role, confidence FROM release_assignments WHERE release_id = ?`, releaseID)
	var item domain.ReleaseAssignment
	var role string
	if err := row.Scan(&item.ReleaseID, &item.TrackID, &item.RuleID, &role, &item.Confidence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	item.ReleaseRole = domain.ReleaseRole(role)
	return &item, nil
}

func upsertSource(ctx context.Context, q querier, source domain.Source) error {
	_, err := q.ExecContext(ctx, `
INSERT INTO sources (id, provider, source_url, source_type, creator_id, creator_name, auth_profile_id, enabled, sync_cursor, last_synced_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  provider = excluded.provider,
  source_url = excluded.source_url,
  source_type = excluded.source_type,
  creator_id = excluded.creator_id,
  creator_name = excluded.creator_name,
  auth_profile_id = excluded.auth_profile_id,
  enabled = excluded.enabled,
  sync_cursor = excluded.sync_cursor,
  last_synced_at = excluded.last_synced_at`,
		source.ID, source.Provider, source.SourceURL, source.SourceType, source.CreatorID, source.CreatorName, source.AuthProfileID, boolInt(source.Enabled), source.SyncCursor, formatTime(source.LastSyncedAt),
	)
	return err
}

func upsertTrack(ctx context.Context, q querier, track domain.StoryTrack) (*domain.StoryTrack, error) {
	now := time.Now().UTC()
	createdAt := track.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := track.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	_, err := q.ExecContext(ctx, `
INSERT INTO story_tracks (id, source_id, track_key, track_name, canonical_author, series_meta, output_policy, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source_id, track_key) DO UPDATE SET
  track_name = excluded.track_name,
  canonical_author = excluded.canonical_author,
  series_meta = excluded.series_meta,
  output_policy = excluded.output_policy,
  updated_at = excluded.updated_at`,
		track.ID, track.SourceID, track.TrackKey, track.TrackName, track.CanonicalAuthor, track.SeriesMeta, track.OutputPolicy, formatTime(createdAt), formatTime(updatedAt),
	)
	if err != nil {
		return nil, err
	}
	row := q.QueryRowContext(ctx, `SELECT id, source_id, track_key, track_name, canonical_author, series_meta, output_policy, created_at, updated_at FROM story_tracks WHERE source_id = ? AND track_key = ?`, track.SourceID, track.TrackKey)
	item, err := scanTrackRow(row)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func scanSource(rows interface{ Scan(...any) error }) (domain.Source, error) {
	var item domain.Source
	var enabled int
	var lastSynced string
	err := rows.Scan(&item.ID, &item.Provider, &item.SourceURL, &item.SourceType, &item.CreatorID, &item.CreatorName, &item.AuthProfileID, &enabled, &item.SyncCursor, &lastSynced)
	if err != nil {
		return domain.Source{}, err
	}
	item.Enabled = enabled == 1
	item.LastSyncedAt = parseTime(lastSynced)
	return item, nil
}

func scanSourceRow(row *sql.Row) (domain.Source, error) {
	return scanSource(row)
}

func scanTrack(rows interface{ Scan(...any) error }) (domain.StoryTrack, error) {
	var item domain.StoryTrack
	var createdAt, updatedAt string
	err := rows.Scan(&item.ID, &item.SourceID, &item.TrackKey, &item.TrackName, &item.CanonicalAuthor, &item.SeriesMeta, &item.OutputPolicy, &createdAt, &updatedAt)
	if err != nil {
		return domain.StoryTrack{}, err
	}
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, nil
}

func scanTrackRow(row *sql.Row) (domain.StoryTrack, error) {
	return scanTrack(row)
}

func scanRelease(rows interface{ Scan(...any) error }) (domain.Release, error) {
	var item domain.Release
	var publishedAt, editedAt, discoveredAt string
	err := rows.Scan(&item.ID, &item.SourceID, &item.ProviderReleaseID, &item.URL, &item.Title, &publishedAt, &editedAt, &item.PostType, &item.VisibilityState, &item.NormalizedPayloadRef, &item.RawPayloadRef, &item.ContentHash, &discoveredAt, &item.Status)
	if err != nil {
		return domain.Release{}, err
	}
	item.PublishedAt = parseTime(publishedAt)
	item.EditedAt = parseTime(editedAt)
	item.DiscoveredAt = parseTime(discoveredAt)
	return item, nil
}

func scanReleaseRow(row *sql.Row) (domain.Release, error) {
	return scanRelease(row)
}

func scanArtifact(rows interface{ Scan(...any) error }) (domain.Artifact, error) {
	var item domain.Artifact
	var isCanonical int
	var builtAt string
	err := rows.Scan(&item.ID, &item.ReleaseID, &item.TrackID, &item.ArtifactKind, &isCanonical, &item.Filename, &item.MIMEType, &item.SHA256, &item.StorageRef, &builtAt, &item.State, &item.MetadataRef, &item.NormalizedRef, &item.RawRef)
	if err != nil {
		return domain.Artifact{}, err
	}
	item.IsCanonical = isCanonical == 1
	item.BuiltAt = parseTime(builtAt)
	return item, nil
}

func scanArtifactRow(row *sql.Row) (domain.Artifact, error) {
	return scanArtifact(row)
}

func scanRunRow(row *sql.Row) (domain.RunRecord, error) {
	var item domain.RunRecord
	var startedAt string
	var finishedAt sql.NullString
	var dryRun int
	var status string
	if err := row.Scan(&item.ID, &item.Command, &startedAt, &finishedAt, &status, &item.Summary, &item.SourceScope, &dryRun); err != nil {
		return domain.RunRecord{}, err
	}
	item.StartedAt = parseTime(startedAt)
	if finishedAt.Valid {
		finished := parseTime(finishedAt.String)
		item.FinishedAt = &finished
	}
	item.Status = domain.RunStatus(status)
	item.DryRun = dryRun == 1
	return item, nil
}

func scanEvent(rows interface{ Scan(...any) error }) (domain.EventRecord, error) {
	var item domain.EventRecord
	var timestamp string
	if err := rows.Scan(&item.ID, &item.RunID, &timestamp, &item.Level, &item.Component, &item.Message, &item.EntityKind, &item.EntityID, &item.PayloadRef); err != nil {
		return domain.EventRecord{}, err
	}
	item.Timestamp = parseTime(timestamp)
	return item, nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nilStringPtr(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

var _ store.Repository = (*Store)(nil)
