package observe

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
)

type Recorder struct {
	repo store.Repository
	run  domain.RunRecord
}

func Start(ctx context.Context, repo store.Repository, command string, sourceScope string, dryRun bool) (*Recorder, error) {
	run := domain.RunRecord{
		ID:          "run_" + uuid.NewString(),
		Command:     command,
		StartedAt:   time.Now().UTC(),
		Status:      domain.RunStatusRunning,
		SourceScope: sourceScope,
		DryRun:      dryRun,
	}
	if err := repo.StartRun(ctx, run); err != nil {
		return nil, err
	}
	return &Recorder{repo: repo, run: run}, nil
}

func (r *Recorder) RunID() string {
	return r.run.ID
}

func (r *Recorder) Event(ctx context.Context, level, component, message, entityKind, entityID string) error {
	return r.repo.AddEvent(ctx, domain.EventRecord{
		ID:         "evt_" + uuid.NewString(),
		RunID:      r.run.ID,
		Timestamp:  time.Now().UTC(),
		Level:      strings.ToLower(level),
		Component:  component,
		Message:    message,
		EntityKind: entityKind,
		EntityID:   entityID,
	})
}

func (r *Recorder) Finish(ctx context.Context, status domain.RunStatus, summary string) error {
	return r.repo.FinishRun(ctx, r.run.ID, status, summary)
}
