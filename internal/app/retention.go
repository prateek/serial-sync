package app

import (
	"context"
	"fmt"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
)

// CleanupDiagnostics previews or expires diagnostics under the configured policy.
func (s *Service) CleanupDiagnostics(ctx context.Context, apply bool) (observe.CleanupResult, error) {
	days := s.Config.Runtime.DiagnosticRetentionDays
	if days <= 0 {
		return observe.CleanupResult{}, fmt.Errorf("set runtime.diagnostic_retention_days to enable diagnostic expiry")
	}
	before := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	var result observe.CleanupResult
	err := s.Repo.WithDiagnosticSnapshot(ctx, apply, func(runs []domain.RunRecord, pending []domain.PendingPublish) error {
		var err error
		result, err = s.cleanupDiagnosticSnapshot(before, runs, pending, apply)
		return err
	})
	return result, err
}

func (s *Service) cleanupDiagnosticSnapshot(before time.Time, runs []domain.RunRecord, pending []domain.PendingPublish, apply bool) (observe.CleanupResult, error) {
	for _, run := range runs {
		if run.Status == domain.RunStatusRunning {
			return observe.CleanupResult{}, fmt.Errorf("cleanup requires no running runs; inspect %s and stop writers before retrying; see docs/observability.md for abandoned-run reconciliation", run.ID)
		}
	}
	files := s.deliveryFiles()
	plans, err := files.expired(before, pending)
	if err != nil {
		return observe.CleanupResult{}, err
	}
	result, err := observe.Cleanup(s.Config.Runtime.LogRoot, before, runs, apply)
	result.Paths = append(result.Paths, plans...)
	if err != nil || !apply {
		return result, err
	}
	return result, files.removeExpired(plans)
}
