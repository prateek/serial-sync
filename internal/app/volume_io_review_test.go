//go:build linux || darwin

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func volumeReadSentinel(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "unneeded-reading-copy")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int64
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0600)
			if err != nil {
				continue
			}
			reads.Add(1)
			_, _ = syscall.Write(fd, []byte("GIF89a unused captured bytes"))
			_ = syscall.Close(fd)
		}
	}()
	t.Cleanup(func() { close(stop); <-stopped })
	return path, &reads
}

func TestVolumeAssessmentAvoidsUnneededFilesystemReads(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		seriesFilter, sourceFilter             string
		singles, rebuild, existing, incomplete bool
		wantPlans                              int
		wantStatus                             string
		wantPublication                        int
	}{
		{name: "excluded-series", seriesFilter: "other"},
		{name: "excluded-source", sourceFilter: "other"},
		{name: "singles-only", singles: true},
		{name: "singles-retirement", singles: true, rebuild: true, existing: true, wantPlans: 1, wantStatus: "singles"},
		{name: "incomplete-range", incomplete: true, wantPlans: 1, wantStatus: "open"},
		{name: "selected-volume", wantPlans: 1, wantStatus: "complete", wantPublication: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chapterPath, chapterReads := volumeReadSentinel(t)
			assetPath, assetReads := volumeReadSentinel(t)
			if tc.wantPublication > 0 {
				assetPath = filepath.Join(t.TempDir(), "cover.gif")
				if err := os.WriteFile(assetPath, []byte("GIF89a selected cover"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			output := config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
			if tc.singles {
				output.Bundling = "none"
			}
			series := config.SeriesConfig{ID: "harbor", Title: "Harbor", Output: output}
			cfg := &config.Config{Series: []config.SeriesConfig{series}, Sources: []config.SourceConfig{{ID: "fictional", Enabled: true}}}
			first, seq1 := planChapter(t, series.ID, "r1", 1, 1)
			second, seq2 := planChapter(t, series.ID, "r2", 2, 2)
			first.Artifact.StorageRef, second.Artifact.StorageRef = chapterPath, chapterPath
			candidates := []domain.PublishCandidate{first, second}
			if tc.incomplete {
				candidates = candidates[:1]
			}
			request := volumeEvaluation{config: cfg, series: cfg.Series, seriesFilter: tc.seriesFilter, sourceFilter: tc.sourceFilter, rebuild: tc.rebuild, candidates: candidates, blockedSeries: map[string]bool{},
				inputs:    map[string]domain.NormalizedRelease{"r1": {ProviderReleaseID: "r1"}, "r2": {ProviderReleaseID: "r2"}},
				histories: map[string]map[string]classify.ExplainedDecision{"fictional": {"r1": {Decision: domain.TrackDecision{Sequence: &seq1}}, "r2": {Decision: domain.TrackDecision{Sequence: &seq2}}}},
			}
			if tc.existing {
				request.existing = []domain.VolumeEdition{{ID: "prior", SeriesID: series.ID, SourceID: "fictional", GroupID: "range:1:2", Active: true, Members: []domain.VolumeMember{{ReleaseID: "r1", Position: 1}, {ReleaseID: "r2", Position: 2}}}}
			}
			publicationReads := 0
			request.publication = func(string, domain.NormalizedRelease, domain.TrackDecision) (*domain.PublicationMetadata, error) {
				publicationReads++
				_, err := os.ReadFile(assetPath)
				return &domain.PublicationMetadata{Cover: &domain.MetadataAsset{Path: assetPath, SHA256: "selected-cover"}}, err
			}
			reader := &catalogReader{}
			var plans []domain.VolumePlan
			var editions []domain.VolumeEdition
			done := make(chan error, 1)
			go func() {
				var err error
				plans, editions, err = reader.evaluateVolumes(context.Background(), request, nil)
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("assessment blocked on an unnecessary filesystem read")
			}
			if chapterReads.Load() != 0 || assetReads.Load() != 0 {
				t.Fatalf("assessment opened unused bytes: chapters=%d assets=%d", chapterReads.Load(), assetReads.Load())
			}
			if publicationReads != tc.wantPublication {
				t.Fatalf("publication reads=%d want=%d", publicationReads, tc.wantPublication)
			}
			if len(plans) != tc.wantPlans || tc.wantPlans > 0 && plans[0].Status != tc.wantStatus {
				t.Fatalf("plans=%+v", plans)
			}
			if tc.existing && len(editions) != 0 {
				t.Fatalf("disabled-bundling rebuild retained editions: %+v", editions)
			}
		})
	}
}

func TestVolumeAssessmentDoesNotHashReadingCopies(t *testing.T) {
	if os.Getenv("SERIAL_SYNC_VOLUME_NOHASH_CHILD") != "1" {
		root := t.TempDir()
		path := filepath.Join(root, "unneeded-sparse-epub")
		// Hashing rejects FIFOs before opening them. A sparse regular file makes
		// accidental hashing observable, and the child timeout bounds that work.
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(1 << 40); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestVolumeAssessmentDoesNotHashReadingCopies$")
		command.Env = append(os.Environ(), "SERIAL_SYNC_VOLUME_NOHASH_CHILD=1", "SERIAL_SYNC_VOLUME_SPARSE_REF="+path, "TMPDIR="+root)
		data, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("assessment read the sparse chapter instead of sidecar-only facts: %v %s", err, data)
		}
		return
	}

	path := os.Getenv("SERIAL_SYNC_VOLUME_SPARSE_REF")
	sidecar := path + ".metadata.json"
	if err := os.WriteFile(sidecar, []byte(`{"output_version":6,"decision":{"output_format":"epub"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"excluded-series", "excluded-source", "singles-only", "selected-volume"} {
		t.Run(scenario, func(t *testing.T) {
			series := config.SeriesConfig{ID: "harbor", Title: "Harbor", Output: config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 1}}
			if scenario == "singles-only" {
				series.Output.Bundling = "none"
			}
			cfg := &config.Config{Series: []config.SeriesConfig{series}, Sources: []config.SourceConfig{{ID: "fictional", Enabled: true}}}
			candidate, seq := planChapter(t, series.ID, "r1", 1, 1)
			candidate.Artifact.StorageRef, candidate.Artifact.MetadataRef = path, sidecar
			request := volumeEvaluation{config: cfg, series: cfg.Series, candidates: []domain.PublishCandidate{candidate}, blockedSeries: map[string]bool{},
				inputs:    map[string]domain.NormalizedRelease{"r1": {ProviderReleaseID: "r1"}},
				histories: map[string]map[string]classify.ExplainedDecision{"fictional": {"r1": {Decision: domain.TrackDecision{Sequence: &seq}}}},
				publication: func(string, domain.NormalizedRelease, domain.TrackDecision) (*domain.PublicationMetadata, error) {
					return nil, nil
				},
			}
			if scenario == "excluded-series" {
				request.seriesFilter = "other"
			}
			if scenario == "excluded-source" {
				request.sourceFilter = "other"
			}
			plans, _, err := (&catalogReader{}).evaluateVolumes(context.Background(), request, nil)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "selected-volume" {
				if len(plans) != 1 || plans[0].Status != "complete" {
					t.Fatalf("selected volume assessment: %+v", plans)
				}
			} else if len(plans) != 0 {
				t.Fatalf("skipped assessment planned output: %+v", plans)
			}
		})
	}
}
