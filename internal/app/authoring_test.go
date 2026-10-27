package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
)

type editingEnricher struct{ edit func() }

func (e editingEnricher) EnrichCaptured(release domain.NormalizedRelease, raw []byte) (domain.NormalizedRelease, error) {
	e.edit()
	return release, nil
}

func TestAuthoringBindsTheConfigBytesItEvaluated(t *testing.T) {
	root := t.TempDir()
	sources := `[[sources]]
id="alpha"
provider="patreon"
url="https://example.invalid/before"
enabled=true
`
	series := `[[series]]
id="harbor"
title="Before"
[[series.inputs]]
source="alpha"
match_type="fallback"
release_role="extra"
content_strategy="text_post"
min_body_chars=0
`
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", `{"version":3,"provider":"patreon","sources_file":"sources.toml","series_file":"series.toml","creators":[{"source_id":"alpha","posts_file":"posts.ndjson","raw_posts_dir":"raw"}]}`)
	write("sources.toml", sources)
	write("series.toml", series)
	write("posts.ndjson", `{"normalized":{"provider":"patreon","provider_release_id":"1","title":"Harbor","text_html":"<p>body</p>","text_plain":"body"}}`)
	write("raw/1.json", `{}`)
	edited := false
	authoring := NewAuthoring(&config.Config{}, nil, editingEnricher{edit: func() {
		edited = true
		write("sources.toml", strings.ReplaceAll(sources, "before", "after"))
		write("series.toml", strings.ReplaceAll(series, "Before", "After"))
	}})
	got, err := authoring.Preview(context.Background(), RulesPreviewOptions{WorkspacePath: root, SeriesFile: "series.toml", ShowPosts: true})
	if err != nil {
		t.Fatal(err)
	}
	if !edited {
		t.Fatal("edit did not occur between loading and reporting")
	}
	if got.SeriesFileHash != hashBytes([]byte(series)) || got.SourcesFileHash != hashBytes([]byte(sources)) {
		t.Fatal("provenance describes different config bytes than evaluation")
	}
	if len(got.Creators) != 1 || got.Creators[0].Preview.Posts[0].TrackName != "Before" {
		t.Fatalf("evaluation did not retain captured config: %+v", got.Creators)
	}
}

func TestAuthoringRejectsMissingConfig(t *testing.T) {
	if _, err := NewAuthoring(nil, nil, nil).Preview(context.Background(), RulesPreviewOptions{}); err == nil {
		t.Fatal("missing config accepted")
	}
}

type sharedTrackCatalog struct {
	store.CatalogReader
	track *domain.StoryTrack
}

func (s sharedTrackCatalog) GetTrackBySourceAndKey(context.Context, string, string) (*domain.StoryTrack, error) {
	return s.track, nil
}

func TestCatalogReaderDoesNotChangeBorrowedTrack(t *testing.T) {
	original := domain.StoryTrack{ID: "track", TrackKey: "harbor", TrackName: "Before", CanonicalAuthor: "Before"}
	borrowed := original
	reader := &catalogReader{catalog: sharedTrackCatalog{track: &borrowed}}
	got, err := reader.resolveTrack(context.Background(), domain.Source{ID: "alpha"}, domain.TrackDecision{TrackKey: "harbor", TrackName: "After", CanonicalAuthor: "After"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != original.ID || got.TrackName != "After" {
		t.Fatalf("track projection: %+v", got)
	}
	if borrowed != original {
		t.Fatalf("read-only assessment changed borrowed catalog fact: %+v", borrowed)
	}
}

type failedMemberCatalog struct{ store.CatalogReader }

func (failedMemberCatalog) GetReleaseBundle(context.Context, string) (*domain.ReleaseBundle, error) {
	return nil, fmt.Errorf("member catalog unavailable")
}

func TestVolumeReadFailureRetainsActiveProjection(t *testing.T) {
	active := domain.VolumeEdition{ID: "volume", SeriesID: "harbor", SourceID: "alpha", GroupID: "range:1:2", Active: true, Members: []domain.VolumeMember{{ReleaseID: "missing"}}}
	reader := &catalogReader{catalog: failedMemberCatalog{}}
	_, editions, err := reader.evaluateVolumes(context.Background(), volumeEvaluation{config: &config.Config{}, existing: []domain.VolumeEdition{active}, rebuild: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "member catalog unavailable") {
		t.Fatalf("missing read error: %v", err)
	}
	if len(editions) != 1 || editions[0].ID != active.ID {
		t.Fatalf("catalog read failure lost active projection and could plan singles/retirements: %+v", editions)
	}
}

func TestEmptyAuthoringLibraryScopeRetainsAllDeliveredFiles(t *testing.T) {
	cfg := &config.Config{Sources: []config.SourceConfig{{ID: "alpha", Enabled: true}}, Publishers: []config.PublisherConfig{{ID: "library", Enabled: true}}}
	for _, scope := range [][]string{nil, {}} {
		result := (&Authoring{catalogReader: &catalogReader{}}).appliedLibraryPlan(context.Background(), cfg, scope)
		if len(result.Actions) != 0 || len(result.Notices) == 0 || len(result.Blocked) != 0 {
			t.Fatalf("empty scope should retain files without reading catalog: %+v", result)
		}
	}
	if !cfg.Sources[0].Enabled {
		t.Fatal("scoping mutated caller config")
	}
}

type emptyTrackCatalog struct{ store.CatalogReader }

func (emptyTrackCatalog) GetTrackBySourceAndKey(context.Context, string, string) (*domain.StoryTrack, error) {
	return nil, nil
}

func TestCatalogReaderKeepsDeterministicTrackIdentity(t *testing.T) {
	reader := &catalogReader{catalog: emptyTrackCatalog{}}
	resolve := func(source, key string) domain.StoryTrack {
		t.Helper()
		got, err := reader.resolveTrack(context.Background(), domain.Source{ID: source}, domain.TrackDecision{TrackKey: key, TrackName: key})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	first, again := resolve("alpha", "harbor"), resolve("alpha", "harbor")
	if first.ID != again.ID || first.ID == "" {
		t.Fatalf("same catalog key changed track identity: %q / %q", first.ID, again.ID)
	}
	if resolve("beta", "harbor").ID == first.ID || resolve("alpha", "other").ID == first.ID {
		t.Fatal("distinct catalog keys share track identity")
	}
}
