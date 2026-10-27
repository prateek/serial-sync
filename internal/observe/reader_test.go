package observe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
)

// recordingStore captures the run lifecycle so the test drives the real
// writer and asserts the reader reads back what was written.
type recordingStore struct {
	store.Repository
	events []domain.EventRecord
	run    domain.RunRecord
}

func (r *recordingStore) StartRun(_ context.Context, run domain.RunRecord) error {
	r.run = run
	return nil
}
func (r *recordingStore) AddEvent(_ context.Context, event domain.EventRecord) error {
	r.events = append(r.events, event)
	return nil
}
func (r *recordingStore) FinishRun(_ context.Context, _ string, _ domain.RunStatus, _ string) error {
	return nil
}

func TestRunRecordRoundTrip(t *testing.T) {
	root := t.TempDir()
	repo := &recordingStore{}
	recorder, err := Start(context.Background(), repo, "run", "alpha", false, Options{LogRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.EventDataK(context.Background(), "info", KindReleaseSynced, "release synced", "release", "rel_1", map[string]any{"duration_ms": 12}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.EventDataK(context.Background(), "info", KindPublishPlanned, "planned filesystem publish", "artifact", "art_1", nil); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Finish(context.Background(), domain.RunStatusSucceeded, "done"); err != nil {
		t.Fatal(err)
	}

	record, err := ReadRun(root, recorder.RunID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	forensics := record.Forensics()
	if forensics.KindCounts[KindReleaseSynced] != 1 || forensics.KindCounts[KindPublishPlanned] != 1 {
		t.Fatalf("kind counts from declared kinds: %+v", forensics.KindCounts)
	}
	if forensics.KindCounts[KindRunFinished] != 1 {
		t.Fatalf("finish event lost its kind: %+v", forensics.KindCounts)
	}
	if forensics.ErrorEvents != 0 || forensics.InfoEvents < 3 {
		t.Fatalf("level counts: %+v", forensics)
	}
	if len(forensics.ProgressHighlights) != 0 {
		t.Fatalf("unexpected highlights: %v", forensics.ProgressHighlights)
	}
}

// An old run log line without a kind still classifies by its message, the
// migration fallback (TODO remove after 2026-12-01).
func TestRunRecordClassifiesKindlessLines(t *testing.T) {
	root := t.TempDir()
	line := `{"timestamp":"2026-09-01T00:00:00Z","run_id":"run_old","command":"run","level":"info","component":"sync","message":"release unchanged","kind":""}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "run_old.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	record, err := ReadRun(root, "run_old", nil)
	if err != nil {
		t.Fatal(err)
	}
	forensics := record.Forensics()
	if forensics.KindCounts[KindReleaseUnchanged] != 1 {
		t.Fatalf("substring fallback did not classify the legacy line: %+v", forensics.KindCounts)
	}
}

// A declared kind must beat the message text, because the substring fallback
// exists only for logs written before kinds. A publish failure whose error
// text happens to read "completed" is the case that silently miscounted.
func TestDeclaredKindWinsOverAMisleadingMessage(t *testing.T) {
	root := t.TempDir()
	repo := &recordingStore{}
	recorder, err := Start(context.Background(), repo, "run", "alpha", false, Options{LogRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.EventK(context.Background(), "error", KindPublishFailed, "hook completed with a nonzero status", "artifact", "art_1"); err != nil {
		t.Fatal(err)
	}
	record, err := ReadRun(root, recorder.RunID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	forensics := record.Forensics()
	if forensics.KindCounts[KindPublishFailed] != 1 {
		t.Fatalf("declared kind ignored: %+v", forensics.KindCounts)
	}
	if forensics.KindCounts[KindPublishCompleted] != 0 {
		t.Fatalf("message text overrode the declared kind: %+v", forensics.KindCounts)
	}
	if forensics.ErrorEvents != 1 || len(forensics.RecentErrors) != 1 {
		t.Fatalf("error not recorded: errors=%d recent=%d", forensics.ErrorEvents, len(forensics.RecentErrors))
	}
	if forensics.RecentErrors[0].EventID == "" || forensics.RecentErrors[0].RunID != recorder.RunID() {
		t.Fatalf("recent error lacks identity: %+v", forensics.RecentErrors[0])
	}
}

// Every kind names a component by construction; this pins that no kind can be
// added without one.
func TestEveryDeclaredKindHasItsOwnComponent(t *testing.T) {
	for kind, component := range components {
		if component == "" {
			t.Fatalf("kind %q has no component", kind)
		}
		if kind.Component() != component {
			t.Fatalf("kind %q reports %q, declared %q", kind, kind.Component(), component)
		}
	}
	if Kind("never_declared").Component() != "run" {
		t.Fatal("an unknown kind should report the run component")
	}
}

func TestRunRecordCatalogFallback(t *testing.T) {
	for _, name := range []string{"missing", "empty", "blank", "log wins"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			fallback := []domain.EventRecord{
				{ID: "legacy_id", RunID: "legacy_run", Timestamp: time.Date(2026, 10, 27, 0, 0, 0, 123456789, time.UTC), Level: "error", Component: "publish", Kind: string(KindPublishFailed), Message: "hook completed", EntityKind: "artifact", EntityID: "art_1", PayloadRef: filepath.Join(root, "payload.json")},
				{ID: "", RunID: "", Level: "info", Component: "sync", Message: "release unchanged"},
			}
			if err := os.WriteFile(fallback[0].PayloadRef, []byte(`{"duration_ms":13}`), 0o600); err != nil {
				t.Fatal(err)
			}
			body := ""
			if name == "blank" {
				body = "\n \n\t\n"
			}
			if name == "log wins" {
				body = `{"timestamp":"2026-10-27T00:00:00.123456789-04:00","component":"sync","message":"release synced"}`
			}
			if name != "missing" {
				if err := os.WriteFile(filepath.Join(root, "run_test.jsonl"), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			record, err := ReadRun(root, "run_test", fallback)
			if err != nil {
				t.Fatal(err)
			}
			forensics := record.Forensics()
			if name == "log wins" {
				if len(record.Events) != 1 || forensics.KindCounts[KindReleaseSynced] != 1 || forensics.ErrorEvents != 0 || record.Events[0].EventID != "run_test_evt_1" {
					t.Fatalf("catalog events mixed into log: %+v", record)
				}
				stamp := record.Events[0].Timestamp
				_, offset := stamp.Zone()
				if stamp.Nanosecond() != 123456789 || offset != -4*60*60 {
					t.Fatalf("timestamp lost precision or offset: %s", stamp)
				}
				return
			}
			if len(record.Events) != len(fallback) || forensics.KindCounts[KindPublishFailed] != 1 || forensics.KindCounts[KindPublishCompleted] != 0 || forensics.KindCounts[KindReleaseUnchanged] != 1 || forensics.ErrorEvents != 1 || forensics.InfoEvents != 1 || forensics.EventPayloadCount != 1 || forensics.ComponentCounts["publish"] != 1 || forensics.EntityCounts["artifact"] != 1 {
				t.Fatalf("incorrect catalog forensics: %+v", forensics)
			}
			for i, event := range record.Events {
				want := fallback[i]
				if event.EventID != want.ID || event.RunID != want.RunID || !event.Timestamp.Equal(want.Timestamp) || event.Level != want.Level || string(event.Kind) != want.Kind || event.Component != want.Component || event.Message != want.Message || event.EntityKind != want.EntityKind || event.EntityID != want.EntityID || event.PayloadRef != want.PayloadRef {
					t.Fatalf("catalog event changed: %+v; want %+v", event, want)
				}
			}
			if !reflect.DeepEqual(forensics.RecentErrors, record.Events[:1]) {
				t.Fatal("recent errors lost catalog identity")
			}
		})
	}
}

func TestRunRecordRejectsInvalidLogsWithoutFallback(t *testing.T) {
	for _, body := range []string{
		`{`,
		`{"timestamp":"invalid"}`,
		`{"message":"missing timestamp"}`,
		`{"timestamp":null}`,
	} {
		t.Run(body[:min(25, len(body))], func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "run_test.jsonl"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			record, err := ReadRun(root, "run_test", []domain.EventRecord{{ID: "must_not_use"}})
			if err == nil || !strings.Contains(err.Error(), "run_test.jsonl") {
				t.Fatalf("expected contextual log error, got %v", err)
			}
			if len(record.Events) != 0 {
				t.Fatalf("invalid log used fallback: %+v", record.Events)
			}
		})
	}
}

func TestRunRecordRejectsOversizedJSONLine(t *testing.T) {
	root := t.TempDir()
	body, err := json.Marshal(map[string]any{
		"timestamp": "2026-10-27T00:00:00Z",
		"message":   strings.Repeat("x", 4*1024*1024),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(body) {
		t.Fatal("size-bound fixture must be valid JSON")
	}
	if err := os.WriteFile(filepath.Join(root, "run_test.jsonl"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := ReadRun(root, "run_test", []domain.EventRecord{{ID: "must_not_use"}})
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("expected scanner size bound, got %v", err)
	}
	if len(record.Events) != 0 {
		t.Fatal("oversized log used fallback")
	}
}

func TestRunRecordForensicsUsesLoadedEvents(t *testing.T) {
	root := t.TempDir()
	payloadRef := filepath.Join(root, "payload.json")
	body, err := json.Marshal(LogEvent{Timestamp: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC), Message: "Patreon live release listing complete", PayloadRef: payloadRef})
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "run_test.jsonl")
	if err := os.WriteFile(logPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := ReadRun(root, "run_test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(logPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payloadRef, []byte(`{"duration_ms":37,"documents":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	forensics := record.Forensics()
	if forensics.InfoEvents != 1 || forensics.EventPayloadCount != 1 || forensics.PhaseTimingsMS["provider_list_releases"] != 37 || !reflect.DeepEqual(forensics.ProgressHighlights, []string{"live listing documents=2 duration=37ms"}) {
		t.Fatalf("forensics did not derive from loaded events and current payload: %+v", forensics)
	}
}

func TestRunRecordEmptyFallbackShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fallback []domain.EventRecord
	}{
		{name: "nil"},
		{name: "non-nil empty", fallback: []domain.EventRecord{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record, err := ReadRun(t.TempDir(), "missing", tc.fallback)
			if err != nil {
				t.Fatal(err)
			}
			if len(record.Events) != 0 || (record.Events == nil) != (tc.fallback == nil) {
				t.Fatalf("fallback shape changed: %#v", record.Events)
			}
		})
	}
}
