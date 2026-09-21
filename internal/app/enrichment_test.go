package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/provider/patreon"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

func TestIdentityEnrichmentDoesNotChangeContentHash(t *testing.T) {
	release := domain.NormalizedRelease{Provider: "patreon", ProviderReleaseID: "1", Title: "Harbor", Collections: []string{"Harbor"}}
	before, _ := json.Marshal(hashableNormalizedRelease(release))
	release.Enrichment = &domain.ReleaseEnrichment{NormalizerVersion: domain.NormalizerVersion, Collections: []domain.LabelReference{{Provider: "patreon", Campaign: "fictional", Type: "collection", ID: "1", Names: []string{"Harbor"}}}}
	after, _ := json.Marshal(hashableNormalizedRelease(release))
	if string(before) != string(after) {
		t.Fatal("adding identity enrichment changed content hash input")
	}
}

func TestOfflineEnrichmentUsesLatestObservationForTheSameCapture(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := sqlite.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	s := Service{Repo: repo, Providers: provider.NewRegistry(patreon.New())}
	normalized := domain.NormalizedRelease{Provider: "patreon", ProviderReleaseID: "post-1", Title: "Harbor", Collections: []string{"Harbor"}}
	release := domain.Release{SourceID: "fictional", ProviderReleaseID: "post-1", NormalizedPayloadRef: filepath.Join(root, "normalized.json"), RawPayloadRef: filepath.Join(root, "raw.json")}
	raw := []byte(`{"data":{"id":"post-1","relationships":{"campaign":{"data":{"id":"fictional"}},"collections":{"data":[{"id":"old"}]}}}}`)
	if err := os.WriteFile(release.RawPayloadRef, raw, 0600); err != nil {
		t.Fatal(err)
	}
	write := func() {
		t.Helper()
		data, _ := json.Marshal(normalized)
		if err := os.WriteFile(release.NormalizedPayloadRef, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	got, err := s.loadStoredNormalized(ctx, release)
	if err != nil || got.Enrichment.Collections[0].ID != "old" {
		t.Fatalf("legacy raw enrichment: %+v %v", got, err)
	}
	normalized.Enrichment = got.Enrichment
	write()
	observed := normalized
	metadata := *normalized.Enrichment
	metadata.Collections = []domain.LabelReference{{Provider: "patreon", Campaign: "fictional", Type: "collection", ID: "new", Names: []string{"Harbor"}}}
	observed.Enrichment = &metadata
	if err := s.saveEnrichment(ctx, "fictional", observed); err != nil {
		t.Fatal(err)
	}
	got, err = s.loadStoredNormalized(ctx, release)
	if err != nil || got.Enrichment.Collections[0].ID != "new" {
		t.Fatalf("stale embedded enrichment won: %+v %v", got, err)
	}
	observed.Title = "A newer unsaved post revision"
	observed.Enrichment.Collections[0].ID = "future"
	if err := s.saveEnrichment(ctx, "fictional", observed); err != nil {
		t.Fatal(err)
	}
	got, err = s.loadStoredNormalized(ctx, release)
	if err != nil || got.Enrichment.Collections[0].ID != "new" {
		t.Fatalf("enrichment crossed captured versions: %+v %v", got, err)
	}
}

func TestWorkspaceRejectsReleaseIDTraversalBeforeRawEnrichment(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "posts.ndjson")
	data := []byte(`{"normalized":{"provider":"patreon","provider_release_id":"../../outside","title":"Harbor"}}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWorkspacePosts(root, "", path); err == nil {
		t.Fatal("workspace accepted a release ID outside the capture")
	}
}
