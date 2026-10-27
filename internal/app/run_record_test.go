package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

func TestRunRecordLogBackedCallersAgree(t *testing.T) {
	for _, tc := range []struct {
		name, suffix string
		pad          bool
	}{
		{name: "long line", suffix: strings.Repeat("x", 71781)},
		{name: "legacy identity", pad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, run := newRunRecordService(t)
			payloadRef := filepath.Join(service.Config.Runtime.LogRoot, "payload.json")
			if err := os.WriteFile(payloadRef, []byte(`{"duration_ms":37,"documents":2}`), 0o600); err != nil {
				t.Fatal(err)
			}
			want := []domain.EventRecord{
				{ID: "run_test_evt_2", RunID: run.ID, Timestamp: run.StartedAt.Add(time.Second), Level: "info", Component: "sync", Message: "release unchanged"},
				{ID: "explicit", RunID: "writer_run", Timestamp: run.StartedAt, Level: "error", Kind: string(observe.KindPublishFailed), Component: "publish", Message: "hook completed with a nonzero status" + tc.suffix, EntityKind: "artifact", EntityID: "art_missing", PayloadRef: payloadRef},
				{ID: "run_test_evt_5", RunID: run.ID, Timestamp: run.StartedAt, Level: "warning", Component: "provider", Message: "Patreon live release listing complete", PayloadRef: payloadRef},
			}
			logBody := "\n"
			for i, event := range want {
				stamp := event.Timestamp.Format(time.RFC3339Nano)
				id, runID := event.ID, event.RunID
				if i != 1 {
					id, runID = "", ""
				}
				if tc.pad {
					stamp = " " + stamp + " "
					id, runID = " "+id+" ", " "+runID+" "
				}
				data, err := json.Marshal(map[string]any{"timestamp": stamp, "event_id": id, "run_id": runID, "level": event.Level, "kind": event.Kind, "component": event.Component, "message": event.Message, "entity_kind": event.EntityKind, "entity_id": event.EntityID, "payload_ref": event.PayloadRef})
				if err != nil {
					t.Fatal(err)
				}
				logBody += string(data) + "\n"
				if i == 1 {
					logBody += "\n"
				}
			}
			if err := os.WriteFile(filepath.Join(service.Config.Runtime.LogRoot, run.ID+".jsonl"), []byte(logBody), 0o600); err != nil {
				t.Fatal(err)
			}
			explained, dir := checkRunRecordCallers(t, service, run.ID, want)
			if explained.InfoEvents != 1 || explained.WarningEvents != 1 || explained.ErrorEvents != 1 || explained.ReleaseUnchanged != 1 || explained.PublishFailed != 1 || explained.PublishSucceeded != 0 || explained.EventPayloadCount != 2 || explained.PhaseTimingsMS["provider_list_releases"] != 37 || len(explained.ProgressHighlights) != 1 {
				t.Fatalf("wrong forensics: %+v", explained)
			}
			if !reflect.DeepEqual(explained.RecentErrors, want[1:2]) {
				t.Fatal("recent errors differ from listed events")
			}
			filtered, err := service.ListRunEvents(context.Background(), run.ID, app.RunEventFilter{Level: "error", Component: "publish", EntityKind: "artifact", EntityID: "art_missing", Limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(filtered.Events, want[1:2]) {
				t.Fatal("filter lost event identity or order")
			}
			for _, event := range want[1:] {
				data, err := os.ReadFile(filepath.Join(dir, "payloads", "events", event.ID, "payload.json"))
				if err != nil || string(data) != `{"duration_ms":37,"documents":2}` {
					t.Fatalf("exported payload: %s, %v", data, err)
				}
			}
		})
	}
}

func TestRunRecordMissingOrEmptyLogCallersAgree(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		missing    bool
	}{
		{name: "missing", missing: true},
		{name: "empty"},
		{name: "blank lines", body: "\n \n\t\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, run := newRunRecordService(t)
			if !tc.missing {
				if err := os.WriteFile(filepath.Join(service.Config.Runtime.LogRoot, run.ID+".jsonl"), []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			explained, _ := checkRunRecordCallers(t, service, run.ID, nil)
			if explained.InfoEvents+explained.WarningEvents+explained.ErrorEvents != 0 || explained.EventPayloadCount != 0 || len(explained.RecentErrors) != 0 {
				t.Fatal("expected empty forensics")
			}
		})
	}
}

func checkRunRecordCallers(t *testing.T, service *app.Service, runID string, want []domain.EventRecord) (*app.RunForensics, string) {
	t.Helper()
	ctx := context.Background()
	inspected, err := service.InspectRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inspected.Events, want) {
		t.Fatalf("inspection events differ: got %d events, want %d", len(inspected.Events), len(want))
	}
	record, err := observe.ReadRun(service.Config.Runtime.LogRoot, runID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Events) != len(want) {
		t.Fatal("observe and inspection disagree on event count")
	}
	for i, event := range record.Events {
		if event.EventID != want[i].ID || event.RunID != want[i].RunID || !event.Timestamp.Equal(want[i].Timestamp) || string(event.Kind) != want[i].Kind {
			t.Fatalf("observe and inspection disagree on event %d", i)
		}
	}
	listed, err := service.ListRunEvents(ctx, runID, app.RunEventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Events) != len(want) || (len(want) > 0 && !reflect.DeepEqual(listed.Events, want)) {
		t.Fatal("listing differs from inspection")
	}
	explained, err := service.ExplainRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := service.SupportBundle(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	var exported domain.RunBundle
	readRunRecordJSON(t, filepath.Join(dir, "run.json"), &exported)
	if !reflect.DeepEqual(exported, *inspected) {
		t.Fatal("support run differs from inspection")
	}
	var exportedForensics app.RunForensics
	readRunRecordJSON(t, filepath.Join(dir, "forensics.json"), &exportedForensics)
	wantForensics, err := json.Marshal(explained)
	if err != nil {
		t.Fatal(err)
	}
	gotForensics, err := json.Marshal(exportedForensics)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotForensics) != string(wantForensics) {
		t.Fatal("support forensics differs from explanation")
	}
	return explained, dir
}

func readRunRecordJSON(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatal(err)
	}
}

func newRunRecordService(t *testing.T) (*app.Service, domain.RunRecord) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	repo, err := sqlite.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	run := domain.RunRecord{ID: "run_test", Command: "run", StartedAt: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), Status: domain.RunStatusSucceeded}
	if err := repo.StartRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Runtime.LogRoot = root
	cfg.Runtime.SupportRoot = filepath.Join(root, "support")
	return app.New(cfg, config.Roots{}, "fixture.toml", repo, nil), run
}
