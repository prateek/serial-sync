package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/prateek/serial-sync/internal/domain"
	sqldb "github.com/prateek/serial-sync/internal/store/sqlite/db"
)

func (s *Store) GetPendingPublish(ctx context.Context, targetID string) (*domain.PendingPublish, error) {
	if !s.hasVolumeSchema {
		return nil, nil
	}
	row, err := s.queries.GetPendingPublish(ctx, targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &domain.PendingPublish{ID: row.ID, TargetID: row.TargetID, PayloadRef: row.PayloadRef}, nil
}

func (s *Store) SavePendingPublish(ctx context.Context, pending domain.PendingPublish) error {
	return s.queries.SavePendingPublish(ctx, sqldb.SavePendingPublishParams{ID: pending.ID, TargetID: pending.TargetID, PayloadRef: pending.PayloadRef})
}

func (s *Store) CompletePendingPublish(ctx context.Context, id string) error {
	return s.queries.CompletePendingPublish(ctx, id)
}
