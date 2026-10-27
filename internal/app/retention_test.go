package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
	"github.com/prateek/serial-sync/internal/publish"
	"github.com/prateek/serial-sync/internal/store"
)

func TestSteadyStatePayloadGrowth(t *testing.T) {
	svc, _ := deliveryFixture(t)
	ctx := context.Background()
	recorder, err := observe.Start(ctx, svc.Repo, "synthetic", "", false, svc.observeOptions())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3000; i++ {
		for _, kind := range []observe.Kind{observe.KindReleaseUnchanged, observe.KindPublishHeld, observe.KindPublishSkipped} {
			if err := recorder.EventDataK(ctx, "info", kind, "synthetic outcome", "artifact", "fictional", map[string]any{"large": "redundant evidence"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := recorder.Finish(ctx, domain.RunStatusSucceeded, "done"); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(svc.Config.Runtime.LogRoot, "event-payloads", recorder.RunID(), "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) > 2 {
		t.Fatalf("steady-state workload wrote %d payload files; want <=2", len(paths))
	}
	record, err := observe.ReadRun(svc.Config.Runtime.LogRoot, recorder.RunID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Events) != 9002 {
		t.Fatalf("lost events: %d", len(record.Events))
	}
	info, err := os.Stat(filepath.Join(svc.Config.Runtime.LogRoot, recorder.RunID()+".jsonl"))
	if err != nil || info.Size() == 0 {
		t.Fatalf("missing event history: %v", err)
	}
}

func TestDiagnosticCleanupRetryAndPendingRecovery(t *testing.T) {
	svc, repo := deliveryFixture(t)
	ctx := context.Background()
	svc.Config.Runtime.DiagnosticRetentionDays = 10
	old := time.Now().UTC().Add(-20 * 24 * time.Hour)
	run := domain.RunRecord{ID: "run_old", StartedAt: old, Command: "synthetic", Status: domain.RunStatusRunning}
	if err := repo.StartRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishRun(ctx, run.ID, domain.RunStatusFailed, "useful failure summary"); err != nil {
		t.Fatal(err)
	}
	// Use a future horizon through the module seam so the fixture never changes the host clock.
	bundles, err := repo.ListRuns(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Add(time.Hour)
	log := filepath.Join(svc.Config.Runtime.LogRoot, run.ID+".jsonl")
	if err := os.WriteFile(log, []byte("old log"), 0600); err != nil {
		t.Fatal(err)
	}
	payloadDir := filepath.Join(svc.Config.Runtime.LogRoot, "event-payloads", run.ID)
	if err := os.MkdirAll(payloadDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payloadDir, "event.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(svc.Config.Runtime.ArtifactRoot, "deliveries")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	pendingPath := filepath.Join(dir, "pending.json")
	completedPath := filepath.Join(dir, "completed.json")
	for _, path := range []string{pendingPath, completedPath} {
		if err := os.WriteFile(path, []byte(`{"candidates":[{"fictional":"capture"}]}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pending := domain.PendingPublish{ID: "pending", TargetID: "removed-from-config", PayloadRef: pendingPath}
	if err := repo.SavePendingPublish(ctx, pending); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(svc.Config.Runtime.ArtifactRoot, "capture.json")
	if err := os.WriteFile(capture, []byte("protected capture"), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.cleanupDiagnosticSnapshot(before, bundles, []domain.PendingPublish{pending}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Paths) != 3 {
		t.Fatalf("unexpected selection: %+v", preview)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatal("preview mutated logs")
	}
	// Simulate interruption after durable intent and the first unlink.
	stamp, _ := json.Marshal(before)
	if err := os.WriteFile(filepath.Join(svc.Config.Runtime.LogRoot, "retention.json"), stamp, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		err := repo.WithDiagnosticSnapshot(ctx, true, func(runs []domain.RunRecord, plans []domain.PendingPublish) error {
			_, err := svc.cleanupDiagnosticSnapshot(before, runs, plans, true)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{pendingPath, capture} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("lost recovery content %s: %v", path, err)
		}
	}
	for _, path := range []string{payloadDir, completedPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("not expired: %s: %v", path, err)
		}
	}
	loaded, err := repo.GetPendingPublish(ctx, pending.TargetID)
	if err != nil || loaded == nil || loaded.PayloadRef != pendingPath {
		t.Fatalf("pending provenance lost: %+v %v", loaded, err)
	}
	forensics, err := svc.ExplainRun(ctx, run.ID)
	if err != nil || forensics.History != "expired" || forensics.Run.Summary != "useful failure summary" {
		t.Fatalf("debug expiry: %+v %v", forensics, err)
	}
	bundleDir, err := svc.SupportBundle(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(bundleDir, "run.json"))
	if err != nil || !strings.Contains(string(data), `"history": "expired"`) {
		t.Fatalf("support expiry: %s %v", data, err)
	}
}

func TestCleanupProtectsActiveRunsAndIsOptIn(t *testing.T) {
	svc, repo := deliveryFixture(t)
	ctx := context.Background()
	if _, err := svc.CleanupDiagnostics(ctx, true); err == nil {
		t.Fatal("expiry enabled by default")
	}
	svc.Config.Runtime.DiagnosticRetentionDays = 10
	if err := repo.StartRun(ctx, domain.RunRecord{ID: "run_active", StartedAt: time.Now(), Status: domain.RunStatusRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CleanupDiagnostics(ctx, true); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("active run not protected: %v", err)
	}
}

func TestRepeatedHeldPlansDoNotCreateSnapshots(t *testing.T) {
	svc, _ := deliveryFixture(t)
	ctx := context.Background()
	target := &publish.DropTarget{ID: "drop", Path: filepath.Join(t.TempDir(), "reading")}
	candidate := deliveryCandidate("held", "new")
	old := candidate.Artifact
	old.SHA256 = "old"
	records := []domain.PublishRecordBundle{{Record: domain.PublishRecord{ArtifactID: candidate.Artifact.ID, TargetID: "drop", Status: domain.PublishStatusPublished}, Artifact: old, Source: candidate.Source, Release: candidate.Release}}
	cfg := config.PublisherConfig{ID: "drop", Kind: "drop", Enabled: true, Path: target.Path}
	recorder := startDeliveryRecorder(t, svc)
	for i := 0; i < 100; i++ {
		outcome, err := svc.executeDelivery(ctx, recorder, deliveryScope{}, target, cfg, []domain.PublishCandidate{candidate}, records, nil, false)
		if err != nil || outcome.Failed != 0 || len(outcome.Items) != 1 || outcome.Items[0].Action != "held" {
			t.Fatalf("held outcome: %+v %v", outcome, err)
		}
	}
	records[0].Artifact = candidate.Artifact
	for i := 0; i < 100; i++ {
		outcome, err := svc.executeDelivery(ctx, recorder, deliveryScope{}, target, cfg, []domain.PublishCandidate{candidate}, records, nil, false)
		if err != nil || outcome.Skipped != 1 {
			t.Fatalf("unchanged outcome: %+v %v", outcome, err)
		}
	}
	files, err := filepath.Glob(filepath.Join(svc.Config.Runtime.ArtifactRoot, "deliveries", "*.json"))
	if err != nil || len(files) != 0 {
		t.Fatalf("no-op plans grew: %d %v", len(files), err)
	}
	if _, err := os.Stat(target.Path); !os.IsNotExist(err) {
		t.Fatalf("held workflow created reading copies: %v", err)
	}
}

func TestCompletedPlanCompactsOnlyAfterSuccess(t *testing.T) {
	svc, repo := deliveryFixture(t)
	ctx := context.Background()
	recorder, err := observe.Start(ctx, svc.Repo, "synthetic", "", false, svc.observeOptions())
	if err != nil {
		t.Fatal(err)
	}
	target := memoryTarget(t)
	candidate := deliveryCandidate("chapter", "digest")
	cfg := config.PublisherConfig{ID: "memory", Kind: "memory", Enabled: true}
	target.DeliverErr = map[string]error{candidate.Artifact.ID: os.ErrPermission}
	failed, err := svc.executeDelivery(ctx, recorder, deliveryScope{}, target, cfg, []domain.PublishCandidate{candidate}, nil, nil, false)
	if err != nil || failed.Failed != 1 {
		t.Fatalf("failure fixture: %+v %v", failed, err)
	}
	pending, err := repo.GetPendingPublish(ctx, "memory")
	if err != nil || pending == nil {
		t.Fatalf("pending absent: %+v %v", pending, err)
	}
	data, err := os.ReadFile(pending.PayloadRef)
	if err != nil || !strings.Contains(string(data), `"candidates"`) {
		t.Fatalf("pending capture lost: %s %v", data, err)
	}
	if err := recorder.Finish(ctx, domain.RunStatusFailed, "interrupted delivery"); err != nil {
		t.Fatal(err)
	}
	if err := repo.WithDiagnosticSnapshot(ctx, true, func(runs []domain.RunRecord, plans []domain.PendingPublish) error {
		_, err := svc.cleanupDiagnosticSnapshot(time.Now().Add(time.Hour), runs, plans, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	recorder, err = observe.Start(ctx, svc.Repo, "synthetic retry", "", false, svc.observeOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Finish(ctx, domain.RunStatusSucceeded, "done")
	target.DeliverErr = nil
	result, err := svc.executeDelivery(ctx, recorder, deliveryScope{}, target, cfg, []domain.PublishCandidate{candidate}, nil, nil, false)
	if err != nil || result.Failed != 0 || result.Published != 1 {
		t.Fatalf("retry: %+v %v", result, err)
	}
	if still, err := repo.GetPendingPublish(ctx, "memory"); err != nil || still != nil {
		t.Fatalf("pending not completed: %+v %v", still, err)
	}
	data, err = os.ReadFile(pending.PayloadRef)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"candidates"`) || !strings.Contains(string(data), pending.ID) || !strings.Contains(string(data), `"state":"completed"`) {
		t.Fatalf("invalid recovery receipt: %s", data)
	}
}

type compactionFailureTarget struct {
	publish.Target
	directory string
}

func (t compactionFailureTarget) Deliver(ctx context.Context, runID, scope string, candidate domain.PublishCandidate, identity publish.DeliveryIdentity, owned []string) (domain.PublishRecord, error) {
	record, err := t.Target.Deliver(ctx, runID, scope, candidate, identity, owned)
	if err != nil {
		return record, err
	}
	if err := os.Rename(t.directory, t.directory+".saved"); err != nil {
		return record, err
	}
	return record, os.WriteFile(t.directory, []byte("synthetic filesystem failure"), 0600)
}

func TestCompactionFailurePreservesSuccessfulDeliveryAndLifecycle(t *testing.T) {
	svc, repo := deliveryFixture(t)
	ctx := context.Background()
	recorder := startDeliveryRecorder(t, svc)
	dir := filepath.Join(svc.Config.Runtime.ArtifactRoot, "deliveries")
	target := compactionFailureTarget{Target: memoryTarget(t), directory: dir}
	lifecycle := filepath.Join(t.TempDir(), "lifecycle.jsonl")
	cfg := config.PublisherConfig{ID: "memory", Kind: "memory", Enabled: true, LifecycleCommand: []string{"sh", "-c", `cat >> "$1"; printf '\n' >> "$1"`, "lifecycle", lifecycle}}
	result, err := svc.executeDelivery(ctx, recorder, deliveryScope{}, target, cfg, []domain.PublishCandidate{deliveryCandidate("one", "digest")}, nil, nil, false)
	if err != nil || result.Published != 1 || result.Failed != 0 {
		t.Fatalf("diagnostic failure changed delivery: %+v %v", result, err)
	}
	pending, err := repo.GetPendingPublish(ctx, "memory")
	if err != nil || pending != nil {
		t.Fatalf("completed plan pending: %+v %v", pending, err)
	}
	data, err := os.ReadFile(lifecycle)
	if err != nil || !strings.Contains(string(data), `"action":"complete"`) {
		t.Fatalf("missing lifecycle completion: %s %v", data, err)
	}
	record, err := observe.ReadRun(svc.Config.Runtime.LogRoot, recorder.RunID(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range record.Events {
		if event.Level == "warn" && strings.Contains(event.Message, "compaction") {
			found = true
		}
	}
	if !found {
		t.Fatal("compaction failure not reported")
	}
	saved, err := filepath.Glob(dir + ".saved/*.json")
	if err != nil || len(saved) != 1 {
		t.Fatalf("full snapshot not retained: %v %v", saved, err)
	}
}

func TestResumedDeliveryCompactsItsSavedPath(t *testing.T) {
	svc, repo := deliveryFixture(t)
	ctx := context.Background()
	recorder := startDeliveryRecorder(t, svc)
	candidate := deliveryCandidate("relocated", "digest")
	target := memoryTarget(t)
	cfg := config.PublisherConfig{ID: "memory", Kind: "memory", Enabled: true}
	plan, err := svc.deliveryPlan(ctx, deliveryScope{}, target, cfg, []domain.PublishCandidate{candidate}, librarySnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	relocated := filepath.Join(t.TempDir(), "saved-plan.json")
	if err := os.Rename(plan.PayloadRef, relocated); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompletePendingPublish(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.SavePendingPublish(ctx, domain.PendingPublish{ID: plan.ID, TargetID: cfg.ID, PayloadRef: relocated}); err != nil {
		t.Fatal(err)
	}
	result, err := svc.executeDelivery(ctx, recorder, deliveryScope{}, target, cfg, []domain.PublishCandidate{candidate}, nil, nil, false)
	if err != nil || result.Failed != 0 {
		t.Fatalf("relocated delivery: %+v %v", result, err)
	}
	data, err := os.ReadFile(relocated)
	if err != nil {
		t.Fatal(err)
	}
	var receipt deliveryReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.State != "completed" || receipt.ID != plan.ID || receipt.RunID != recorder.RunID() {
		t.Fatalf("wrong relocated receipt: %+v", receipt)
	}
	if _, err := os.Stat(plan.PayloadRef); !os.IsNotExist(err) {
		t.Fatalf("compaction created a second snapshot: %v", err)
	}
}

func TestDeliveryExpiryProtectsCatalogReferencesAndRemovesTemps(t *testing.T) {
	svc, _ := deliveryFixture(t)
	files := svc.deliveryFiles()
	if err := os.MkdirAll(files.directory, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	before := time.Now().Add(-10 * 24 * time.Hour)
	for _, name := range []string{"pending.json", "aliased.json", "completed.json", ".retention-orphan"} {
		path := filepath.Join(files.directory, name)
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	pending := []domain.PendingPublish{{ID: "pending", PayloadRef: "/other/root/pending.json"}, {ID: "different-id", PayloadRef: filepath.Join(files.directory, "aliased.json")}}
	paths, err := files.expired(before, pending)
	if err != nil || len(paths) != 2 {
		t.Fatalf("expiry selection: %v %v", paths, err)
	}
	if err := files.removeExpired(paths); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pending.json", "aliased.json"} {
		if _, err := os.Stat(filepath.Join(files.directory, name)); err != nil {
			t.Fatalf("pending reference lost: %s %v", name, err)
		}
	}
	actual := files.directory + ".saved"
	if err := os.Rename(files.directory, actual); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, files.directory); err != nil {
		t.Fatal(err)
	}
	if _, err := files.expired(before, nil); err == nil {
		t.Fatal("symlinked recovery directory accepted")
	}
}

type pendingReadCatalog struct {
	store.CatalogReader
	pending domain.PendingPublish
}

func (c pendingReadCatalog) GetPendingPublish(context.Context, string) (*domain.PendingPublish, error) {
	copy := c.pending
	return &copy, nil
}

func TestAuthoringReadsPendingPlanWithoutWriterCapability(t *testing.T) {
	files := deliveryFiles{directory: filepath.Join(t.TempDir(), "plans")}
	target := config.PublisherConfig{ID: "memory", Kind: "memory", Enabled: true}
	plan := deliveryPlan{ID: "delivery_reader", EventScope: "stable_scope", Target: target, Candidates: []domain.PublishCandidate{deliveryCandidate("read", "digest")}}
	path, err := files.write(plan)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	authoring := NewAuthoring(cfg, pendingReadCatalog{pending: domain.PendingPublish{ID: plan.ID, TargetID: target.ID, PayloadRef: path}}, nil)
	loaded, err := authoring.pendingDelivery(context.Background(), cfg, deliveryScope{}, target)
	if err != nil || loaded == nil || loaded.PayloadRef != path || loaded.EventScope != plan.EventScope {
		t.Fatalf("read-only pending plan: %+v %v", loaded, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("pending inspection changed saved plan: %v", err)
	}
	entries, err := os.ReadDir(files.directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("pending inspection wrote extra files: %v %v", entries, err)
	}
}
