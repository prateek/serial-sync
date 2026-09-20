package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/prateek/serial-sync/internal/domain"
	sqldb "github.com/prateek/serial-sync/internal/store/sqlite/db"
)

func (s *Store) ListVolumeEditions(ctx context.Context) ([]domain.VolumeEdition, error) {
	if !s.hasVolumeSchema {
		return nil, nil
	}
	rows, err := s.queries.ListVolumeEditions(ctx)
	if err != nil {
		return nil, err
	}
	volumes := make([]domain.VolumeEdition, 0, len(rows))
	for _, row := range rows {
		artifact, err := s.GetArtifact(ctx, row.ArtifactID)
		if err != nil {
			return nil, err
		}
		if artifact == nil {
			return nil, fmt.Errorf("volume %s has no artifact", row.ID)
		}
		volume := domain.VolumeEdition{ID: row.ID, SeriesID: row.SeriesID, SourceID: row.SourceID, TrackID: row.TrackID, GroupID: row.GroupID, First: int(row.FirstChapter), Last: int(row.LastChapter), RecipeHash: row.RecipeHash, Active: row.Active != 0, Artifact: *artifact}
		members, err := s.queries.ListVolumeMembers(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		for _, member := range members {
			volume.Members = append(volume.Members, domain.VolumeMember{ReleaseID: member.ReleaseID, ContentHash: member.ContentHash, Position: int(member.Position)})
		}
		volumes = append(volumes, volume)
	}
	return volumes, nil
}

func (s *Store) ReplaceVolumes(ctx context.Context, seriesID string, volumes []domain.VolumeEdition, deactivatedGroups []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := s.queries.WithTx(tx)
	for _, groupID := range deactivatedGroups {
		if err := q.DeactivateVolumeGroup(ctx, sqldb.DeactivateVolumeGroupParams{SeriesID: seriesID, GroupID: groupID}); err != nil {
			return err
		}
	}
	for _, volume := range volumes {
		art := volume.Artifact
		if err := q.UpsertArtifact(ctx, sqldb.UpsertArtifactParams{ID: art.ID, ReleaseID: "", TrackID: art.TrackID, ArtifactKind: art.ArtifactKind, IsCanonical: 1, Filename: art.Filename, MimeType: art.MIMEType, Sha256: art.SHA256, StorageRef: art.StorageRef, BuiltAt: formatTime(art.BuiltAt), State: string(art.State), MetadataRef: art.MetadataRef, NormalizedRef: "", RawRef: ""}); err != nil {
			return err
		}
		if err := q.DeactivateVolumeGroup(ctx, sqldb.DeactivateVolumeGroupParams{SeriesID: volume.SeriesID, GroupID: volume.GroupID}); err != nil {
			return err
		}
		if err := q.InsertVolumeEdition(ctx, sqldb.InsertVolumeEditionParams{ID: volume.ID, SeriesID: volume.SeriesID, SourceID: volume.SourceID, TrackID: volume.TrackID, GroupID: volume.GroupID, FirstChapter: int64(volume.First), LastChapter: int64(volume.Last), RecipeHash: volume.RecipeHash, ArtifactID: art.ID}); err != nil {
			return err
		}
		for _, member := range volume.Members {
			if err := q.InsertVolumeMember(ctx, sqldb.InsertVolumeMemberParams{EditionID: volume.ID, ReleaseID: member.ReleaseID, ContentHash: member.ContentHash, Position: int64(member.Position)}); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ListPublishRecords(ctx context.Context, sourceID, targetID string) ([]domain.PublishRecordBundle, error) {
	items, err := s.listReleasePublishRecords(ctx, sourceID, targetID)
	if err != nil {
		return nil, err
	}
	volumes, err := s.ListVolumeEditions(ctx)
	if err != nil {
		return nil, err
	}
	if len(volumes) == 0 {
		return s.withPublishFilenames(ctx, items)
	}
	byArtifact := map[string]domain.VolumeEdition{}
	for _, volume := range volumes {
		byArtifact[volume.Artifact.ID] = volume
	}
	rows, err := s.queries.ListVolumePublishRecords(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		volume := byArtifact[row.ArtifactID]
		if sourceID != "" && volume.SourceID != sourceID || targetID != "" && row.TargetID != targetID {
			continue
		}
		source, err := s.GetSource(ctx, volume.SourceID)
		if err != nil {
			return nil, err
		}
		track, err := s.GetTrack(ctx, volume.TrackID)
		if err != nil {
			return nil, err
		}
		if source == nil || track == nil {
			return nil, fmt.Errorf("volume %s has missing source or series", volume.ID)
		}
		items = append(items, domain.PublishRecordBundle{Record: domain.PublishRecord{ID: row.ID, ArtifactID: row.ArtifactID, TargetID: row.TargetID, TargetKind: row.TargetKind, TargetRef: row.TargetRef, PublishHash: row.PublishHash, PublishedAt: parseTime(row.PublishedAt), Status: domain.PublishStatus(row.Status), Message: row.Message}, Artifact: volume.Artifact, Source: *source, Track: *track})
	}
	return s.withPublishFilenames(ctx, items)
}

func (s *Store) withPublishFilenames(ctx context.Context, records []domain.PublishRecordBundle) ([]domain.PublishRecordBundle, error) {
	for i := range records {
		record := &records[i]
		record.Record.Filename = record.Artifact.Filename
		if !s.hasVolumeSchema {
			continue
		}
		filename, err := s.queries.GetPublishFilename(ctx, sqldb.GetPublishFilenameParams{ArtifactID: record.Record.ArtifactID, TargetID: record.Record.TargetID, PublishHash: record.Record.PublishHash})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		record.Record.Filename = filename
	}
	return records, nil
}
