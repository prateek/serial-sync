package artifact

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

func TestReviewRetainedArtifactMustKeepCapturedMetadata(t *testing.T) {
	m := New(t.TempDir())
	source := domain.Source{ID: "fixture"}
	track := domain.StoryTrack{ID: "track", TrackKey: "harbor", TrackName: "Harbor"}
	release := domain.Release{ID: "release", ProviderReleaseID: "1", Title: "Chapter 1", ContentHash: "unchanged"}
	normalized := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Chapter 1", TextHTML: "<p>Story.</p>"}
	decision := domain.TrackDecision{ContentStrategy: domain.ContentStrategyTextPost, OutputFormat: domain.OutputFormatPreserve, Matched: true, Publication: &domain.PublicationMetadata{Description: "original capture"}}
	firstPlan, err := m.Plan(context.Background(), source, track, release, normalized, decision, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Materialize(context.Background(), source, track, release, firstPlan)
	if err != nil {
		t.Fatal(err)
	}
	decision.Publication = &domain.PublicationMetadata{Description: "later capture"}
	secondPlan, err := m.Plan(context.Background(), source, track, release, normalized, decision, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Materialize(context.Background(), source, track, release, secondPlan)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.SHA256 != second.SHA256 {
		t.Fatal("fixture must represent distinct artifacts with same reading bytes")
	}
	stored, err := StoredPublication(first)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Description != "original capture" {
		t.Fatalf("retained artifact %s now reads %q after second materialization; metadata paths equal=%t", first.ID, stored.Description, first.MetadataRef == second.MetadataRef)
	}
}

func captureFixture(t *testing.T) (*Materializer, domain.Source, domain.StoryTrack, domain.Release, domain.ArtifactPlan) {
	t.Helper()
	m := New(t.TempDir())
	source := domain.Source{ID: "source", Enabled: true}
	track := domain.StoryTrack{ID: "track", SourceID: source.ID, TrackKey: "harbor", TrackName: "Harbor"}
	release := domain.Release{ID: "release", SourceID: source.ID, ProviderReleaseID: "1", Title: "Chapter 1", ContentHash: "content"}
	normalized := domain.NormalizedRelease{ProviderReleaseID: "1", Title: release.Title, TextHTML: "<p>Story.</p>"}
	decision := domain.TrackDecision{ContentStrategy: domain.ContentStrategyTextPost, OutputFormat: domain.OutputFormatPreserve, Publication: &domain.PublicationMetadata{Description: "original"}}
	plan, err := m.Plan(context.Background(), source, track, release, normalized, decision, []byte(`{"capture":1}`))
	if err != nil {
		t.Fatal(err)
	}
	return m, source, track, release, plan
}

func TestCaptureRetriesReuseIdentityAndReadingBlob(t *testing.T) {
	m, source, track, release, plan := captureFixture(t)
	first, err := m.Materialize(context.Background(), source, track, release, plan)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(first.StorageRef)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(123456789, 0)
	if err := os.Chtimes(first.StorageRef, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	again, err := m.Materialize(context.Background(), source, track, release, plan)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(again.StorageRef)
	if err != nil {
		t.Fatal(err)
	}
	if first != again || !os.SameFile(before, after) || !after.ModTime().Equal(stamp) {
		t.Fatalf("retry changed capture or rewrote reading blob: first=%+v again=%+v", first, again)
	}
	plan.RawJSON = []byte(`{"capture":2}`)
	changed, err := m.Materialize(context.Background(), source, track, release, plan)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID == first.ID || changed.MetadataRef == first.MetadataRef || changed.StorageRef != first.StorageRef {
		t.Fatalf("new provenance must retain a separate capture and reuse reading bytes: %+v", changed)
	}
}

func TestCaptureSidecarsAreVerifiedAndExactRetryRepairs(t *testing.T) {
	for _, name := range []string{"metadata.json", "normalized.json", "raw.json", "manifest.json"} {
		for _, corrupt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/corrupt=%t", name, corrupt), func(t *testing.T) {
				m, source, track, release, plan := captureFixture(t)
				art, err := m.Materialize(context.Background(), source, track, release, plan)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(filepath.Dir(art.MetadataRef), name)
				if corrupt {
					err = os.WriteFile(path, []byte(`{"valid_json":"wrong capture"}`), 0644)
				} else {
					err = os.Remove(path)
				}
				if err != nil {
					t.Fatal(err)
				}
				if state := Inspect(art); !state.Blocked || state.Publication != nil {
					t.Fatalf("damaged capture exposed metadata: %+v", state)
				}
				if IsCurrent(art, release, track, domain.TrackDecision{}) {
					t.Fatal("damaged capture is current")
				}
				if _, err := StoredPublication(art); err == nil {
					t.Fatal("damaged capture returned publication")
				}
				repaired, err := m.Materialize(context.Background(), source, track, release, plan)
				if err != nil {
					t.Fatal(err)
				}
				if repaired.ID != art.ID || Inspect(repaired).Blocked {
					t.Fatal("retry did not restore original capture")
				}
				pub, err := StoredPublication(art)
				if err != nil || pub.Description != "original" {
					t.Fatalf("retained reference not repaired: %+v %v", pub, err)
				}
			})
		}
	}
}

