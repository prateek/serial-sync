package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
)

type Options struct {
	LogRoot string
}

type Recorder struct {
	repo       store.Repository
	run        domain.RunRecord
	logRoot    string
	textLog    *os.File
	jsonLog    *os.File
	payloadDir string
}

type logEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	RunID       string    `json:"run_id"`
	Command     string    `json:"command"`
	SourceScope string    `json:"source_scope"`
	DryRun      bool      `json:"dry_run"`
	Level       string    `json:"level"`
	Component   string    `json:"component"`
	Message     string    `json:"message"`
	EntityKind  string    `json:"entity_kind,omitempty"`
	EntityID    string    `json:"entity_id,omitempty"`
	PayloadRef  string    `json:"payload_ref,omitempty"`
}

func Start(ctx context.Context, repo store.Repository, command string, sourceScope string, dryRun bool, opts Options) (*Recorder, error) {
	run := domain.RunRecord{
		ID:          "run_" + uuid.NewString(),
		Command:     command,
		StartedAt:   time.Now().UTC(),
		Status:      domain.RunStatusRunning,
		SourceScope: sourceScope,
		DryRun:      dryRun,
	}
	recorder := &Recorder{
		repo:       repo,
		run:        run,
		logRoot:    strings.TrimSpace(opts.LogRoot),
		payloadDir: filepath.Join(strings.TrimSpace(opts.LogRoot), "event-payloads", run.ID),
	}
	if err := recorder.openLogs(); err != nil {
		return nil, err
	}
	if err := repo.StartRun(ctx, run); err != nil {
		recorder.closeLogs()
		return nil, err
	}
	if err := recorder.Event(ctx, "info", "run", "run started", "", ""); err != nil {
		return nil, err
	}
	return recorder, nil
}

func (r *Recorder) RunID() string {
	return r.run.ID
}

func (r *Recorder) Event(ctx context.Context, level, component, message, entityKind, entityID string) error {
	return r.EventData(ctx, level, component, message, entityKind, entityID, nil)
}

func (r *Recorder) EventData(ctx context.Context, level, component, message, entityKind, entityID string, payload any) error {
	payloadRef, err := r.writePayload(payload)
	if err != nil {
		return err
	}
	event := domain.EventRecord{
		ID:         "evt_" + uuid.NewString(),
		RunID:      r.run.ID,
		Timestamp:  time.Now().UTC(),
		Level:      strings.ToLower(level),
		Component:  component,
		Message:    message,
		EntityKind: entityKind,
		EntityID:   entityID,
		PayloadRef: payloadRef,
	}
	if err := r.repo.AddEvent(ctx, event); err != nil {
		return err
	}
	return r.log(event.Level, event.Component, event.Message, event.EntityKind, event.EntityID, event.PayloadRef)
}

func (r *Recorder) Finish(ctx context.Context, status domain.RunStatus, summary string) error {
	if err := r.EventData(ctx, "info", "run", "run finished: "+string(status), "", "", map[string]any{
		"status":  status,
		"summary": summary,
	}); err != nil {
		return err
	}
	if err := r.repo.FinishRun(ctx, r.run.ID, status, summary); err != nil {
		return err
	}
	return r.closeLogs()
}

func (r *Recorder) openLogs() error {
	if r.logRoot == "" {
		return nil
	}
	if err := os.MkdirAll(r.logRoot, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(r.payloadDir, 0o755); err != nil {
		return err
	}
	textLog, err := os.OpenFile(filepath.Join(r.logRoot, r.run.ID+".log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	jsonLog, err := os.OpenFile(filepath.Join(r.logRoot, r.run.ID+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = textLog.Close()
		return err
	}
	r.textLog = textLog
	r.jsonLog = jsonLog
	return nil
}

func (r *Recorder) closeLogs() error {
	var closeErr error
	if r.textLog != nil {
		if err := r.textLog.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
		r.textLog = nil
	}
	if r.jsonLog != nil {
		if err := r.jsonLog.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
		r.jsonLog = nil
	}
	return closeErr
}

func (r *Recorder) writePayload(payload any) (string, error) {
	if payload == nil || r.logRoot == "" {
		return "", nil
	}
	if err := os.MkdirAll(r.payloadDir, 0o755); err != nil {
		return "", err
	}
	payloadID := "evt_" + uuid.NewString()
	payloadPath := filepath.Join(r.payloadDir, payloadID+".json")
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(payloadPath, data, 0o644); err != nil {
		return "", err
	}
	return payloadPath, nil
}

func (r *Recorder) log(level, component, message, entityKind, entityID, payloadRef string) error {
	entry := logEntry{
		Timestamp:   time.Now().UTC(),
		RunID:       r.run.ID,
		Command:     r.run.Command,
		SourceScope: r.run.SourceScope,
		DryRun:      r.run.DryRun,
		Level:       strings.ToLower(level),
		Component:   component,
		Message:     message,
		EntityKind:  entityKind,
		EntityID:    entityID,
		PayloadRef:  payloadRef,
	}
	if r.jsonLog != nil {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := r.jsonLog.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	if r.textLog != nil {
		line := fmt.Sprintf(
			"%s %-5s %-8s command=%q scope=%q dry_run=%t",
			entry.Timestamp.Format(time.RFC3339),
			strings.ToUpper(entry.Level),
			entry.Component,
			entry.Command,
			entry.SourceScope,
			entry.DryRun,
		)
		if entry.EntityKind != "" && entry.EntityID != "" {
			line += fmt.Sprintf(" %s=%q", entry.EntityKind, entry.EntityID)
		}
		if entry.PayloadRef != "" {
			line += fmt.Sprintf(" payload=%q", entry.PayloadRef)
		}
		line += " " + entry.Message + "\n"
		if _, err := r.textLog.WriteString(line); err != nil {
			return err
		}
	}
	return nil
}
