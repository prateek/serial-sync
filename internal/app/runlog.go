package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

type runLogEvent struct {
	Timestamp  string `json:"timestamp"`
	EventID    string `json:"event_id"`
	RunID      string `json:"run_id"`
	Level      string `json:"level"`
	Component  string `json:"component"`
	Message    string `json:"message"`
	EntityKind string `json:"entity_kind"`
	EntityID   string `json:"entity_id"`
	PayloadRef string `json:"payload_ref"`
}

func (s *Service) loadRunEvents(runID string, fallback []domain.EventRecord) ([]domain.EventRecord, error) {
	path := filepath.Join(s.Config.Runtime.LogRoot, runID+".jsonl")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fallback, nil
		}
		return nil, err
	}
	defer file.Close()

	events := make([]domain.EventRecord, 0, max(1, len(fallback)))
	scanner := bufio.NewScanner(file)
	for index := 0; scanner.Scan(); index++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry runLogEvent
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, err
		}
		event, err := runLogEventRecord(entry, runID, index)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return fallback, nil
	}
	return events, nil
}

func runLogEventRecord(entry runLogEvent, runID string, index int) (domain.EventRecord, error) {
	timestamp, err := parseRunLogTimestamp(entry.Timestamp)
	if err != nil {
		return domain.EventRecord{}, err
	}
	eventID := strings.TrimSpace(entry.EventID)
	if eventID == "" {
		eventID = syntheticRunEventID(runID, index)
	}
	logRunID := strings.TrimSpace(entry.RunID)
	if logRunID == "" {
		logRunID = runID
	}
	return domain.EventRecord{
		ID:         eventID,
		RunID:      logRunID,
		Timestamp:  timestamp,
		Level:      entry.Level,
		Component:  entry.Component,
		Message:    entry.Message,
		EntityKind: entry.EntityKind,
		EntityID:   entry.EntityID,
		PayloadRef: entry.PayloadRef,
	}, nil
}

func syntheticRunEventID(runID string, index int) string {
	return runID + "_evt_" + strconv.Itoa(index+1)
}

func parseRunLogTimestamp(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
}
