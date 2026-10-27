package app

import (
	"context"
	"fmt"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
)

func (s *Service) loadRunRecord(ctx context.Context, runID string) (*domain.RunBundle, observe.RunRecord, error) {
	bundle, err := s.Repo.GetRunBundle(ctx, runID)
	if err != nil {
		return nil, observe.RunRecord{}, err
	}
	if bundle == nil {
		return nil, observe.RunRecord{}, fmt.Errorf("unknown run %q", runID)
	}
	record, err := observe.ReadRun(s.Config.Runtime.LogRoot, runID, bundle.Events)
	if err != nil {
		return nil, record, err
	}
	if record.History == "unavailable" {
		before, err := observe.ExpiredBefore(s.Config.Runtime.LogRoot)
		if err != nil {
			return nil, record, err
		}
		if bundle.Run.FinishedAt != nil && bundle.Run.FinishedAt.Before(before) {
			record.History = "expired"
		}
	}
	bundle.History = record.History
	bundle.MissingPayloads = record.MissingPayloads
	bundle.Events = nil
	if record.Events != nil {
		bundle.Events = make([]domain.EventRecord, 0, len(record.Events))
		for _, event := range record.Events {
			bundle.Events = append(bundle.Events, eventRecordFromLog(event))
		}
	}
	return bundle, record, nil
}

func eventRecordFromLog(event observe.LogEvent) domain.EventRecord {
	return domain.EventRecord{
		ID:         event.EventID,
		RunID:      event.RunID,
		Timestamp:  event.Timestamp,
		Level:      event.Level,
		Kind:       string(event.Kind),
		Component:  event.Component,
		Message:    event.Message,
		EntityKind: event.EntityKind,
		EntityID:   event.EntityID,
		PayloadRef: event.PayloadRef,
	}
}