func TestCaptureRetryAfterInterruptedInstallation(t *testing.T) {
	m, source, track, release, plan := captureFixture(t)
	// Fail after blob installation, before the capture is visible.
	if err := os.MkdirAll(m.Root, 0755); err != nil {
		t.Fatal(err)
	}
	obstruction := filepath.Join(m.Root, "captures")
	if err := os.WriteFile(obstruction, []byte("blocked"), 0644); err != nil {
		t.Fatal(err)
	}
	if art, err := m.Materialize(context.Background(), source, track, release, plan); err == nil || art.ID != "" {
		t.Fatal("incomplete installation returned a reference")
	}
	blob := filepath.Join(m.Root, "blobs", "sha256", plan.SHA256)
	before, err := os.Stat(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(obstruction); err != nil {
		t.Fatal(err)
	}
	// Model a process dying before the staging directory is installed.
	staging := filepath.Join(obstruction, ".capture-abandoned")
	if err := os.MkdirAll(staging, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "metadata.json"), []byte("partial"), 0644); err != nil {
		t.Fatal(err)
	}
	art, err := m.Materialize(context.Background(), source, track, release, plan)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(art.StorageRef)
	if err != nil || !os.SameFile(before, after) || Inspect(art).Blocked {
		t.Fatalf("retry did not finish using installed blob: %v", err)
	}
}

func TestCatalogRetainsCapturesWithIdenticalReadingBytes(t *testing.T) {
	m, source, track, release, plan := captureFixture(t)
	ctx := context.Background()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	save := func(plan domain.ArtifactPlan) domain.Artifact {
		t.Helper()
		art, err := m.Materialize(ctx, source, track, release, plan)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.SaveSyncSnapshot(ctx, store.SyncSnapshot{Source: source, Track: track, Release: release, Assignment: domain.ReleaseAssignment{ReleaseID: release.ID, TrackID: track.ID}, Artifact: art}); err != nil {
			t.Fatal(err)
		}
		return art
	}
	first := save(plan)
	if err := repo.UpsertPublishRecord(ctx, domain.PublishRecord{ID: "delivery-A", ArtifactID: first.ID, TargetID: "library", PublishHash: "same-reading-bytes", Status: domain.PublishStatusPublished}); err != nil {
		t.Fatal(err)
	}
	var meta canonicalSidecar
	if err := json.Unmarshal(plan.MetadataJSON, &meta); err != nil {
		t.Fatal(err)
	}
	meta.Decision.Publication.Description = "later capture"
	plan.MetadataJSON, err = json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	second := save(plan)
	again := save(plan)
	if second.ID != again.ID {
		t.Fatal("catalog retry changed identity")
	}
	bundle, err := repo.GetReleaseBundle(ctx, release.ID)
	if err != nil || len(bundle.Artifacts) != 2 {
		t.Fatalf("catalog collapsed retained captures: %+v %v", bundle, err)
	}
	for _, want := range []struct {
		art         domain.Artifact
		description string
		canonical   bool
	}{{first, "original", false}, {second, "later capture", true}} {
		got, err := repo.GetArtifact(ctx, want.art.ID)
		if err != nil || got == nil {
			t.Fatalf("retained catalog reference lost: %s %v", want.art.ID, err)
		}
		pub, err := StoredPublication(*got)
		if err != nil || pub.Description != want.description || got.IsCanonical != want.canonical {
			t.Fatalf("capture changed: %+v %+v %v", got, pub, err)
		}
	}
	records, err := repo.ListPublishRecords(ctx, source.ID, "library")
	if err != nil || len(records) != 1 || records[0].Artifact.ID != first.ID {
		t.Fatalf("retained delivery reference changed: %+v %v", records, err)
	}
	if first.StorageRef != second.StorageRef || first.SHA256 != second.SHA256 {
		t.Fatal("same reading bytes were not reused")
	}
}

