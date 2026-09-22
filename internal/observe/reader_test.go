package observe

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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

	record, err := ReadRun(root, recorder.RunID())
	if err != nil {
		t.Fatal(err)
	}
	if record.KindCounts[KindReleaseSynced] != 1 || record.KindCounts[KindPublishPlanned] != 1 {
		t.Fatalf("kind counts from declared kinds: %+v", record.KindCounts)
	}
	if record.KindCounts[KindRunFinished] != 1 {
		t.Fatalf("finish event lost its kind: %+v", record.KindCounts)
	}
	if record.ErrorEvents != 0 || record.InfoEvents < 3 {
		t.Fatalf("level counts: %+v", record)
	}
	if len(record.ProgressHighlights) != 0 {
		t.Fatalf("unexpected highlights: %v", record.ProgressHighlights)
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
	record, err := ReadRun(root, "run_old")
	if err != nil {
		t.Fatal(err)
	}
	if record.KindCounts[KindReleaseUnchanged] != 1 {
		t.Fatalf("substring fallback did not classify the legacy line: %+v", record.KindCounts)
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
	record, err := ReadRun(root, recorder.RunID())
	if err != nil {
		t.Fatal(err)
	}
	if record.KindCounts[KindPublishFailed] != 1 {
		t.Fatalf("declared kind ignored: %+v", record.KindCounts)
	}
	if record.KindCounts[KindPublishCompleted] != 0 {
		t.Fatalf("message text overrode the declared kind: %+v", record.KindCounts)
	}
	if record.ErrorEvents != 1 || len(record.RecentErrors) != 1 {
		t.Fatalf("error not recorded: errors=%d recent=%d", record.ErrorEvents, len(record.RecentErrors))
	}
	if record.RecentErrors[0].EventID == "" || record.RecentErrors[0].RunID != recorder.RunID() {
		t.Fatalf("recent error lacks identity: %+v", record.RecentErrors[0])
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
