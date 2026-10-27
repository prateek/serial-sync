package observe

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

// LogEvent is one event from a run's JSONL log.
type LogEvent struct {
	Timestamp   time.Time `json:"timestamp"`
	EventID     string    `json:"event_id,omitempty"`
	Level       string    `json:"level"`
	Kind        Kind      `json:"kind,omitempty"`
	Component   string    `json:"component"`
	Message     string    `json:"message"`
	EntityKind  string    `json:"entity_kind,omitempty"`
	EntityID    string    `json:"entity_id,omitempty"`
	PayloadRef  string    `json:"payload_ref,omitempty"`
	RunID       string    `json:"run_id"`
	Command     string    `json:"command"`
	SourceScope string    `json:"source_scope"`
	DryRun      bool      `json:"dry_run"`
}

// RunRecord contains a run's selected events, without reading their payloads.
type RunRecord struct {
	RunID       string
	Command     string
	SourceScope string
	DryRun      bool
	Events      []LogEvent
}

// Forensics contains counts and payload-derived details for a run record.
type Forensics struct {
	KindCounts         map[Kind]int
	ComponentCounts    map[string]int
	EntityCounts       map[string]int
	InfoEvents         int
	WarningEvents      int
	ErrorEvents        int
	EventPayloadCount  int
	PhaseTimingsMS     map[string]int64
	ProgressHighlights []string
	RetryEvents        int
	RecentErrors       []LogEvent
}

// ReadRun loads JSONL events without reading payloads. Missing or empty logs
// use fallback events in their given order. With no events, nil fallback keeps
// Events nil; a non-nil empty fallback produces an empty, non-nil Events slice.
// Malformed logs return an error rather than falling back.
func ReadRun(logRoot, runID string, fallback []domain.EventRecord) (RunRecord, error) {
	record := RunRecord{RunID: runID}
	events, err := readLogEvents(filepath.Join(logRoot, runID+".jsonl"), runID)
	if err != nil {
		return record, err
	}
	if len(events) == 0 && fallback != nil {
		events = make([]LogEvent, 0, len(fallback))
		for _, event := range fallback {
			events = append(events, LogEvent{
				Timestamp: event.Timestamp, EventID: event.ID, RunID: event.RunID,
				Level: event.Level, Kind: Kind(event.Kind), Component: event.Component,
				Message: event.Message, EntityKind: event.EntityKind, EntityID: event.EntityID,
				PayloadRef: event.PayloadRef,
			})
		}
	}
	record.Events = events
	return record, nil
}

// Forensics derives counts and details from these events, reading their payload
// references where available. It does not reopen or reparse the run log.
func (r RunRecord) Forensics() Forensics {
	forensics := Forensics{
		KindCounts: map[Kind]int{}, ComponentCounts: map[string]int{},
		EntityCounts: map[string]int{}, PhaseTimingsMS: map[string]int64{},
	}
	for _, event := range r.Events {
		forensics.KindCounts[Classify(event)]++
		if component := strings.TrimSpace(event.Component); component != "" {
			forensics.ComponentCounts[component]++
		}
		if entityKind := strings.TrimSpace(event.EntityKind); entityKind != "" {
			forensics.EntityCounts[entityKind]++
		}
		switch strings.ToLower(strings.TrimSpace(event.Level)) {
		case "warn", "warning":
			forensics.WarningEvents++
		case "error":
			forensics.ErrorEvents++
			forensics.RecentErrors = append(forensics.RecentErrors, event)
			if len(forensics.RecentErrors) > 5 {
				forensics.RecentErrors = forensics.RecentErrors[len(forensics.RecentErrors)-5:]
			}
		default:
			forensics.InfoEvents++
		}
		if strings.Contains(strings.ToLower(event.Message), "rate limited") {
			forensics.RetryEvents++
		}
		if strings.TrimSpace(event.PayloadRef) != "" {
			forensics.EventPayloadCount++
		}
		payload, payloadErr := loadPayload(event.PayloadRef)
		if payloadErr != nil {
			continue
		}
		if durationMS, ok := payloadInt64(payload, "duration_ms"); ok {
			if phase := phaseName(event); phase != "" {
				forensics.PhaseTimingsMS[phase] = durationMS
			}
		}
		if highlight := progressHighlight(event, payload); highlight != "" {
			forensics.ProgressHighlights = append(forensics.ProgressHighlights, highlight)
			if len(forensics.ProgressHighlights) > 8 {
				forensics.ProgressHighlights = forensics.ProgressHighlights[len(forensics.ProgressHighlights)-8:]
			}
		}
	}
	return forensics
}

