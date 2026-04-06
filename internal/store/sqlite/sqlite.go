package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
	sqldb "github.com/prateek/serial-sync/internal/store/sqlite/db"
)

//go:embed sql/schema.sql
var schemaSQL string

type Store struct {
	db      *sql.DB
	queries *sqldb.Queries
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
	return &Store{
		db:      db,
		queries: sqldb.New(db),
	}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA journal_mode = WAL;`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA busy_timeout = 5000;`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, schemaSQL)
	return err
}

func (s *Store) UpsertSource(ctx context.Context, source domain.Source) error {
	return s.queries.UpsertSource(ctx, sqldb.UpsertSourceParams{
		ID:            source.ID,
		Provider:      source.Provider,
		SourceUrl:     source.SourceURL,
		SourceType:    source.SourceType,
		CreatorID:     source.CreatorID,
		CreatorName:   source.CreatorName,
		AuthProfileID: source.AuthProfileID,
		Enabled:       boolInt(source.Enabled),
		SyncCursor:    source.SyncCursor,
		LastSyncedAt:  formatTime(source.LastSyncedAt),
	})
}

func (s *Store) ListSources(ctx context.Context) ([]domain.Source, error) {
	rows, err := s.queries.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Source, 0, len(rows))
	for _, row := range rows {
		items = append(items, sourceFromRow(row))
	}
	return items, nil
}

