package app

import (
	"context"
	"strings"

	"github.com/prateek/serial-sync/internal/observe"
	"github.com/prateek/serial-sync/internal/provider"
)

func withRecorderProgress(ctx context.Context, recorder *observe.Recorder) context.Context {
	if recorder == nil {
		return ctx
	}
	return provider.WithProgress(ctx, provider.ProgressReporterFunc(func(ctx context.Context, event provider.ProgressEvent) {
		level := strings.TrimSpace(event.Level)
		if level == "" {
			level = "info"
		}
		component := strings.TrimSpace(event.Component)
		if component == "" {
			component = "provider"
		}
		message := strings.TrimSpace(event.Message)
		if message == "" {
			message = "provider progress"
		}
		entityKind := strings.TrimSpace(event.EntityKind)
		entityID := strings.TrimSpace(event.EntityID)
		if event.Payload != nil {
			_ = recorder.EventData(ctx, level, component, message, entityKind, entityID, event.Payload)
			return
		}
		_ = recorder.Event(ctx, level, component, message, entityKind, entityID)
	}))
}