func readLogEvents(path, runID string) ([]LogEvent, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var events []LogEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for index := 0; scanner.Scan(); index++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry struct {
			LogEvent
			Timestamp string `json:"timestamp"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parse run log %s: %w", path, err)
		}
		event := entry.LogEvent
		event.Timestamp, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(entry.Timestamp))
		if err != nil {
			return nil, fmt.Errorf("parse run log %s: %w", path, err)
		}
		event.EventID = strings.TrimSpace(event.EventID)
		if event.EventID == "" {
			event.EventID = SyntheticEventID(runID, index)
		}
		event.RunID = strings.TrimSpace(event.RunID)
		if event.RunID == "" {
			event.RunID = runID
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read run log %s: %w", path, err)
	}
	return events, nil
}

// Classify answers the event's kind: the declared kind when the writer set
// one, otherwise the message it used to be matched by.
//
// TODO(observe): remove the substring fallback once every run log in the
// support-bundle retention window carries kinds (drop after 2026-12-01).
func Classify(event LogEvent) Kind {
	if event.Kind != "" {
		return event.Kind
	}
	component := strings.TrimSpace(event.Component)
	message := strings.ToLower(strings.TrimSpace(event.Message))
	switch component {
	case "sync":
		switch {
		case strings.Contains(message, "release synced"):
			return KindReleaseSynced
		case strings.Contains(message, "release unchanged"):
			return KindReleaseUnchanged
		case strings.Contains(message, "planned"):
			return KindReleasePlanned
		}
	case "publish":
		switch {
		case strings.Contains(message, "planned"):
			return KindPublishPlanned
		case strings.Contains(message, "skipped"):
			return KindPublishSkipped
		case strings.Contains(message, "completed"):
			return KindPublishCompleted
		case strings.ToLower(strings.TrimSpace(event.Level)) == "error":
			return KindPublishFailed
		}
	case "classify":
		if strings.Contains(message, "unmatched") {
			return KindClassifyUnmatched
		}
		return KindClassifyMatched
	}
	return Kind(strings.ToLower(component))
}

func loadPayload(path string) (map[string]any, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func payloadInt64(payload map[string]any, key string) (int64, bool) {
	if payload == nil {
		return 0, false
	}
	value, ok := payload[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	}
	return 0, false
}

// phaseName maps a provider phase-completion event to its timing phase.
func phaseName(event LogEvent) string {
	switch strings.TrimSpace(event.Message) {
	case "resolved Patreon session":
		return "provider_session_resolution"
	case "bootstrapped Patreon session":
		return "provider_session_bootstrap"
	case "Patreon collection scan complete":
		return "provider_collection_scan"
	case "Patreon feed pagination complete":
		return "provider_feed_pagination"
	case "Patreon post detail fetch complete":
		return "provider_post_detail_fetch"
	case "Patreon live release listing complete":
		return "provider_list_releases"
	case "downloaded Patreon attachment":
		return "provider_attachment_download"
	}
	return ""
}

func progressHighlight(event LogEvent, payload map[string]any) string {
	switch strings.TrimSpace(event.Message) {
	case "Patreon feed pagination complete":
		return fmt.Sprintf("feed pagination discovered=%d pages=%d stop=%s duration=%dms", intOrZero(payload["discovered_ids"]), intOrZero(payload["pages"]), stringOrEmpty(payload["stop_reason"]), intOrZero(payload["duration_ms"]))
	case "Patreon post detail fetch complete":
		return fmt.Sprintf("post detail fetch completed=%d total=%d failed=%d duration=%dms", intOrZero(payload["completed"]), intOrZero(payload["total_posts"]), intOrZero(payload["failed"]), intOrZero(payload["duration_ms"]))
	case "Patreon rate limited request; backing off":
		return fmt.Sprintf("rate limited attempt=%d delay=%dms", intOrZero(payload["attempt"]), intOrZero(payload["delay_ms"]))
	case "Patreon request budget reduced", "Patreon request budget increased":
		budget, _ := payload["budget"].(map[string]any)
		return fmt.Sprintf("%s limit=%d inflight=%d", strings.ToLower(strings.TrimSpace(event.Message)), intOrZero(budget["limit"]), intOrZero(budget["in_flight"]))
	case "Patreon live release listing complete":
		return fmt.Sprintf("live listing documents=%d duration=%dms", intOrZero(payload["documents"]), intOrZero(payload["duration_ms"]))
	}
	return ""
}

func intOrZero(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	}
	return 0
}

func stringOrEmpty(value any) string {
	if typed, ok := value.(string); ok {
		return typed
	}
	return ""
}

// SyntheticEventID names an event that predates event ids in the run log, by
// its position in that log.
func SyntheticEventID(runID string, index int) string {
	return runID + "_evt_" + strconv.Itoa(index+1)
}
