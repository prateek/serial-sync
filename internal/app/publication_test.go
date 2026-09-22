package app_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
)

func TestMetadataChangesNeedRebuildAndDoNotRepublishUnchangedEditions(t *testing.T) {
	for _, bundling := range []string{"none", "volume"} {
		t.Run(bundling, func(t *testing.T) {
			s, _ := newReaderService(t)
			s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: bundling, ChaptersPerVolume: 2}
			s.Config.Series[0].Metadata.Description = "First description"
			s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
			if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
				t.Fatal(err)
			}
			paths := findFiles(t, s.Config.Publishers[0].Path, ".epub")
			before := map[string][]byte{}
			for _, path := range paths {
				before[path] = mustReadFile(t, path)
			}
			s.Config.Series[0].Metadata.Description = "Revised description"
			if result, err := s.RunOnce(context.Background(), "", "", "metadata refresh"); err != nil || result.Publish.Published != 0 {
				t.Fatalf("metadata refresh republished: %+v %v", result, err)
			}
			for path, data := range before {
				if !bytes.Equal(data, mustReadFile(t, path)) {
					t.Fatal("ordinary metadata refresh changed a reading copy")
				}
			}
			preview, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
			if err != nil {
				t.Fatal(err)
			}
			changed := false
			for _, item := range preview.Plans {
				changed = changed || item.Action == "rebuild"
			}
			if !changed {
				t.Fatal("metadata-only edit missing from preview")
			}
			for path, data := range before {
				if !bytes.Equal(data, mustReadFile(t, path)) {
					t.Fatal("preview changed a reading copy")
				}
			}
			if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
				t.Fatal(err)
			}
			for _, path := range findFiles(t, s.Config.Publishers[0].Path, ".epub") {
				if !bytes.Contains(epubEntry(t, path, ".opf"), []byte("Revised description")) {
					t.Fatalf("rebuild omitted metadata: %s", path)
				}
			}
			if result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "unchanged"); err != nil || result.Publish.Published != 0 {
				t.Fatalf("unchanged rebuild republished: %+v %v", result, err)
			}
		})
	}
}
