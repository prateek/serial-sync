package sqlite

import (
	"context"

	"github.com/prateek/serial-sync/internal/domain"
	sqldb "github.com/prateek/serial-sync/internal/store/sqlite/db"
)

// WithDiagnosticSnapshot holds a catalog snapshot; apply also excludes writers.
// The callback must use the supplied snapshot without calling repository methods.
func (s *Store) WithDiagnosticSnapshot(ctx context.Context, apply bool, fn func([]domain.RunRecord, []domain.PendingPublish) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	begin := "BEGIN"
	if apply {
		begin = "BEGIN IMMEDIATE"
	}
	if _, err = conn.ExecContext(ctx, begin); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	q := sqldb.New(conn)
	rows, err := q.ListRunRecordsRecent(ctx, 1<<62)
	if err != nil {
		return err
	}
	runs := make([]domain.RunRecord, 0, len(rows))
	for _, row := range rows {
		run := runFromRow(row)

		runs = append(runs, run)
	}
	pending, err := q.ListPendingPublishes(ctx)
	if err != nil {
		return err
	}
	plans := make([]domain.PendingPublish, 0, len(pending))
	for _, row := range pending {
		plans = append(plans, domain.PendingPublish{ID: row.ID, TargetID: row.TargetID, PayloadRef: row.PayloadRef})
	}
	if err = fn(runs, plans); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}
