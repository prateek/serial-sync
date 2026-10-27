package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestReviewVolumeRebuildMustNoticeChapterEditionChange(t *testing.T) {
	series := config.SeriesConfig{ID: "harbor", Title: "Harbor", Source: "fictional"}
	output := config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 1}
	candidate, seq := planChapter(t, series.ID, "r1", 1, 1)
	candidate.Artifact.SHA256 = "old-reading-copy-hash"
	candidates := []domain.PublishCandidate{candidate}
	request := planRequest(series, output, false, candidates, nil, map[string]domain.Sequence{"r1": seq})
	initial, err := planSeriesVolumes(series, output, request)
	if err != nil || len(initial.Editions) != 1 {
		t.Fatalf("initial: %+v %v", initial, err)
	}
	prior := initial.Editions[0]
	prior.Artifact.ID = "stored-volume"
	request.existing = []domain.VolumeEdition{prior}
	request.archiveValid[prior.Artifact.ID] = true
	request.rebuild = true
	candidates[0].Artifact.ID = "relocated-reading-copy"
	candidates[0].Artifact.StorageRef = "/relocated.epub"
	result, err := planSeriesVolumes(series, output, request)
	if err != nil || len(result.Builds) != 0 || result.Plans[0].Status != "frozen" {
		t.Fatalf("capture location changed edition identity: %+v %v", result, err)
	}
	candidates[0].Artifact.SHA256 = "new-reading-copy-hash"
	candidates[0].Artifact.ID = "new-reading-copy-id"
	candidates[0].Artifact.StorageRef = "/new-reading-copy.epub"
	request.rebuild = false
	result, err = planSeriesVolumes(series, output, request)
	if err != nil || len(result.Builds) != 0 || result.Plans[0].Status != "pending_rebuild" {
		t.Fatalf("ordinary sync replaced changed edition: %+v %v", result, err)
	}
	request.rebuild = true
	result, err = planSeriesVolumes(series, output, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Builds) == 0 {
		t.Fatalf("chapter reading-copy bytes changed but explicit rebuild retained volume: plans=%+v", result.Plans)
	}
}

func TestReviewPlannerDependsOnCapturedSidecarFacts(t *testing.T) {
	// The original probe changed plans by editing the sidecar between identical planner calls.
	series := config.SeriesConfig{ID: "harbor", Title: "Harbor", Source: "fictional"}
	output := config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 1}
	candidate, seq := planChapter(t, series.ID, "r1", 1, 1)
	sidecar := filepath.Join(t.TempDir(), "metadata.json")
	candidate.Artifact.MetadataRef = sidecar
	write := func(version int) {
		b, _ := json.Marshal(map[string]any{"output_version": version, "decision": domain.TrackDecision{OutputFormat: domain.OutputFormatEPUB}})
		if err := os.WriteFile(sidecar, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(6)
	request := planRequest(series, output, false, []domain.PublishCandidate{candidate}, nil, map[string]domain.Sequence{"r1": seq})
	first, err := planSeriesVolumes(series, output, request)
	if err != nil {
		t.Fatal(err)
	}
	write(1)
	second, err := planSeriesVolumes(series, output, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first.Builds) != 1 {
		t.Fatalf("captured planner changed with filesystem: %+v %+v", first, second)
	}
	refreshed := planRequest(series, output, false, []domain.PublishCandidate{candidate}, nil, map[string]domain.Sequence{"r1": seq})
	third, err := planSeriesVolumes(series, output, refreshed)
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Builds) != 0 || third.Plans[0].Status != "pending_rebuild" {
		t.Fatalf("capture missed legacy sidecar: %+v", third)
	}
}

func TestVolumePlanningUsesCapturedPublicationAssets(t *testing.T) {
	series := config.SeriesConfig{ID: "harbor", Title: "Harbor", Source: "fictional"}
	output := config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 1}
	candidate, seq := planChapter(t, series.ID, "r1", 1, 1)
	cover := filepath.Join(t.TempDir(), "cover.gif")
	if err := os.WriteFile(cover, []byte("GIF89a captured artwork"), 0600); err != nil {
		t.Fatal(err)
	}
	series.Metadata.Cover.Path = cover
	request := volumeEvaluation{
		config: &config.Config{}, candidates: []domain.PublishCandidate{candidate},
		scope: map[string]bool{"fictional": true}, archiveValid: map[string]bool{},
		inputs:    map[string]domain.NormalizedRelease{"r1": {ProviderReleaseID: "r1"}},
		histories: map[string]map[string]classify.ExplainedDecision{"fictional": {"r1": {Decision: domain.TrackDecision{Sequence: &seq}}}},
		publication: func(string, domain.NormalizedRelease, domain.TrackDecision) (*domain.PublicationMetadata, error) {
			asset, err := publicationAsset(config.AssetConfig{Path: cover})
			return &domain.PublicationMetadata{Cover: asset}, err
		},
	}
	facts := captureVolumeFacts(series, output, request)
	if err := os.Remove(cover); err != nil {
		t.Fatal(err)
	}
	first, err := planSeriesVolumes(series, output, facts)
	if err != nil || len(first.Editions) != 1 {
		t.Fatalf("planning reread captured artwork: %+v %v", first, err)
	}
	prior := first.Editions[0]
	facts.existing = []domain.VolumeEdition{prior}
	facts.archiveValid[prior.Artifact.ID] = true
	facts.rebuild = true
	// The selected asset bytes survive relocation; configuration and capture paths
	// cannot turn that relocation into a new edition.
	series.Metadata.Cover.Path = "/relocated/config-cover.gif"
	chapter := facts.chapters["r1"]
	chapter.publication.Cover.Path = "/relocated/captured-cover.gif"
	facts.chapters["r1"] = chapter
	second, err := planSeriesVolumes(series, output, facts)
	if err != nil || len(second.Builds) != 0 || second.Plans[0].Status != "frozen" {
		t.Fatalf("asset relocation changed edition identity: %+v %v", second, err)
	}
}
