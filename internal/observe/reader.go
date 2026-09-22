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
)

// LogEvent is one line of a run's JSONL event log, the durable record of what
// happened. RunRecord is the whole read: the run's identity plus every event,
// and the derived forensics counts, phase timings and progress highlights the
// debug commands print. internal/observe owns both writing and reading this
// record; the app formats it.
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

// RunRecord is one run's read-back: identity, events and derived forensics.
type RunRecord struct {
	RunID              string
	Command            string
	SourceScope        string
	DryRun             bool
	Events             []LogEvent
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

// ReadRun loads the JSONL event log for runID under logRoot. A missing log is
// an empty record, not an error: runs executed without logging configured
// have nothing to read, and debug commands report that as zero events.
func ReadRun(logRoot, runID string) (RunRecord, error) {
	record := RunRecord{
		RunID: runID, KindCounts: map[Kind]int{}, ComponentCounts: map[string]int{},
		EntityCounts: map[string]int{}, PhaseTimingsMS: map[string]int64{},
	}
	path := filepath.Join(logRoot, runID+".jsonl")
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return record, nil
	}
	if err != nil {
		return record, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for index := 0; scanner.Scan(); index++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event LogEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return record, fmt.Errorf("parse run log %s: %w", path, err)
		}
		if strings.TrimSpace(event.EventID) == "" {
			event.EventID = SyntheticEventID(runID, index)
		}
		if strings.TrimSpace(event.RunID) == "" {
			event.RunID = runID
		}
		record.Events = append(record.Events, event)
		record.KindCounts[Classify(event)]++
		if component := strings.TrimSpace(event.Component); component != "" {
			record.ComponentCounts[component]++
		}
		if entityKind := strings.TrimSpace(event.EntityKind); entityKind != "" {
			record.EntityCounts[entityKind]++
		}
		switch strings.ToLower(strings.TrimSpace(event.Level)) {
		case "warn", "warning":
			record.WarningEvents++
		case "error":
			record.ErrorEvents++
			record.RecentErrors = append(record.RecentErrors, event)
			if len(record.RecentErrors) > 5 {
				record.RecentErrors = record.RecentErrors[len(record.RecentErrors)-5:]
			}
		default:
			record.InfoEvents++
		}
		if strings.Contains(strings.ToLower(event.Message), "rate limited") {
			record.RetryEvents++
		}
		if strings.TrimSpace(event.PayloadRef) != "" {
			record.EventPayloadCount++
		}
		payload, payloadErr := loadPayload(event.PayloadRef)
		if payloadErr != nil {
			continue
		}
		if durationMS, ok := payloadInt64(payload, "duration_ms"); ok {
			if phase := phaseName(event); phase != "" {
				record.PhaseTimingsMS[phase] = durationMS
			}
		}
		if highlight := progressHighlight(event, payload); highlight != "" {
			record.ProgressHighlights = append(record.ProgressHighlights, highlight)
			if len(record.ProgressHighlights) > 8 {
				record.ProgressHighlights = record.ProgressHighlights[len(record.ProgressHighlights)-8:]
			}
		}
	}
	return record, scanner.Err()
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
