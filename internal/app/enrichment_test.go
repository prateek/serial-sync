package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/prateek/serial-sync/internal/config"
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

func TestWorkspacePreviewUpgradesCapturedMetadataOffline(t *testing.T) {
	for _, version := range []int{2, 3, domain.NormalizerVersion} {
		root := t.TempDir()
		captured := map[string][]byte{}
		write := func(name string, value any) {
			t.Helper()
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			captured[path] = data
		}
		write("manifest.json", dumpManifest{Version: 3, SourcesFile: "sources.toml", SeriesFile: "series.toml", Creators: []SourceDumpCreator{{SourceID: "author", PostsFile: "posts.ndjson", RawPostsDir: "raw"}}})
		sources := []byte("[[sources]]\nid='author'\nprovider='patreon'\nurl='https://www.patreon.com/c/author/posts'\nenabled=true\n")
		if err := os.WriteFile(filepath.Join(root, "sources.toml"), sources, 0600); err != nil {
			t.Fatal(err)
		}
		enrichment := &domain.ReleaseEnrichment{NormalizerVersion: version}
		want := "Ada"
		if version == domain.NormalizerVersion {
			enrichment.Author = &domain.AuthorProfile{ID: "patreon:user:creator", Name: "Current capture"}
			want = "Current capture"
		}
		write("posts.ndjson", dumpPostRecord{Normalized: domain.NormalizedRelease{Provider: "patreon", ProviderReleaseID: "post", Title: "Harbor Chapter 1", TextPlain: "Story text.", Enrichment: enrichment}})
		write("raw/post.json", json.RawMessage(`{"data":{"id":"post","relationships":{"user":{"data":{"id":"creator"}}}},"included":[{"type":"user","id":"creator","attributes":{"full_name":"Ada","about":"Biography"}}]}`))
		s := Service{Config: &config.Config{Sources: []config.SourceConfig{{ID: "author", Enabled: true}}, Rules: []config.RuleConfig{{Source: "author", MatchType: "fallback", TrackKey: "harbor", ContentStrategy: "text_post", OutputFormat: "epub"}}}, Providers: provider.NewRegistry(patreon.New())}
		preview, err := s.PreviewRules(context.Background(), RulesPreviewOptions{WorkspacePath: root, ShowPosts: true}, "preview")
		if err != nil {
			t.Fatal(err)
		}
		metadata := preview.Creators[0].Preview.Posts[0].Decision.Publication
		if metadata == nil || len(metadata.Authors) != 1 || metadata.Authors[0].Name != want {
			t.Fatalf("version %d preview metadata = %+v, want author %q", version, metadata, want)
		}
		for path, before := range captured {
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("preview changed captured file %s: %v", path, err)
			}
		}
	}
}