func (s *Store) GetSource(ctx context.Context, id string) (*domain.Source, error) {
	row, err := s.queries.GetSource(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item := sourceFromRow(row)
	return &item, nil
}

func (s *Store) UpsertTrack(ctx context.Context, track domain.StoryTrack) (*domain.StoryTrack, error) {
	now := time.Now().UTC()
	if track.CreatedAt.IsZero() {
		track.CreatedAt = now
	}
	if track.UpdatedAt.IsZero() {
		track.UpdatedAt = now
	}
	if err := s.queries.UpsertTrack(ctx, sqldb.UpsertTrackParams{
		ID:              track.ID,
		SourceID:        track.SourceID,
		TrackKey:        track.TrackKey,
		TrackName:       track.TrackName,
		CanonicalAuthor: track.CanonicalAuthor,
		SeriesMeta:      track.SeriesMeta,
		OutputPolicy:    track.OutputPolicy,
		CreatedAt:       formatTime(track.CreatedAt),
		UpdatedAt:       formatTime(track.UpdatedAt),
	}); err != nil {
		return nil, err
	}
	row, err := s.queries.GetTrackBySourceAndKey(ctx, sqldb.GetTrackBySourceAndKeyParams{
		SourceID: track.SourceID,
		TrackKey: track.TrackKey,
	})
	if err != nil {
		return nil, err
	}
	item := trackFromRow(row)
	return &item, nil
}

func (s *Store) GetTrackBySourceAndKey(ctx context.Context, sourceID, trackKey string) (*domain.StoryTrack, error) {
	row, err := s.queries.GetTrackBySourceAndKey(ctx, sqldb.GetTrackBySourceAndKeyParams{
		SourceID: sourceID,
		TrackKey: trackKey,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item := trackFromRow(row)
	return &item, nil
}

func (s *Store) GetTrack(ctx context.Context, id string) (*domain.StoryTrack, error) {
	row, err := s.queries.GetTrack(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item := trackFromRow(row)
	return &item, nil
}

func (s *Store) ListTracks(ctx context.Context, sourceID string) ([]domain.StoryTrack, error) {
	var (
		rows []sqldb.StoryTrack
		err  error
	)
	if sourceID == "" {
		rows, err = s.queries.ListTracks(ctx)
	} else {
		rows, err = s.queries.ListTracksBySource(ctx, sourceID)
	}
	if err != nil {
		return nil, err
	}
	items := make([]domain.StoryTrack, 0, len(rows))
	for _, row := range rows {
		items = append(items, trackFromRow(row))
	}
	return items, nil
}

func (s *Store) GetReleaseByProviderID(ctx context.Context, sourceID, providerReleaseID string) (*domain.Release, error) {
	row, err := s.queries.GetReleaseByProviderID(ctx, sqldb.GetReleaseByProviderIDParams{
		SourceID:          sourceID,
		ProviderReleaseID: providerReleaseID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item := releaseFromRow(row)
	return &item, nil
}

func (s *Store) GetReleaseBundle(ctx context.Context, id string) (*domain.ReleaseBundle, error) {
	releaseRow, err := s.queries.GetRelease(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	release := releaseFromRow(releaseRow)
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
		Track:     track,
	}
	if source != nil {
		result.Source = *source
	}
	if assignment != nil {
		result.Assignment = *assignment
	}
	return result, nil
}

func (s *Store) ListReleases(ctx context.Context, sourceID string) ([]domain.Release, error) {
	rows, err := s.queries.ListReleasesBySource(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Release, 0, len(rows))
	for _, row := range rows {
		items = append(items, releaseFromRow(row))
	}
	return items, nil
}

func (s *Store) GetCanonicalArtifactByReleaseID(ctx context.Context, releaseID string) (*domain.Artifact, error) {
	row, err := s.queries.GetCanonicalArtifactByReleaseID(ctx, releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item := artifactFromRow(row)
	return &item, nil
}

func (s *Store) GetArtifact(ctx context.Context, id string) (*domain.Artifact, error) {
	row, err := s.queries.GetArtifact(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item := artifactFromRow(row)
	return &item, nil
}

func (s *Store) ListArtifactsByReleaseID(ctx context.Context, releaseID string) ([]domain.Artifact, error) {
	rows, err := s.queries.ListArtifactsByReleaseID(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Artifact, 0, len(rows))
	for _, row := range rows {
		items = append(items, artifactFromRow(row))
	}
	return items, nil
}

func (s *Store) SaveSyncSnapshot(ctx context.Context, snapshot store.SyncSnapshot) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	qtx := s.queries.WithTx(tx)

	if err := qtx.UpsertSource(ctx, sqldb.UpsertSourceParams{
		ID:            snapshot.Source.ID,
		Provider:      snapshot.Source.Provider,
		SourceUrl:     snapshot.Source.SourceURL,
		SourceType:    snapshot.Source.SourceType,
		CreatorID:     snapshot.Source.CreatorID,
		CreatorName:   snapshot.Source.CreatorName,
		AuthProfileID: snapshot.Source.AuthProfileID,
		Enabled:       boolInt(snapshot.Source.Enabled),
		SyncCursor:    snapshot.Source.SyncCursor,
		LastSyncedAt:  formatTime(snapshot.Source.LastSyncedAt),
	}); err != nil {
		return err
	}
	now := time.Now().UTC()
	if snapshot.Track.CreatedAt.IsZero() {
		snapshot.Track.CreatedAt = now
	}
	if snapshot.Track.UpdatedAt.IsZero() {
		snapshot.Track.UpdatedAt = now
	}
	if err := qtx.UpsertTrack(ctx, sqldb.UpsertTrackParams{
		ID:              snapshot.Track.ID,
		SourceID:        snapshot.Track.SourceID,
		TrackKey:        snapshot.Track.TrackKey,
		TrackName:       snapshot.Track.TrackName,
		CanonicalAuthor: snapshot.Track.CanonicalAuthor,
		SeriesMeta:      snapshot.Track.SeriesMeta,
		OutputPolicy:    snapshot.Track.OutputPolicy,
		CreatedAt:       formatTime(snapshot.Track.CreatedAt),
		UpdatedAt:       formatTime(snapshot.Track.UpdatedAt),
	}); err != nil {
		return err
	}
	trackRow, err := qtx.GetTrackBySourceAndKey(ctx, sqldb.GetTrackBySourceAndKeyParams{
		SourceID: snapshot.Track.SourceID,
		TrackKey: snapshot.Track.TrackKey,
	})
	if err != nil {
		return err
	}
	track := trackFromRow(trackRow)
	snapshot.Assignment.TrackID = track.ID
	snapshot.Artifact.TrackID = track.ID
	if err := qtx.UpsertRelease(ctx, sqldb.UpsertReleaseParams{
		ID:                   snapshot.Release.ID,
		SourceID:             snapshot.Release.SourceID,
		ProviderReleaseID:    snapshot.Release.ProviderReleaseID,
		Url:                  snapshot.Release.URL,
		Title:                snapshot.Release.Title,
		PublishedAt:          formatTime(snapshot.Release.PublishedAt),
		EditedAt:             formatTime(snapshot.Release.EditedAt),
		PostType:             snapshot.Release.PostType,
		VisibilityState:      snapshot.Release.VisibilityState,
		NormalizedPayloadRef: snapshot.Release.NormalizedPayloadRef,
		RawPayloadRef:        snapshot.Release.RawPayloadRef,
		ContentHash:          snapshot.Release.ContentHash,
		DiscoveredAt:         formatTime(snapshot.Release.DiscoveredAt),
		Status:               snapshot.Release.Status,
	}); err != nil {
		return err
	}
	if err := qtx.UpsertReleaseAssignment(ctx, sqldb.UpsertReleaseAssignmentParams{
		ReleaseID:   snapshot.Assignment.ReleaseID,
		TrackID:     snapshot.Assignment.TrackID,
		RuleID:      snapshot.Assignment.RuleID,
		ReleaseRole: string(snapshot.Assignment.ReleaseRole),
		Confidence:  snapshot.Assignment.Confidence,
	}); err != nil {
		return err
	}
	if err := qtx.ClearCanonicalArtifactsForRelease(ctx, snapshot.Release.ID); err != nil {
		return err
	}
	if snapshot.Artifact.ID != "" {
		if err := qtx.UpsertArtifact(ctx, sqldb.UpsertArtifactParams{
			ID:            snapshot.Artifact.ID,
			ReleaseID:     snapshot.Artifact.ReleaseID,
			TrackID:       snapshot.Artifact.TrackID,
			ArtifactKind:  snapshot.Artifact.ArtifactKind,
			IsCanonical:   boolInt(snapshot.Artifact.IsCanonical),
			Filename:      snapshot.Artifact.Filename,
			MimeType:      snapshot.Artifact.MIMEType,
			Sha256:        snapshot.Artifact.SHA256,
			StorageRef:    snapshot.Artifact.StorageRef,
			BuiltAt:       formatTime(snapshot.Artifact.BuiltAt),
			State:         string(snapshot.Artifact.State),
			MetadataRef:   snapshot.Artifact.MetadataRef,
			NormalizedRef: snapshot.Artifact.NormalizedRef,
			RawRef:        snapshot.Artifact.RawRef,
		}); err != nil {
			return err
		}
	}
	if err := qtx.UpdateSourceLastSyncedAt(ctx, sqldb.UpdateSourceLastSyncedAtParams{
		LastSyncedAt: formatTime(now),
		ID:           snapshot.Source.ID,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) StartRun(ctx context.Context, run domain.RunRecord) error {
	return s.queries.InsertRunRecord(ctx, sqldb.InsertRunRecordParams{
		ID:          run.ID,
		Command:     run.Command,
		StartedAt:   formatTime(run.StartedAt),
		FinishedAt:  nullStringTime(run.FinishedAt),
		Status:      string(run.Status),
		Summary:     run.Summary,
		SourceScope: run.SourceScope,
		DryRun:      boolInt(run.DryRun),
	})
}

func (s *Store) FinishRun(ctx context.Context, runID string, status domain.RunStatus, summary string) error {
	finished := time.Now().UTC()
	return s.queries.UpdateRunRecord(ctx, sqldb.UpdateRunRecordParams{
		FinishedAt: nullStringTime(&finished),
		Status:     string(status),
		Summary:    summary,
		ID:         runID,
	})
}

func (s *Store) AddEvent(ctx context.Context, event domain.EventRecord) error {
	return nil
}

func (s *Store) GetRunBundle(ctx context.Context, runID string) (*domain.RunBundle, error) {
	runRow, err := s.queries.GetRunRecord(ctx, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &domain.RunBundle{
		Run: runFromRow(runRow),
	}, nil
}

func (s *Store) ListRuns(ctx context.Context, limit int) ([]domain.RunRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.queries.ListRunRecordsRecent(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	items := make([]domain.RunRecord, 0, len(rows))
	for _, row := range rows {
		items = append(items, runFromRow(row))
	}
	return items, nil
}

func (s *Store) ListPublishCandidates(ctx context.Context, sourceID string) ([]domain.PublishCandidate, error) {
	if sourceID == "" {
		rows, err := s.queries.ListPublishCandidates(ctx)
		if err != nil {
			return nil, err
		}
		items := make([]domain.PublishCandidate, 0, len(rows))
		for _, row := range rows {
			items = append(items, publishCandidateFromRow(row))
		}
		return items, nil
	}
	rows, err := s.queries.ListPublishCandidatesBySource(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	items := make([]domain.PublishCandidate, 0, len(rows))
	for _, row := range rows {
		items = append(items, publishCandidateFromRowBySource(row))
	}
	return items, nil
}

func (s *Store) HasSuccessfulPublish(ctx context.Context, artifactID, targetID, publishHash string) (bool, error) {
	count, err := s.queries.CountSuccessfulPublishRecords(ctx, sqldb.CountSuccessfulPublishRecordsParams{
		ArtifactID:  artifactID,
		TargetID:    targetID,
		PublishHash: publishHash,
		Status:      string(domain.PublishStatusPublished),
	})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) UpsertPublishRecord(ctx context.Context, record domain.PublishRecord) error {
	return s.queries.UpsertPublishRecord(ctx, sqldb.UpsertPublishRecordParams{
		ID:          record.ID,
		ArtifactID:  record.ArtifactID,
		TargetID:    record.TargetID,
		TargetKind:  record.TargetKind,
		TargetRef:   record.TargetRef,
		PublishHash: record.PublishHash,
		PublishedAt: formatTime(record.PublishedAt),
		Status:      string(record.Status),
		Message:     record.Message,
	})
}

func (s *Store) ListPublishRecords(ctx context.Context, sourceID, targetID string) ([]domain.PublishRecordBundle, error) {
	switch {
	case sourceID == "" && targetID == "":
		rows, err := s.queries.ListPublishRecords(ctx)
		if err != nil {
			return nil, err
		}
		items := make([]domain.PublishRecordBundle, 0, len(rows))
		for _, row := range rows {
			items = append(items, publishRecordBundleFromListRow(row))
		}
		return items, nil
	case sourceID != "" && targetID == "":
		rows, err := s.queries.ListPublishRecordsBySource(ctx, sourceID)
		if err != nil {
			return nil, err
		}
		items := make([]domain.PublishRecordBundle, 0, len(rows))
		for _, row := range rows {
			items = append(items, publishRecordBundleFromListBySourceRow(row))
		}
		return items, nil
	case sourceID == "" && targetID != "":
		rows, err := s.queries.ListPublishRecordsByTarget(ctx, targetID)
		if err != nil {
			return nil, err
		}
		items := make([]domain.PublishRecordBundle, 0, len(rows))
		for _, row := range rows {
			items = append(items, publishRecordBundleFromListByTargetRow(row))
		}
		return items, nil
	default:
		rows, err := s.queries.ListPublishRecordsBySourceAndTarget(ctx, sqldb.ListPublishRecordsBySourceAndTargetParams{
			SourceID: sourceID,
			TargetID: targetID,
		})
		if err != nil {
			return nil, err
		}
		items := make([]domain.PublishRecordBundle, 0, len(rows))
		for _, row := range rows {
			items = append(items, publishRecordBundleFromListBySourceAndTargetRow(row))
		}
		return items, nil
	}
}

func (s *Store) GetPublishRecord(ctx context.Context, id string) (*domain.PublishRecordBundle, error) {
	row, err := s.queries.GetPublishRecordBundle(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item := publishRecordBundleFromGetRow(row)
	return &item, nil
}

func (s *Store) AcquireLease(ctx context.Context, key, holder string, ttl time.Duration) (bool, error) {
	if key == "" || holder == "" {
		return false, fmt.Errorf("lease key and holder are required")
	}
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)
	rowsAffected, err := s.queries.AcquireLease(ctx, sqldb.AcquireLeaseParams{
		Key:       key,
		Holder:    holder,
		ExpiresAt: formatTime(expiresAt),
		UpdatedAt: formatTime(now),
	})
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

func (s *Store) ReleaseLease(ctx context.Context, key, holder string) error {
	if key == "" || holder == "" {
		return nil
	}
	return s.queries.ReleaseLease(ctx, sqldb.ReleaseLeaseParams{
		Key:    key,
		Holder: holder,
	})
}

func (s *Store) getAssignment(ctx context.Context, releaseID string) (*domain.ReleaseAssignment, error) {
	row, err := s.queries.GetReleaseAssignment(ctx, releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item := assignmentFromRow(row)
	return &item, nil
}

func sourceFromRow(row sqldb.Source) domain.Source {
	return domain.Source{
		ID:            row.ID,
		Provider:      row.Provider,
		SourceURL:     row.SourceUrl,
		SourceType:    row.SourceType,
		CreatorID:     row.CreatorID,
		CreatorName:   row.CreatorName,
		AuthProfileID: row.AuthProfileID,
		Enabled:       row.Enabled == 1,
		SyncCursor:    row.SyncCursor,
		LastSyncedAt:  parseTime(row.LastSyncedAt),
	}
}

func trackFromRow(row sqldb.StoryTrack) domain.StoryTrack {
	return domain.StoryTrack{
		ID:              row.ID,
		SourceID:        row.SourceID,
		TrackKey:        row.TrackKey,
		TrackName:       row.TrackName,
		CanonicalAuthor: row.CanonicalAuthor,
		SeriesMeta:      row.SeriesMeta,
		OutputPolicy:    row.OutputPolicy,
		CreatedAt:       parseTime(row.CreatedAt),
		UpdatedAt:       parseTime(row.UpdatedAt),
	}
}

func releaseFromRow(row sqldb.Release) domain.Release {
	return domain.Release{
		ID:                   row.ID,
		SourceID:             row.SourceID,
		ProviderReleaseID:    row.ProviderReleaseID,
		URL:                  row.Url,
		Title:                row.Title,
		PublishedAt:          parseTime(row.PublishedAt),
		EditedAt:             parseTime(row.EditedAt),
		PostType:             row.PostType,
		VisibilityState:      row.VisibilityState,
		NormalizedPayloadRef: row.NormalizedPayloadRef,
		RawPayloadRef:        row.RawPayloadRef,
		ContentHash:          row.ContentHash,
		DiscoveredAt:         parseTime(row.DiscoveredAt),
		Status:               row.Status,
	}
}

func artifactFromRow(row sqldb.Artifact) domain.Artifact {
	return domain.Artifact{
		ID:            row.ID,
		ReleaseID:     row.ReleaseID,
		TrackID:       row.TrackID,
		ArtifactKind:  row.ArtifactKind,
		IsCanonical:   row.IsCanonical == 1,
		Filename:      row.Filename,
		MIMEType:      row.MimeType,
		SHA256:        row.Sha256,
		StorageRef:    row.StorageRef,
		BuiltAt:       parseTime(row.BuiltAt),
		State:         domain.ArtifactState(row.State),
		MetadataRef:   row.MetadataRef,
		NormalizedRef: row.NormalizedRef,
		RawRef:        row.RawRef,
	}
}

func assignmentFromRow(row sqldb.ReleaseAssignment) domain.ReleaseAssignment {
	return domain.ReleaseAssignment{
		ReleaseID:   row.ReleaseID,
		TrackID:     row.TrackID,
		RuleID:      row.RuleID,
		ReleaseRole: domain.ReleaseRole(row.ReleaseRole),
		Confidence:  row.Confidence,
	}
}

func runFromRow(row sqldb.RunRecord) domain.RunRecord {
	item := domain.RunRecord{
		ID:          row.ID,
		Command:     row.Command,
		StartedAt:   parseTime(row.StartedAt),
		Status:      domain.RunStatus(row.Status),
		Summary:     row.Summary,
		SourceScope: row.SourceScope,
		DryRun:      row.DryRun == 1,
	}
	if row.FinishedAt.Valid {
		finished := parseTime(row.FinishedAt.String)
		item.FinishedAt = &finished
	}
	return item
}

func publishCandidateFromRow(row sqldb.ListPublishCandidatesRow) domain.PublishCandidate {
	return domain.PublishCandidate{
		Source: sourceFromRow(sqldb.Source{
			ID:            row.ID,
			Provider:      row.Provider,
			SourceUrl:     row.SourceUrl,
			SourceType:    row.SourceType,
			CreatorID:     row.CreatorID,
			CreatorName:   row.CreatorName,
			AuthProfileID: row.AuthProfileID,
			Enabled:       row.Enabled,
			SyncCursor:    row.SyncCursor,
			LastSyncedAt:  row.LastSyncedAt,
		}),
		Track: trackFromNullableRow(row.ID_2, row.SourceID, row.TrackKey, row.TrackName, row.CanonicalAuthor, row.SeriesMeta, row.OutputPolicy, row.CreatedAt, row.UpdatedAt),
		Release: releaseFromRow(sqldb.Release{
			ID:                   row.ID_3,
			SourceID:             row.SourceID_2,
			ProviderReleaseID:    row.ProviderReleaseID,
			Url:                  row.Url,
			Title:                row.Title,
			PublishedAt:          row.PublishedAt,
			EditedAt:             row.EditedAt,
			PostType:             row.PostType,
			VisibilityState:      row.VisibilityState,
			NormalizedPayloadRef: row.NormalizedPayloadRef,
			RawPayloadRef:        row.RawPayloadRef,
			ContentHash:          row.ContentHash,
			DiscoveredAt:         row.DiscoveredAt,
			Status:               row.Status,
		}),
		Assignment: assignmentFromNullableRow(row.ReleaseID, row.TrackID, row.RuleID, row.ReleaseRole, row.Confidence),
		Artifact: artifactFromRow(sqldb.Artifact{
			ID:            row.ID_4,
			ReleaseID:     row.ReleaseID_2,
			TrackID:       row.TrackID_2,
			ArtifactKind:  row.ArtifactKind,
			IsCanonical:   row.IsCanonical,
			Filename:      row.Filename,
			MimeType:      row.MimeType,
			Sha256:        row.Sha256,
			StorageRef:    row.StorageRef,
			BuiltAt:       row.BuiltAt,
			State:         row.State,
			MetadataRef:   row.MetadataRef,
			NormalizedRef: row.NormalizedRef,
			RawRef:        row.RawRef,
		}),
	}
}

func publishCandidateFromRowBySource(row sqldb.ListPublishCandidatesBySourceRow) domain.PublishCandidate {
	return domain.PublishCandidate{
		Source: sourceFromRow(sqldb.Source{
			ID:            row.ID,
			Provider:      row.Provider,
			SourceUrl:     row.SourceUrl,
			SourceType:    row.SourceType,
			CreatorID:     row.CreatorID,
			CreatorName:   row.CreatorName,
			AuthProfileID: row.AuthProfileID,
			Enabled:       row.Enabled,
			SyncCursor:    row.SyncCursor,
			LastSyncedAt:  row.LastSyncedAt,
		}),
		Track: trackFromNullableRow(row.ID_2, row.SourceID, row.TrackKey, row.TrackName, row.CanonicalAuthor, row.SeriesMeta, row.OutputPolicy, row.CreatedAt, row.UpdatedAt),
		Release: releaseFromRow(sqldb.Release{
			ID:                   row.ID_3,
			SourceID:             row.SourceID_2,
			ProviderReleaseID:    row.ProviderReleaseID,
			Url:                  row.Url,
			Title:                row.Title,
			PublishedAt:          row.PublishedAt,
			EditedAt:             row.EditedAt,
			PostType:             row.PostType,
			VisibilityState:      row.VisibilityState,
			NormalizedPayloadRef: row.NormalizedPayloadRef,
			RawPayloadRef:        row.RawPayloadRef,
			ContentHash:          row.ContentHash,
			DiscoveredAt:         row.DiscoveredAt,
			Status:               row.Status,
		}),
		Assignment: assignmentFromNullableRow(row.ReleaseID, row.TrackID, row.RuleID, row.ReleaseRole, row.Confidence),
		Artifact: artifactFromRow(sqldb.Artifact{
			ID:            row.ID_4,
			ReleaseID:     row.ReleaseID_2,
			TrackID:       row.TrackID_2,
			ArtifactKind:  row.ArtifactKind,
			IsCanonical:   row.IsCanonical,
			Filename:      row.Filename,
			MimeType:      row.MimeType,
			Sha256:        row.Sha256,
			StorageRef:    row.StorageRef,
			BuiltAt:       row.BuiltAt,
			State:         row.State,
			MetadataRef:   row.MetadataRef,
			NormalizedRef: row.NormalizedRef,
			RawRef:        row.RawRef,
		}),
	}
}

func publishRecordBundleFromGetRow(row sqldb.GetPublishRecordBundleRow) domain.PublishRecordBundle {
	return buildPublishRecordBundle(publishRecordBundleFields{
		recordID:             row.ID,
		artifactID:           row.ArtifactID,
		targetID:             row.TargetID,
		targetKind:           row.TargetKind,
		targetRef:            row.TargetRef,
		publishHash:          row.PublishHash,
		publishedAt:          row.PublishedAt,
		publishStatus:        row.Status,
		publishMessage:       row.Message,
		artID:                row.ID_2,
		artReleaseID:         row.ReleaseID,
		artTrackID:           row.TrackID,
		artKind:              row.ArtifactKind,
		artCanonical:         row.IsCanonical,
		artFilename:          row.Filename,
		artMIME:              row.MimeType,
		artSHA:               row.Sha256,
		artStorage:           row.StorageRef,
		artBuiltAt:           row.BuiltAt,
		artState:             row.State,
		artMetadataRef:       row.MetadataRef,
		artNormalizedRef:     row.NormalizedRef,
		artRawRef:            row.RawRef,
		releaseID:            row.ID_3,
		releaseSourceID:      row.SourceID,
		providerReleaseID:    row.ProviderReleaseID,
		releaseURL:           row.Url,
		releaseTitle:         row.Title,
		releasePublishedAt:   row.PublishedAt_2,
		releaseEditedAt:      row.EditedAt,
		releasePostType:      row.PostType,
		releaseVisibility:    row.VisibilityState,
		releaseNormalizedRef: row.NormalizedPayloadRef,
		releaseRawRef:        row.RawPayloadRef,
		releaseHash:          row.ContentHash,
		releaseDiscoveredAt:  row.DiscoveredAt,
		releaseStatus:        row.Status_2,
		sourceID:             row.ID_4,
		sourceProvider:       row.Provider,
		sourceURL:            row.SourceUrl,
		sourceType:           row.SourceType,
		creatorID:            row.CreatorID,
		creatorName:          row.CreatorName,
		authProfileID:        row.AuthProfileID,
		sourceEnabled:        row.Enabled,
		sourceSyncCursor:     row.SyncCursor,
		sourceLastSyncedAt:   row.LastSyncedAt,
		trackID:              row.ID_5,
		trackSourceID:        row.SourceID_2,
		trackKey:             row.TrackKey,
		trackName:            row.TrackName,
		canonicalAuthor:      row.CanonicalAuthor,
		seriesMeta:           row.SeriesMeta,
		outputPolicy:         row.OutputPolicy,
		createdAt:            row.CreatedAt,
		updatedAt:            row.UpdatedAt,
	})
}

func publishRecordBundleFromListRow(row sqldb.ListPublishRecordsRow) domain.PublishRecordBundle {
	return buildPublishRecordBundle(publishRecordBundleFields{
		recordID:             row.ID,
		artifactID:           row.ArtifactID,
		targetID:             row.TargetID,
		targetKind:           row.TargetKind,
		targetRef:            row.TargetRef,
		publishHash:          row.PublishHash,
		publishedAt:          row.PublishedAt,
		publishStatus:        row.Status,
		publishMessage:       row.Message,
		artID:                row.ID_2,
		artReleaseID:         row.ReleaseID,
		artTrackID:           row.TrackID,
		artKind:              row.ArtifactKind,
		artCanonical:         row.IsCanonical,
		artFilename:          row.Filename,
		artMIME:              row.MimeType,
		artSHA:               row.Sha256,
		artStorage:           row.StorageRef,
		artBuiltAt:           row.BuiltAt,
		artState:             row.State,
		artMetadataRef:       row.MetadataRef,
		artNormalizedRef:     row.NormalizedRef,
		artRawRef:            row.RawRef,
		releaseID:            row.ID_3,
		releaseSourceID:      row.SourceID,
		providerReleaseID:    row.ProviderReleaseID,
		releaseURL:           row.Url,
		releaseTitle:         row.Title,
		releasePublishedAt:   row.PublishedAt_2,
		releaseEditedAt:      row.EditedAt,
		releasePostType:      row.PostType,
		releaseVisibility:    row.VisibilityState,
		releaseNormalizedRef: row.NormalizedPayloadRef,
		releaseRawRef:        row.RawPayloadRef,
		releaseHash:          row.ContentHash,
		releaseDiscoveredAt:  row.DiscoveredAt,
		releaseStatus:        row.Status_2,
		sourceID:             row.ID_4,
		sourceProvider:       row.Provider,
		sourceURL:            row.SourceUrl,
		sourceType:           row.SourceType,
		creatorID:            row.CreatorID,
		creatorName:          row.CreatorName,
		authProfileID:        row.AuthProfileID,
		sourceEnabled:        row.Enabled,
		sourceSyncCursor:     row.SyncCursor,
		sourceLastSyncedAt:   row.LastSyncedAt,
		trackID:              row.ID_5,
		trackSourceID:        row.SourceID_2,
		trackKey:             row.TrackKey,
		trackName:            row.TrackName,
		canonicalAuthor:      row.CanonicalAuthor,
		seriesMeta:           row.SeriesMeta,
		outputPolicy:         row.OutputPolicy,
		createdAt:            row.CreatedAt,
		updatedAt:            row.UpdatedAt,
	})
}

func publishRecordBundleFromListBySourceRow(row sqldb.ListPublishRecordsBySourceRow) domain.PublishRecordBundle {
	return buildPublishRecordBundle(publishRecordBundleFields{
		recordID:             row.ID,
		artifactID:           row.ArtifactID,
		targetID:             row.TargetID,
		targetKind:           row.TargetKind,
		targetRef:            row.TargetRef,
		publishHash:          row.PublishHash,
		publishedAt:          row.PublishedAt,
		publishStatus:        row.Status,
		publishMessage:       row.Message,
		artID:                row.ID_2,
		artReleaseID:         row.ReleaseID,
		artTrackID:           row.TrackID,
		artKind:              row.ArtifactKind,
		artCanonical:         row.IsCanonical,
		artFilename:          row.Filename,
		artMIME:              row.MimeType,
		artSHA:               row.Sha256,
		artStorage:           row.StorageRef,
		artBuiltAt:           row.BuiltAt,
		artState:             row.State,
		artMetadataRef:       row.MetadataRef,
		artNormalizedRef:     row.NormalizedRef,
		artRawRef:            row.RawRef,
		releaseID:            row.ID_3,
		releaseSourceID:      row.SourceID,
		providerReleaseID:    row.ProviderReleaseID,
		releaseURL:           row.Url,
		releaseTitle:         row.Title,
		releasePublishedAt:   row.PublishedAt_2,
		releaseEditedAt:      row.EditedAt,
		releasePostType:      row.PostType,
		releaseVisibility:    row.VisibilityState,
		releaseNormalizedRef: row.NormalizedPayloadRef,
		releaseRawRef:        row.RawPayloadRef,
		releaseHash:          row.ContentHash,
		releaseDiscoveredAt:  row.DiscoveredAt,
		releaseStatus:        row.Status_2,
		sourceID:             row.ID_4,
		sourceProvider:       row.Provider,
		sourceURL:            row.SourceUrl,
		sourceType:           row.SourceType,
		creatorID:            row.CreatorID,
		creatorName:          row.CreatorName,
		authProfileID:        row.AuthProfileID,
		sourceEnabled:        row.Enabled,
		sourceSyncCursor:     row.SyncCursor,
		sourceLastSyncedAt:   row.LastSyncedAt,
		trackID:              row.ID_5,
		trackSourceID:        row.SourceID_2,
		trackKey:             row.TrackKey,
		trackName:            row.TrackName,
		canonicalAuthor:      row.CanonicalAuthor,
		seriesMeta:           row.SeriesMeta,
		outputPolicy:         row.OutputPolicy,
		createdAt:            row.CreatedAt,
		updatedAt:            row.UpdatedAt,
	})
}

func publishRecordBundleFromListBySourceAndTargetRow(row sqldb.ListPublishRecordsBySourceAndTargetRow) domain.PublishRecordBundle {
	return buildPublishRecordBundle(publishRecordBundleFields{
		recordID:             row.ID,
		artifactID:           row.ArtifactID,
		targetID:             row.TargetID,
		targetKind:           row.TargetKind,
		targetRef:            row.TargetRef,
		publishHash:          row.PublishHash,
		publishedAt:          row.PublishedAt,
		publishStatus:        row.Status,
		publishMessage:       row.Message,
		artID:                row.ID_2,
		artReleaseID:         row.ReleaseID,
		artTrackID:           row.TrackID,
		artKind:              row.ArtifactKind,
		artCanonical:         row.IsCanonical,
		artFilename:          row.Filename,
		artMIME:              row.MimeType,
		artSHA:               row.Sha256,
		artStorage:           row.StorageRef,
		artBuiltAt:           row.BuiltAt,
		artState:             row.State,
		artMetadataRef:       row.MetadataRef,
		artNormalizedRef:     row.NormalizedRef,
		artRawRef:            row.RawRef,
		releaseID:            row.ID_3,
		releaseSourceID:      row.SourceID,
		providerReleaseID:    row.ProviderReleaseID,
		releaseURL:           row.Url,
		releaseTitle:         row.Title,
		releasePublishedAt:   row.PublishedAt_2,
		releaseEditedAt:      row.EditedAt,
		releasePostType:      row.PostType,
		releaseVisibility:    row.VisibilityState,
		releaseNormalizedRef: row.NormalizedPayloadRef,
		releaseRawRef:        row.RawPayloadRef,
		releaseHash:          row.ContentHash,
		releaseDiscoveredAt:  row.DiscoveredAt,
		releaseStatus:        row.Status_2,
		sourceID:             row.ID_4,
		sourceProvider:       row.Provider,
		sourceURL:            row.SourceUrl,
		sourceType:           row.SourceType,
		creatorID:            row.CreatorID,
		creatorName:          row.CreatorName,
		authProfileID:        row.AuthProfileID,
		sourceEnabled:        row.Enabled,
		sourceSyncCursor:     row.SyncCursor,
		sourceLastSyncedAt:   row.LastSyncedAt,
		trackID:              row.ID_5,
		trackSourceID:        row.SourceID_2,
		trackKey:             row.TrackKey,
		trackName:            row.TrackName,
		canonicalAuthor:      row.CanonicalAuthor,
		seriesMeta:           row.SeriesMeta,
		outputPolicy:         row.OutputPolicy,
		createdAt:            row.CreatedAt,
		updatedAt:            row.UpdatedAt,
	})
}

func publishRecordBundleFromListByTargetRow(row sqldb.ListPublishRecordsByTargetRow) domain.PublishRecordBundle {
	return buildPublishRecordBundle(publishRecordBundleFields{
		recordID:             row.ID,
		artifactID:           row.ArtifactID,
		targetID:             row.TargetID,
		targetKind:           row.TargetKind,
		targetRef:            row.TargetRef,
		publishHash:          row.PublishHash,
		publishedAt:          row.PublishedAt,
		publishStatus:        row.Status,
		publishMessage:       row.Message,
		artID:                row.ID_2,
		artReleaseID:         row.ReleaseID,
		artTrackID:           row.TrackID,
		artKind:              row.ArtifactKind,
		artCanonical:         row.IsCanonical,
		artFilename:          row.Filename,
		artMIME:              row.MimeType,
		artSHA:               row.Sha256,
		artStorage:           row.StorageRef,
		artBuiltAt:           row.BuiltAt,
		artState:             row.State,
		artMetadataRef:       row.MetadataRef,
		artNormalizedRef:     row.NormalizedRef,
		artRawRef:            row.RawRef,
		releaseID:            row.ID_3,
		releaseSourceID:      row.SourceID,
		providerReleaseID:    row.ProviderReleaseID,
		releaseURL:           row.Url,
		releaseTitle:         row.Title,
		releasePublishedAt:   row.PublishedAt_2,
		releaseEditedAt:      row.EditedAt,
		releasePostType:      row.PostType,
		releaseVisibility:    row.VisibilityState,
		releaseNormalizedRef: row.NormalizedPayloadRef,
		releaseRawRef:        row.RawPayloadRef,
		releaseHash:          row.ContentHash,
		releaseDiscoveredAt:  row.DiscoveredAt,
		releaseStatus:        row.Status_2,
		sourceID:             row.ID_4,
		sourceProvider:       row.Provider,
		sourceURL:            row.SourceUrl,
		sourceType:           row.SourceType,
		creatorID:            row.CreatorID,
		creatorName:          row.CreatorName,
		authProfileID:        row.AuthProfileID,
		sourceEnabled:        row.Enabled,
		sourceSyncCursor:     row.SyncCursor,
		sourceLastSyncedAt:   row.LastSyncedAt,
		trackID:              row.ID_5,
		trackSourceID:        row.SourceID_2,
		trackKey:             row.TrackKey,
		trackName:            row.TrackName,
		canonicalAuthor:      row.CanonicalAuthor,
		seriesMeta:           row.SeriesMeta,
		outputPolicy:         row.OutputPolicy,
		createdAt:            row.CreatedAt,
		updatedAt:            row.UpdatedAt,
	})
}

type publishRecordBundleFields struct {
	recordID             string
	artifactID           string
	targetID             string
	targetKind           string
	targetRef            string
	publishHash          string
	publishedAt          string
	publishStatus        string
	publishMessage       string
	artID                string
	artReleaseID         string
	artTrackID           string
	artKind              string
	artCanonical         int64
	artFilename          string
	artMIME              string
	artSHA               string
	artStorage           string
	artBuiltAt           string
	artState             string
	artMetadataRef       string
	artNormalizedRef     string
	artRawRef            string
	releaseID            string
	releaseSourceID      string
	providerReleaseID    string
	releaseURL           string
	releaseTitle         string
	releasePublishedAt   string
	releaseEditedAt      string
	releasePostType      string
	releaseVisibility    string
	releaseNormalizedRef string
	releaseRawRef        string
	releaseHash          string
	releaseDiscoveredAt  string
	releaseStatus        string
	sourceID             string
	sourceProvider       string
	sourceURL            string
	sourceType           string
	creatorID            string
	creatorName          string
	authProfileID        string
	sourceEnabled        int64
	sourceSyncCursor     string
	sourceLastSyncedAt   string
	trackID              sql.NullString
	trackSourceID        sql.NullString
	trackKey             sql.NullString
	trackName            sql.NullString
	canonicalAuthor      sql.NullString
	seriesMeta           sql.NullString
	outputPolicy         sql.NullString
	createdAt            sql.NullString
	updatedAt            sql.NullString
}

func buildPublishRecordBundle(fields publishRecordBundleFields) domain.PublishRecordBundle {
	return domain.PublishRecordBundle{
		Record: domain.PublishRecord{
			ID:          fields.recordID,
			ArtifactID:  fields.artifactID,
			TargetID:    fields.targetID,
			TargetKind:  fields.targetKind,
			TargetRef:   fields.targetRef,
			PublishHash: fields.publishHash,
			PublishedAt: parseTime(fields.publishedAt),
			Status:      domain.PublishStatus(fields.publishStatus),
			Message:     fields.publishMessage,
		},
		Artifact: domain.Artifact{
			ID:            fields.artID,
			ReleaseID:     fields.artReleaseID,
			TrackID:       fields.artTrackID,
			ArtifactKind:  fields.artKind,
			IsCanonical:   fields.artCanonical == 1,
			Filename:      fields.artFilename,
			MIMEType:      fields.artMIME,
			SHA256:        fields.artSHA,
			StorageRef:    fields.artStorage,
			BuiltAt:       parseTime(fields.artBuiltAt),
			State:         domain.ArtifactState(fields.artState),
			MetadataRef:   fields.artMetadataRef,
			NormalizedRef: fields.artNormalizedRef,
			RawRef:        fields.artRawRef,
		},
		Release: domain.Release{
			ID:                   fields.releaseID,
			SourceID:             fields.releaseSourceID,
			ProviderReleaseID:    fields.providerReleaseID,
			URL:                  fields.releaseURL,
			Title:                fields.releaseTitle,
			PublishedAt:          parseTime(fields.releasePublishedAt),
			EditedAt:             parseTime(fields.releaseEditedAt),
			PostType:             fields.releasePostType,
			VisibilityState:      fields.releaseVisibility,
			NormalizedPayloadRef: fields.releaseNormalizedRef,
			RawPayloadRef:        fields.releaseRawRef,
			ContentHash:          fields.releaseHash,
			DiscoveredAt:         parseTime(fields.releaseDiscoveredAt),
			Status:               fields.releaseStatus,
		},
		Source: domain.Source{
			ID:            fields.sourceID,
			Provider:      fields.sourceProvider,
			SourceURL:     fields.sourceURL,
			SourceType:    fields.sourceType,
			CreatorID:     fields.creatorID,
			CreatorName:   fields.creatorName,
			AuthProfileID: fields.authProfileID,
			Enabled:       fields.sourceEnabled == 1,
			SyncCursor:    fields.sourceSyncCursor,
			LastSyncedAt:  parseTime(fields.sourceLastSyncedAt),
		},
		Track: trackFromNullableRow(
			fields.trackID,
			fields.trackSourceID,
			fields.trackKey,
			fields.trackName,
			fields.canonicalAuthor,
			fields.seriesMeta,
			fields.outputPolicy,
			fields.createdAt,
			fields.updatedAt,
		),
	}
}

func trackFromNullableRow(id, sourceID, trackKey, trackName, canonicalAuthor, seriesMeta, outputPolicy, createdAt, updatedAt sql.NullString) domain.StoryTrack {
	if !id.Valid {
		return domain.StoryTrack{}
	}
	return domain.StoryTrack{
		ID:              id.String,
		SourceID:        nullString(sourceID),
		TrackKey:        nullString(trackKey),
		TrackName:       nullString(trackName),
		CanonicalAuthor: nullString(canonicalAuthor),
		SeriesMeta:      nullString(seriesMeta),
		OutputPolicy:    nullString(outputPolicy),
		CreatedAt:       parseTime(nullString(createdAt)),
		UpdatedAt:       parseTime(nullString(updatedAt)),
	}
}

func assignmentFromNullableRow(releaseID, trackID, ruleID, releaseRole sql.NullString, confidence sql.NullFloat64) domain.ReleaseAssignment {
	return domain.ReleaseAssignment{
		ReleaseID:   nullString(releaseID),
		TrackID:     nullString(trackID),
		RuleID:      nullString(ruleID),
		ReleaseRole: domain.ReleaseRole(nullString(releaseRole)),
		Confidence:  nullFloat(confidence),
	}
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

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func nullStringTime(value *time.Time) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*value), Valid: true}
}

func nullString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func nullFloat(value sql.NullFloat64) float64 {
	if value.Valid {
		return value.Float64
	}
	return 0
}

var _ store.Repository = (*Store)(nil)