func TestLegacyCaptureRemainsReadableAlongsideNewCapture(t *testing.T) {
	m, source, track, release, plan := captureFixture(t)
	legacyDir := filepath.Join(m.Root, source.ID, track.TrackKey, release.ProviderReleaseID, plan.SHA256)
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatal(err)
	}
	legacy := domain.Artifact{ID: "art_legacy", ReleaseID: release.ID, TrackID: track.ID, Filename: plan.Filename, SHA256: plan.SHA256,
		StorageRef: filepath.Join(legacyDir, plan.Filename), MetadataRef: filepath.Join(legacyDir, plan.Filename+".metadata.json")}
	for path, data := range map[string][]byte{legacy.StorageRef: plan.SelectedContent, legacy.MetadataRef: plan.MetadataJSON} {
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.Stat(legacy.MetadataRef)
	if err != nil {
		t.Fatal(err)
	}
	plan.RawJSON = []byte(`{"later":true}`)
	if _, err := m.Materialize(context.Background(), source, track, release, plan); err != nil {
		t.Fatal(err)
	}
	pub, err := StoredPublication(legacy)
	if err != nil || pub.Description != "original" || Inspect(legacy).Blocked || !Inspect(legacy).Intact {
		t.Fatalf("legacy capture no longer readable: %+v %v", pub, err)
	}
	after, err := os.Stat(legacy.MetadataRef)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("legacy sidecar rewritten: %v", err)
	}
}

func TestCaptureRejectsBorrowedSidecarsAndMismatchedBytes(t *testing.T) {
	m, source, track, release, plan := captureFixture(t)
	first, err := m.Materialize(context.Background(), source, track, release, plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.RawJSON = []byte(`{"different":true}`)
	second, err := m.Materialize(context.Background(), source, track, release, plan)
	if err != nil {
		t.Fatal(err)
	}
	borrowed := first
	borrowed.MetadataRef, borrowed.NormalizedRef, borrowed.RawRef = second.MetadataRef, second.NormalizedRef, second.RawRef
	if state := Inspect(borrowed); !state.Blocked {
		t.Fatalf("reference silently borrowed another capture: %+v", state)
	}
	plan.SelectedContent = []byte("wrong reading bytes")
	if art, err := m.Materialize(context.Background(), source, track, release, plan); err == nil || art.ID != "" {
		t.Fatal("mismatched bytes installed")
	}
}

func TestSharedCapturedArtworkIsVerifiedAndRepaired(t *testing.T) {
	m, source, track, release, _ := captureFixture(t)
	asset := testMetadataAsset(t)
	normalized := domain.NormalizedRelease{ProviderReleaseID: "1", Title: release.Title, TextHTML: "<p>Story.</p>"}
	decision := domain.TrackDecision{ContentStrategy: domain.ContentStrategyTextPost, OutputFormat: domain.OutputFormatPreserve, Publication: &domain.PublicationMetadata{Cover: asset}}
	plan, err := m.Plan(context.Background(), source, track, release, normalized, decision, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Materialize(context.Background(), source, track, release, plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.RawJSON = []byte(`{"later":true}`)
	second, err := m.Materialize(context.Background(), source, track, release, plan)
	if err != nil {
		t.Fatal(err)
	}
	for path := range plan.MetadataAssets {
		if err := os.WriteFile(path, []byte("corrupt cover"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, art := range []domain.Artifact{first, second} {
		if !Inspect(art).Blocked {
			t.Fatal("capture accepted damaged shared artwork")
		}
	}
	if _, err := m.Materialize(context.Background(), source, track, release, plan); err != nil {
		t.Fatal(err)
	}
	for _, art := range []domain.Artifact{first, second} {
		if Inspect(art).Blocked {
			t.Fatal("retry did not restore shared artwork for retained captures")
		}
	}
}
