package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func TestVolumeRebuildFollowsChapterPresentationEdition(t *testing.T) {
	s, _ := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	ctx := context.Background()
	if _, err := s.RunOnce(ctx, "", "", "initial"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	before := mustReadFile(t, path)
	chapters, err := s.Repo.ListPublishCandidates(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate chapters made by an older presentation recipe, without changing
	// the release content or volume config. Explicit rebuild must refresh them.
	for _, chapter := range chapters {
		var sidecar map[string]any
		data := mustReadFile(t, chapter.Artifact.MetadataRef)
		if err := json.Unmarshal(data, &sidecar); err != nil {
			t.Fatal(err)
		}
		sidecar["output_version"] = 5
		data, err = json.Marshal(sidecar)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(chapter.Artifact.MetadataRef, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s.Providers = provider.NewRegistry()
	dry, err := s.Rebuild(ctx, app.RebuildOptions{DryRun: true}, "preview")
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Publish.Volumes) != 1 || dry.Publish.Volumes[0].Status != "conditional_rebuild" {
		t.Fatalf("planned chapter presentation changes must report conditional rebuild: %+v", dry.Publish.Volumes)
	}
	if !strings.Contains(dry.Publish.Volumes[0].Reason, "may") {
		t.Fatalf("uncertainty is not explained: %+v", dry.Publish.Volumes)
	}
	for _, item := range dry.Publish.Items {
		if (item.Action == "replace" || item.Action == "retire") && (!item.Conditional || !strings.Contains(item.Message, "regenerated")) {
			t.Fatalf("preview promised an unconditional replacement: %+v", item)
		}
	}
	if len(dry.Notices) != 1 || !strings.Contains(dry.Notices[0], "conditional") || !strings.Contains(dry.Notices[0], "If replacement occurs") {
		t.Fatalf("notice promises an unconditional replacement: %v", dry.Notices)
	}
	if text := app.FormatPublishResult(dry.Publish); !strings.Contains(text, "[conditional replace]") {
		t.Fatalf("preview text hides uncertainty: %s", text)
	}
	if !bytes.Equal(before, mustReadFile(t, path)) {
		t.Fatal("preview wrote output")
	}
	result, err := s.Rebuild(ctx, app.RebuildOptions{}, "rebuild")
	if err != nil {
		t.Fatal(err)
	}
	// These particular chapters rebuild to the same bytes: the preview is
	// conservative, and activation can safely reuse the existing volume.
	if len(result.Publish.Volumes) != 1 || result.Publish.Volumes[0].Status != "frozen" {
		t.Fatalf("identical materialized editions should freeze: %+v", result.Publish.Volumes)
	}
	if !bytes.Equal(before, mustReadFile(t, path)) {
		t.Fatal("identical chapter rebuild changed volume")
	}
}

func TestVolumeRebuildAppliesChangedChapterEditionWithUnchangedRelease(t *testing.T) {
	s, _ := newReaderService(t)
	s.Config.Series[0].Authors = nil
	s.Config.Sources[0].Author = "Original Author"
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	ctx := context.Background()
	if _, err := s.RunOnce(ctx, "", "", "initial"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	before := mustReadFile(t, path)
	chapters, err := s.Repo.ListPublishCandidates(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Sources[0].Author = "Corrected Author"
	s.Providers = provider.NewRegistry()
	archiveFiles := findFiles(t, s.Config.Runtime.ArtifactRoot, ".epub")
	dry, err := s.Rebuild(ctx, app.RebuildOptions{DryRun: true}, "preview")
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.Publish.Volumes) != 1 || dry.Publish.Volumes[0].Status != "conditional_rebuild" {
		t.Fatalf("preview did not mark unknown member bytes conditional: %+v", dry)
	}
	if len(findFiles(t, s.Config.Runtime.ArtifactRoot, ".epub")) != len(archiveFiles) || !bytes.Equal(before, mustReadFile(t, path)) {
		t.Fatal("preview generated or changed reading copies")
	}
	result, err := s.Rebuild(ctx, app.RebuildOptions{}, "apply")
	if err != nil {
		t.Fatal(err)
	}
	prediction := dry.Publish.Volumes[0]
	prediction.Status, prediction.Reason = "complete", ""
	if !reflect.DeepEqual([]domain.VolumePlan{prediction}, result.Publish.Volumes) || bytes.Equal(before, mustReadFile(t, path)) {
		t.Fatalf("activation did not apply previewed member editions: preview=%+v actual=%+v", dry.Publish.Volumes, result.Publish.Volumes)
	}
	updated, err := s.Repo.ListPublishCandidates(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for i, chapter := range updated {
		if chapter.Release.ContentHash != chapters[i].Release.ContentHash || chapter.Artifact.SHA256 == chapters[i].Artifact.SHA256 {
			t.Fatalf("expected only a reading-copy change: before=%+v after=%+v", chapters[i], chapter)
		}
	}
	after := mustReadFile(t, path)
	unchanged, err := s.Rebuild(ctx, app.RebuildOptions{DryRun: true}, "unchanged")
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Publish.Volumes) != 1 || unchanged.Publish.Volumes[0].Status != "frozen" {
		t.Fatalf("unchanged member editions did not freeze: %+v", unchanged.Publish.Volumes)
	}
	frozen, err := s.Rebuild(ctx, app.RebuildOptions{}, "apply frozen preview")
	if err != nil || len(frozen.Publish.Volumes) != 1 || frozen.Publish.Volumes[0].Status != "frozen" || !bytes.Equal(after, mustReadFile(t, path)) {
		t.Fatalf("activation changed a volume preview promised frozen: %+v %v", frozen, err)
	}
}
