package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/prateek/serial-sync/internal/domain"
	sqldb "github.com/prateek/serial-sync/internal/store/sqlite/db"
)

func (s *Store) GetReleaseEnrichment(ctx context.Context, source, id, fingerprint string) (*domain.ReleaseEnrichment, error) {
	if !s.hasEnrichmentSchema {
		return nil, nil
	}
	raw, err := s.queries.GetReleaseEnrichment(ctx, sqldb.GetReleaseEnrichmentParams{SourceID: source, ProviderReleaseID: id, CaptureFingerprint: fingerprint})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var metadata domain.ReleaseEnrichment
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func (s *Store) SaveReleaseEnrichment(ctx context.Context, source, id string, metadata domain.ReleaseEnrichment) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	queries := s.queries.WithTx(tx)
	if err := queries.UpsertReleaseEnrichment(ctx, sqldb.UpsertReleaseEnrichmentParams{SourceID: source, ProviderReleaseID: id, Metadata: string(raw), CaptureFingerprint: metadata.CaptureFingerprint}); err != nil {
		return err
	}
	for _, label := range metadata.Collections {
		names := label.Names
		if len(names) == 0 {
			names = []string{""}
		}
		for _, name := range names {
			if err := queries.SaveLabelObservation(ctx, sqldb.SaveLabelObservationParams{Provider: label.Provider, Campaign: label.Campaign, ResourceType: label.Type, ResourceID: label.ID, Name: name}); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ListLabelObservations(ctx context.Context) ([]domain.LabelReference, error) {
	if !s.hasEnrichmentSchema {
		return nil, nil
	}
	rows, err := s.queries.ListLabelObservations(ctx)
	if err != nil {
		return nil, err
	}
	var labels []domain.LabelReference
	for _, row := range rows {
		label := domain.LabelReference{Provider: row.Provider, Campaign: row.Campaign, Type: row.ResourceType, ID: row.ResourceID}
		if len(labels) == 0 || labels[len(labels)-1].Key() != label.Key() {
			labels = append(labels, label)
		}
		if row.Name != "" {
			labels[len(labels)-1].Names = append(labels[len(labels)-1].Names, row.Name)
		}
	}
	return labels, nil
}
