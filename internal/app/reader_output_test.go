package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func TestCollisionCannotRenameDeliveredFrozenVolumeWithoutRebuild(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Inputs[0].MatchValue = ".*"
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	upstream.docs["alpha"] = upstream.docs["alpha"][:2]
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	volume := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	before := mustReadFile(t, volume)
	other := upstream.docs["alpha"][0]
	other.Normalized.ProviderReleaseID, other.Normalized.Title = "unnumbered", "Vol01"
	other.Normalized.PublishedAt = time.Time{}
	upstream.docs["alpha"] = append(upstream.docs["alpha"], other)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err == nil || !strings.Contains(err.Error(), "collision") || !strings.Contains(err.Error(), "rebuild") {
		t.Fatalf("frozen collision must require rebuild: %v", err)
	}
	if !bytes.Equal(before, mustReadFile(t, volume)) {
		t.Fatal("collision renamed frozen output")
	}
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
		t.Fatal(err)
	}
	if result, err := s.RunOnce(context.Background(), "", "", "run"); err != nil || result.Publish.Published != 0 {
		t.Fatalf("resolved collision should remain stable: %+v %v", result, err)
	}
}

func TestSourceFilteredPreviewAllowsIndependentVolumesInOneSeries(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Sources = append(s.Config.Sources, upstream.suggestions[1].Source)
	input := s.Config.Series[0].Inputs[0]
	input.Source = "beta"
	s.Config.Series[0].Inputs = append(s.Config.Series[0].Inputs, input)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 1}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	upstream.docs["beta"][0].Normalized.Title = "Alpha Saga Chapter 2"
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	s.Providers = provider.NewRegistry()
	preview, err := s.Rebuild(context.Background(), app.RebuildOptions{SourceID: "alpha", DryRun: true}, "preview")
	if err != nil {
		t.Fatalf("independent excluded volume blocked selected source: %+v %v", preview, err)
	}
	if len(preview.Publish.Items) != 1 || strings.Contains(preview.Publish.Items[0].TargetRef, "/beta/") {
		t.Fatalf("incorrect source scope: %+v", preview.Publish.Items)
	}
}

func TestOfflinePreviewRejectsInvalidBookMappings(t *testing.T) {
	s, _ := newReaderService(t)
	dump, err := s.DumpSources(context.Background(), "patreon-default", app.SourceDumpOptions{Path: filepath.Join(t.TempDir(), "workspace"), CreatorFilters: []string{"alpha"}}, "dump")
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Series[0].Inputs[0].BookID = "missing-book"
	s.Config.Series[0].Output.ChaptersPerVolume = 50
	body, err := toml.Marshal(struct {
		Series []config.SeriesConfig `toml:"series"`
	}{s.Config.Series})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dump.SeriesFile, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewRules(context.Background(), app.RulesPreviewOptions{WorkspacePath: dump.WorkspacePath, SeriesFile: dump.SeriesFile}, "preview"); err == nil || !strings.Contains(err.Error(), "book_id") {
		t.Fatalf("preview accepted invalid book mapping: %v", err)
	}
}

func TestCompletedVolumeCorrectionsNeedRebuildAndRetainPriorEdition(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	before := mustReadFile(t, path)
	beforeOPF := string(epubEntry(t, path, ".opf"))
	upstream.docs["alpha"][0].Normalized.TextHTML = "<p>Corrected text and author note.</p>"
	result, err := s.RunOnce(context.Background(), "", "", "run")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, mustReadFile(t, path)) || len(findFiles(t, s.Config.Publishers[0].Path, ".epub")) != 1 {
		t.Fatal("normal correction altered frozen volume or published a duplicate")
	}
	if len(result.Publish.Volumes) != 1 || result.Publish.Volumes[0].Status != "pending_rebuild" {
		t.Fatalf("missing correction notice: %+v", result.Publish.Volumes)
	}
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
		t.Fatal(err)
	}
	after := mustReadFile(t, path)
	if bytes.Equal(before, after) {
		t.Fatal("rebuild omitted correction")
	}
	records, err := s.ListPublishRecords(context.Background(), "", "library")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Record.TargetRef == path && bytes.Equal(mustReadFile(t, record.Artifact.StorageRef), before) && record.Record.Status != domain.PublishStatusSuperseded {
			t.Fatalf("replaced volume still reported as delivered: %+v", record.Record)
		}
	}
	identifier := regexp.MustCompile(`<dc:identifier[^>]*>[^<]+</dc:identifier>`)
	if identifier.FindString(beforeOPF) == "" || identifier.FindString(beforeOPF) != identifier.FindString(string(epubEntry(t, path, ".opf"))) {
		t.Fatal("corrected volume changed its book identifier")
	}
	retained := false
	for _, file := range findFiles(t, s.Config.Runtime.ArtifactRoot, ".epub") {
		if bytes.Equal(mustReadFile(t, file), before) {
			retained = true
		}
	}
	if !retained {
		t.Fatal("previous volume was not retained in archive")
	}
	if result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "unchanged rebuild"); err != nil || result.Publish.Published != 0 {
		t.Fatalf("unchanged rebuild republished: %+v %v", result, err)
	}
	if result, err := s.RunOnce(context.Background(), "", "", "unchanged run"); err != nil || result.Publish.Published != 0 || !bytes.Equal(after, mustReadFile(t, path)) {
		t.Fatalf("ordinary run disturbed rebuilt output: %+v %v", result, err)
	}
	for _, file := range findFiles(t, s.Config.Runtime.ArtifactRoot, ".epub") {
		if !bytes.Equal(mustReadFile(t, file), after) {
			continue
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "repair archive"); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(mustReadFile(t, file), after) {
			t.Fatal("rebuild failed to restore the archived volume")
		}
		return
	}
	t.Fatal("current volume missing from archive")
}

func TestMissingIntermediateBookDoesNotInventSeriesPositions(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume"}
	s.Config.Series[0].Books = []config.BookConfig{{ID: "one", Number: 1, LastChapter: 1}, {ID: "three", Number: 3, LastChapter: 1}}
	upstream.docs["alpha"][0].Normalized.Title = "Alpha Saga Book 1 Chapter 1"
	upstream.docs["alpha"][1].Normalized.Title = "Alpha Saga Book 3 Chapter 1"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	chapter := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-bk03-ch0001.epub")
	if opf := string(epubEntry(t, chapter, ".opf")); strings.Contains(opf, "calibre:series_index") || strings.Contains(opf, "group-position") {
		t.Fatalf("ambiguous position was invented: %s", opf)
	}
	s.Config.Series[0].Books[1].SeriesPositionStart = 101
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
		t.Fatal(err)
	}
	volume := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-bk03.epub")
	if !strings.Contains(string(epubEntry(t, volume, ".opf")), `name="calibre:series_index" content="101"`) {
		t.Fatal("explicit position did not resolve missing span")
	}
}

func TestSourceFilteredPreviewIgnoresIndependentSeries(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Sources = append(s.Config.Sources, upstream.suggestions[1].Source)
	beta := s.Config.Series[0]
	beta.ID, beta.Title = "beta-tale", "Beta Tale"
	beta.Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume"}
	beta.Inputs = []config.SeriesInputConfig{{Source: "beta", MatchType: "title_regex", MatchValue: "^Beta", ReleaseRole: "chapter", ContentStrategy: "text_post"}}
	s.Config.Series = append(s.Config.Series, beta)
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	s.Providers = provider.NewRegistry()
	preview, err := s.Rebuild(context.Background(), app.RebuildOptions{SourceID: "alpha", DryRun: true}, "preview")
	if err != nil {
		t.Fatalf("excluded source must not block preview: %+v %v", preview, err)
	}
	for _, item := range preview.Publish.Items {
		if strings.Contains(item.TargetRef, "beta-tale") {
			t.Fatalf("preview widened source scope: %+v", item)
		}
	}
}

func TestBookChapterOutsideDeclaredRangeRemainsSingle(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume"}
	s.Config.Series[0].Books = []config.BookConfig{{ID: "one", Number: 1, LastChapter: 1}}
	for i := range upstream.docs["alpha"] {
		upstream.docs["alpha"][i].Normalized.Title = fmt.Sprintf("Alpha Saga Book 1 Chapter %d", i+1)
	}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Publishers[0].Path, ".epub")
	if len(files) != 2 {
		t.Fatalf("out-of-range chapter must remain a single: %v", files)
	}
	volume := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-bk01.epub")
	if strings.Contains(string(epubNavigation(t, volume)), "Chapter 2") {
		t.Fatal("book swallowed an out-of-range chapter")
	}
}

func TestRebuildPreviewReportsUserModifiedDestination(t *testing.T) {
	s, _ := newReaderService(t)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-ch0001.html")
	if err := os.WriteFile(path, []byte("reader notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
	if err == nil || !strings.Contains(err.Error(), "ownership conflict") {
		t.Fatalf("preview hid conflict: %+v %v", result, err)
	}
	if string(mustReadFile(t, path)) != "reader notes" {
		t.Fatal("preview changed reader notes")
	}
	for _, item := range result.Publish.Items {
		if item.TargetRef == path && item.Action != "blocked" {
			t.Fatalf("conflicted output should be blocked: %+v", item)
		}
	}
	s.Config.Series[0].Title = "Renamed Saga"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview rename"); err == nil || !strings.Contains(err.Error(), "retirement ownership conflict") {
		t.Fatalf("preview promised to retire user-modified old path: %v", err)
	}
}

func TestPublishedNamesPreserveBookWordsDatesAndAttachmentSequenceFallback(t *testing.T) {
	for _, tc := range []struct{ series, title, attachment, mime, want string }{
		{"The Sixth School", "The Sixth School. Book Two. Chapter 058.", "Book Two Chapter 058.epub", "application/epub+zip", "the-sixth-school-bk02-ch0058.epub"},
		{"The Sixth School Editing Marathon", "Editing Marathon: Chapter Eighty", "080 Chapter Eighty.pdf", "application/pdf", "the-sixth-school-editing-marathon-ch0080.pdf"},
		{"Announcements", "Schedule update for April", "", "text/html", "announcements-2026-04-05-schedule-update-for-april.html"},
		{"The Sixth School", "New chapter attached", "Book Two Chapter 058.epub", "application/epub+zip", "the-sixth-school-bk02-ch0058.epub"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			s, upstream := newReaderService(t)
			s.Config.Series[0].Title = tc.series
			s.Config.Series[0].Inputs[0].MatchValue = ".*"
			doc := upstream.docs["alpha"][0]
			doc.Normalized.Title = tc.title
			doc.Normalized.PublishedAt = time.Date(2026, 4, 5, 15, 30, 0, 0, time.UTC)
			doc.Normalized.Attachments = nil
			if tc.attachment != "" {
				path := filepath.Join(t.TempDir(), tc.attachment)
				if err := os.WriteFile(path, []byte("unchanged original attachment"), 0o644); err != nil {
					t.Fatal(err)
				}
				doc.Normalized.Attachments = []domain.Attachment{{FileName: tc.attachment, MIMEType: tc.mime, LocalPath: path}}
				s.Config.Series[0].Inputs[0].ContentStrategy = "attachment_only"
			}
			s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
			upstream.docs["alpha"] = []provider.ReleaseDocument{doc}
			if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
				t.Fatal(err)
			}
			files := findFiles(t, s.Config.Publishers[0].Path, "")
			if len(files) != 1 || filepath.Base(files[0]) != tc.want {
				t.Fatalf("published names: %v, want %s", files, tc.want)
			}
			if tc.attachment != "" && string(mustReadFile(t, files[0])) != "unchanged original attachment" {
				t.Fatal("preserve altered original bytes")
			}
		})
	}
}

func TestSourceFilteredRebuildCannotDismantleAMixedSourceVolume(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Sources = append(s.Config.Sources, upstream.suggestions[1].Source)
	input := s.Config.Series[0].Inputs[0]
	input.Source = "beta"
	s.Config.Series[0].Inputs = append(s.Config.Series[0].Inputs, input)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	second := upstream.docs["alpha"][1]
	second.Normalized.ProviderReleaseID = "b2"
	upstream.docs["beta"] = []provider.ReleaseDocument{second}
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	before := mustReadFile(t, path)
	s.Config.Series[0].Output.Bundling = "none"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	s.Providers = provider.NewRegistry()
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{SourceID: "beta"}, "partial rebuild"); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("expected blocked mixed-source transition: %v", err)
	}
	if !bytes.Equal(before, mustReadFile(t, path)) || len(findFiles(t, s.Config.Publishers[0].Path, ".epub")) != 1 {
		t.Fatal("narrow rebuild changed excluded output")
	}
}

func TestFailedRegroupDoesNotActivateOnlyPartOfTheNewVolumes(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 4}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	for _, n := range []string{"3", "4"} {
		extra := docs[0]
		extra.Normalized.ProviderReleaseID = "a" + n
		extra.Normalized.Title = "Alpha Saga Chapter " + n
		docs = append(docs, extra)
	}
	upstream.docs["alpha"] = docs
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	before := mustReadFile(t, path)
	realValidator, err := exec.LookPath("epubcheck")
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	script := `#!/bin/sh
n=0
if [ -f "$READER_FAIL_COUNT" ]; then n=$(cat "$READER_FAIL_COUNT"); fi
n=$((n+1))
printf '%s' "$n" > "$READER_FAIL_COUNT"
if [ "$n" -eq 2 ]; then echo 'injected validator failure' >&2; exit 1; fi
exec "$READER_REAL_VALIDATOR" "$@"
`
	if err := os.WriteFile(filepath.Join(shim, "epubcheck"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("READER_FAIL_COUNT", filepath.Join(shim, "count"))
	t.Setenv("READER_REAL_VALIDATOR", realValidator)
	originalPATH := os.Getenv("PATH")
	t.Setenv("PATH", shim+string(os.PathListSeparator)+originalPATH)
	s.Config.Series[0].Output.ChaptersPerVolume = 2
	failed, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild")
	if err == nil {
		t.Fatal("expected failed regroup")
	}
	for _, volume := range failed.Publish.Volumes {
		if volume.Status == "complete" {
			t.Fatalf("failed series reported a completed replacement: %+v", failed.Publish.Volumes)
		}
	}
	t.Setenv("PATH", originalPATH)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatalf("failed regroup left inconsistent active output: %v", err)
	}
	if !bytes.Equal(before, mustReadFile(t, path)) || len(findFiles(t, s.Config.Publishers[0].Path, ".epub")) != 1 {
		t.Fatal("failed regroup changed the delivered volume")
	}
}

func TestOfflineRebuildRepairsMissingArchivedBytes(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Runtime.ArtifactRoot, ".html")
	if len(files) != 1 {
		t.Fatalf("expected one archived artifact: %v", files)
	}
	published := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-ch0001.html")
	before := mustReadFile(t, published)
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(published); err != nil {
		t.Fatal(err)
	}
	s.Providers = provider.NewRegistry()
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, mustReadFile(t, published)) {
		t.Fatal("offline repair changed chapter bytes")
	}
}

func TestRebuildPreviewIncludesStoredReleasesThatPreviouslyProducedNoFiles(t *testing.T) {
	for _, volume := range []bool{false, true} {
		t.Run(fmt.Sprintf("volume=%t", volume), func(t *testing.T) {
			s, _ := newReaderService(t)
			if volume {
				s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
			}
			s.Config.Series[0].Inputs[0].ContentStrategy = "manual"
			s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
			if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
				t.Fatal(err)
			}
			s.Config.Series[0].Inputs[0].ContentStrategy = "text_post"
			s.Config.Series[0].ID = "new-saga"
			s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
			s.Providers = provider.NewRegistry()
			preview, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true, SeriesID: "new-saga"}, "preview")
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if volume {
				want = 1
			}
			if len(preview.Publish.Items) != want {
				t.Fatalf("preview omitted newly materializable stored releases: %+v", preview)
			}
			if result, err := s.Rebuild(context.Background(), app.RebuildOptions{SeriesID: "new-saga"}, "rebuild"); err != nil || result.Publish.Published != want {
				t.Fatalf("rebuild disagrees with preview: %+v %v", result, err)
			}
		})
	}
}

func TestVersionedHookRenamesPreviouslyDeliveredCollision(t *testing.T) {
	s, upstream := newReaderService(t)
	root := t.TempDir()
	output := filepath.Join(root, "library")
	script := filepath.Join(root, "hook.py")
	body := `import json,sys,pathlib,hashlib
e=json.load(sys.stdin)
root=pathlib.Path(sys.argv[1]);root.mkdir(exist_ok=True)
if e["action"]=="publish":
 a=e["artifact"];(root/a["filename"]).write_bytes(pathlib.Path(a["storage_ref"]).read_bytes())
else:
 a=e["previous"]["artifact"];p=root/a["filename"]
 if p.exists() and hashlib.sha256(p.read_bytes()).hexdigest()==a["sha256"]:p.unlink()
`
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Config.Publishers = []config.PublisherConfig{{ID: "hook", Kind: "exec", Enabled: true, ProtocolVersion: 2, Command: []string{"python3", script, output}}}
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	docs[1].Normalized.Title = "Alpha Saga Chapter 1 duplicate"
	upstream.docs["alpha"] = docs[:1]
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	upstream.docs["alpha"] = docs
	if _, err := s.Sync(context.Background(), "", false, "sync"); err != nil {
		t.Fatal(err)
	}
	preview, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
	if err != nil {
		t.Fatal(err)
	}
	retirements := 0
	for _, item := range preview.Publish.Items {
		if item.Action == "retire" {
			retirements++
		}
	}
	if retirements != 1 {
		t.Fatalf("hook preview must retire the unsuffixed receipt: %+v", preview.Publish.Items)
	}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, output, ".html")
	if len(files) != 2 {
		t.Fatalf("collision lost output: %v", files)
	}
	for _, file := range files {
		if !strings.Contains(filepath.Base(file), "-r") {
			t.Fatalf("hook left the old unsuffixed path: %v", files)
		}
	}
}

func TestPendingDeliveryRetainsItsArtifactAcrossNewUpstreamCorrections(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	root := t.TempDir()
	log := filepath.Join(root, "events.jsonl")
	fail := filepath.Join(root, "fail")
	script := filepath.Join(root, "hook.py")
	if err := os.WriteFile(script, []byte("import json,sys,pathlib\ne=json.load(sys.stdin)\nwith open(sys.argv[1],'a') as f:f.write(json.dumps(e)+'\\n')\nif pathlib.Path(sys.argv[2]).exists():sys.exit(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fail, []byte("fail"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Config.Publishers = []config.PublisherConfig{{ID: "hook", Kind: "exec", Enabled: true, ProtocolVersion: 2, Command: []string{"python3", script, log, fail}}}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err == nil {
		t.Fatal("expected delivery failure")
	}
	upstream.docs["alpha"][0].Normalized.TextHTML = "<p>A correction arrived while the delivery was pending.</p>"
	if _, err := s.RunOnce(context.Background(), "", "", "retry"); err == nil {
		t.Fatal("expected retry failure")
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(string(mustReadFile(t, log))), "\n") {
		var event struct {
			ID string `json:"event_id"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, event.ID)
	}
	if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("pending delivery changed before acknowledgement: %v", ids)
	}
	beforePreview := mustReadFile(t, log)
	preview, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(preview)
	var planned struct {
		Pending []struct {
			Candidates []domain.PublishCandidate `json:"candidates"`
		} `json:"pending_deliveries"`
	}
	if err := json.Unmarshal(data, &planned); err != nil {
		t.Fatal(err)
	}
	var firstEvent struct {
		Artifact domain.Artifact `json:"artifact"`
	}
	if err := json.Unmarshal(bytes.Split(beforePreview, []byte("\n"))[0], &firstEvent); err != nil {
		t.Fatal(err)
	}
	if len(planned.Pending) != 1 || len(planned.Pending[0].Candidates) != 1 || planned.Pending[0].Candidates[0].Artifact.ID != firstEvent.Artifact.ID {
		t.Fatalf("preview omitted the frozen pending edition: %s", data)
	}
	if !bytes.Equal(beforePreview, mustReadFile(t, log)) {
		t.Fatal("preview invoked hook")
	}
	originalCommand := s.Config.Publishers[0].Command
	s.Config.Publishers[0].Command = []string{"sh", "-c", "exit 0"}
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview"); err == nil || !strings.Contains(err.Error(), "pending delivery") {
		t.Fatalf("preview ignored changed pending settings: %v", err)
	}
	s.Config.Publishers = append(s.Config.Publishers, config.PublisherConfig{ID: "independent", Kind: "filesystem", Path: filepath.Join(root, "independent"), Enabled: true})
	if result, err := s.Publish(context.Background(), "", "", false, "retry"); err == nil || result.Published != 1 {
		t.Fatalf("blocked pending hook prevented independent delivery: %+v %v", result, err)
	}
	s.Config.Publishers[0].Command = originalCommand
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(context.Background(), "", "hook", false, "retry"); err != nil {
		t.Fatal(err)
	}
}

func TestRetirementRetryKeepsEventIdentityWhenUnrelatedChaptersArrive(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	root := t.TempDir()
	log := filepath.Join(root, "events.jsonl")
	fail := filepath.Join(root, "fail")
	script := filepath.Join(root, "hook.py")
	if err := os.WriteFile(script, []byte("import json,sys,pathlib\ne=json.load(sys.stdin)\nwith open(sys.argv[1],'a') as f:f.write(json.dumps(e)+'\\n')\nif e['action']=='supersede' and pathlib.Path(sys.argv[2]).exists():sys.exit(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Config.Publishers = []config.PublisherConfig{{ID: "hook", Kind: "exec", Enabled: true, ProtocolVersion: 2, Command: []string{"python3", script, log, fail}}}
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	upstream.docs["alpha"] = docs[:1]
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fail, []byte("fail"), 0o644); err != nil {
		t.Fatal(err)
	}
	upstream.docs["alpha"] = docs
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err == nil {
		t.Fatal("expected failed retirement")
	}
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	third := docs[0]
	third.Normalized.ProviderReleaseID = "a3"
	third.Normalized.Title = "Alpha Saga Chapter 3"
	upstream.docs["alpha"] = append(docs, third)
	if _, err := s.RunOnce(context.Background(), "", "", "retry"); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(string(mustReadFile(t, log))), "\n") {
		var e struct {
			Action, EventID string
			Replacements    []domain.PublishCandidate
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatal(err)
		}
		json.Unmarshal(raw["action"], &e.Action)
		json.Unmarshal(raw["event_id"], &e.EventID)
		json.Unmarshal(raw["replacements"], &e.Replacements)
		if e.Action == "supersede" {
			ids = append(ids, e.EventID)
			if len(e.Replacements) != 1 {
				t.Fatalf("retirement includes unrelated chapters: %+v", e.Replacements)
			}
		}
	}
	if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("retirement event identity changed on retry: %v", ids)
	}
}

func TestLegacyHookRejectsRenameBeforeAnyTargetDelivery(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	log := filepath.Join(t.TempDir(), "event.json")
	s.Config.Publishers = append(s.Config.Publishers, config.PublisherConfig{ID: "hook", Kind: "exec", Enabled: true, Command: []string{"sh", "-c", "cat >> \"$1\"", "hook", log}})
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	before := mustReadFile(t, log)
	s.Config.Series[0].Title = "Renamed Saga"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview"); err == nil || !strings.Contains(err.Error(), "protocol_version") {
		t.Fatalf("preview must reject the incompatible hook before promising a replacement: %v", err)
	}
	result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild")
	if err == nil || !strings.Contains(err.Error(), "protocol_version") {
		t.Fatalf("expected compatibility error: %v", err)
	}
	publishRun, err := s.InspectRun(context.Background(), result.Publish.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if publishRun.Run.Status != domain.RunStatusFailed {
		t.Fatalf("rejected publication recorded %s instead of failed", publishRun.Run.Status)
	}

	if !bytes.Equal(before, mustReadFile(t, log)) {
		t.Fatal("legacy hook received replacement before validation")
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".html"); len(files) != 1 || filepath.Base(files[0]) != "alpha-saga-ch0001.html" {
		t.Fatalf("filesystem changed before hook validation: %v", files)
	}
}

func TestVersionedHookRetainsOldArtifactIdentityWhenOnlyTheNameChanges(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	log := filepath.Join(t.TempDir(), "events.jsonl")
	s.Config.Publishers = []config.PublisherConfig{{ID: "hook", Kind: "exec", Enabled: true, ProtocolVersion: 2, Command: []string{"sh", "-c", "cat >> \"$1\"; echo >> \"$1\"", "hook", log}}}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	s.Config.Series[0].Title = "Renamed Saga"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(mustReadFile(t, log))), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected old publish, renamed publish, supersede; got %d events", len(lines))
	}
	var event struct {
		Action       string                     `json:"action"`
		Previous     domain.PublishRecordBundle `json:"previous"`
		Replacements []domain.PublishCandidate  `json:"replacements"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &event); err != nil {
		t.Fatal(err)
	}
	if event.Action != "supersede" || event.Previous.Artifact.Filename != "alpha-saga-ch0001.html" || len(event.Replacements) != 1 || event.Replacements[0].Artifact.Filename != "renamed-saga-ch0001.html" {
		t.Fatalf("lost prior artifact mapping: %+v", event)
	}
}

func TestPDFVolumeRebuildUsesCapturedAttachmentAfterProviderCacheIsGone(t *testing.T) {
	s, upstream := newReaderService(t)
	cache := filepath.Join(t.TempDir(), "chapter.pdf")
	if err := os.WriteFile(cache, mustReadFile(t, "../../testdata/fixtures/reader/chapter.pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", PrefaceMode: "prepend_post", Bundling: "volume", ChaptersPerVolume: 1}
	s.Config.Series[0].Inputs[0].ContentStrategy = "attachment_only"
	s.Config.Series[0].Inputs[0].AttachmentGlob = []string{"*.pdf"}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	doc := upstream.docs["alpha"][0]
	doc.Normalized.Attachments = []domain.Attachment{{FileName: "chapter.pdf", MIMEType: "application/pdf", LocalPath: cache}}
	doc.Normalized.TextHTML = "<p>An author note worth retaining.</p>"
	upstream.docs["alpha"] = []provider.ReleaseDocument{doc}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	s.Providers = provider.NewRegistry()
	s.Config.Series[0].Title = "Revised Saga"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Publishers[0].Path, ".epub")
	if len(files) != 1 || filepath.Base(files[0]) != "revised-saga-vol01.epub" {
		t.Fatalf("unexpected converted volume: %v", files)
	}
	archive, err := zip.OpenReader(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var contents strings.Builder
	for _, entry := range archive.File {
		if strings.HasSuffix(entry.Name, ".xhtml") || strings.HasSuffix(entry.Name, ".html") {
			reader, err := entry.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				t.Fatal(err)
			}
			contents.Write(data)
		}
	}
	if !strings.Contains(contents.String(), "author note worth retaining") || !strings.Contains(contents.String(), "captured chapter") {
		t.Fatal("conversion or bundling lost the chapter or preface")
	}
	t.Setenv("PATH", t.TempDir())
	if result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "unchanged rebuild"); err != nil || result.Publish.Published != 0 {
		t.Fatalf("unchanged rebuild should reuse validated bytes without conversion: %+v %v", result, err)
	}
	inputs := findFiles(t, filepath.Join(s.Config.Runtime.ArtifactRoot, "inputs"), ".pdf")
	if len(inputs) != 1 {
		t.Fatalf("missing captured PDF: %v", inputs)
	}
	if err := os.WriteFile(inputs[0], []byte("corrupted capture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview"); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("preview trusted a corrupted captured PDF: %v", err)
	}
}

func TestRebuildDryRunNamesVolumeAndExactRetirementWithoutWriting(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	upstream.docs["alpha"] = docs[:1]
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	upstream.docs["alpha"] = docs
	if _, err := s.Sync(context.Background(), "", false, "sync"); err != nil {
		t.Fatal(err)
	}
	s.Providers = provider.NewRegistry()
	before := findFiles(t, s.Config.Runtime.ArtifactRoot, "")
	result, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "rebuild preview")
	if err != nil {
		t.Fatal(err)
	}
	var additions, retirements []string
	for _, item := range result.Publish.Items {
		if item.Action == "retire" {
			retirements = append(retirements, filepath.Base(item.TargetRef))
		} else {
			additions = append(additions, filepath.Base(item.TargetRef))
		}
	}
	if !reflect.DeepEqual(additions, []string{"alpha-saga-vol01.epub"}) || !reflect.DeepEqual(retirements, []string{"alpha-saga-ch0001.epub"}) {
		t.Fatalf("inexact cutover plan: %+v", result.Publish)
	}
	if len(result.Publish.Volumes) != 1 || result.Publish.Volumes[0].Status != "complete" {
		t.Fatalf("missing volume plan: %+v", result.Publish.Volumes)
	}
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), "reading position") {
		t.Fatal("replacement preview omitted the reading-position notice")
	}
	if !reflect.DeepEqual(before, findFiles(t, s.Config.Runtime.ArtifactRoot, "")) {
		t.Fatal("dry-run created artifacts")
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(files) != 1 || filepath.Base(files[0]) != "alpha-saga-ch0001.epub" {
		t.Fatalf("dry-run changed published files: %v", files)
	}
}

func TestOfflinePreviewExplainsChapterSequenceAndVolumeGaps(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 3}
	upstream.docs["alpha"][1].Normalized.Title = "Alpha Saga Chaper 3"
	dump, err := s.DumpSources(context.Background(), "patreon-default", app.SourceDumpOptions{Path: filepath.Join(t.TempDir(), "workspace"), CreatorFilters: []string{"alpha"}}, "dump")
	if err != nil {
		t.Fatal(err)
	}
	body, err := toml.Marshal(struct {
		Series []config.SeriesConfig `toml:"series"`
	}{s.Config.Series})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dump.SeriesFile, body, 0o644); err != nil {
		t.Fatal(err)
	}
	s.Providers = provider.NewRegistry()
	preview, err := s.PreviewRules(context.Background(), app.RulesPreviewOptions{WorkspacePath: dump.WorkspacePath, SeriesFile: dump.SeriesFile, ShowPosts: true}, "preview")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Volumes) != 1 || preview.Volumes[0].First != 1 || preview.Volumes[0].Last != 3 || !reflect.DeepEqual(preview.Volumes[0].Missing, []int{2}) || !reflect.DeepEqual(preview.Volumes[0].Present, []int{1, 3}) {
		t.Fatalf("incomplete volume not explained: %+v", preview.Volumes)
	}
	encoded, _ := json.Marshal(preview)
	if !strings.Contains(string(encoded), `"matched_text":"Chaper 3"`) {
		t.Fatalf("preview omitted the matched chapter marker: %s", encoded)
	}
	if text := app.FormatRulesPreviewResult(preview, true); !strings.Contains(text, "missing=[2]") || !strings.Contains(text, "alpha-saga-ch0003.epub") {
		t.Fatalf("text preview hides reading order or gaps: %s", text)
	}
	for _, post := range preview.Creators[0].Preview.Posts {
		if post.ProviderReleaseID == "a2" {
			if post.Sequence == nil || post.Sequence.Chapter != 3 || post.Filename != "alpha-saga-ch0003.epub" {
				t.Fatalf("preview disagrees with output sequence: %+v", post)
			}
			return
		}
	}
	t.Fatal("preview omitted chapter")
}

func TestSequenceOverridesResolveDuplicateAndShortFinalRange(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 50, FinalChapter: 3}
	s.Config.Series[0].SequenceOverrides = []config.SequenceOverride{{Source: "alpha", ReleaseID: "duplicate", KeepSingle: true}, {Source: "alpha", ReleaseID: "interlude", Chapter: 3}}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	duplicate := docs[1]
	duplicate.Normalized.ProviderReleaseID = "duplicate"
	interlude := docs[0]
	interlude.Normalized.ProviderReleaseID = "interlude"
	interlude.Normalized.Title = "Alpha Saga Interlude"
	upstream.docs["alpha"] = append(docs, duplicate, interlude)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Publishers[0].Path, ".epub")
	if len(files) != 2 {
		t.Fatalf("selected chapter slots must complete volume and retain duplicate single: %v", files)
	}
	volume := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	nav := string(epubNavigation(t, volume))
	if !strings.Contains(nav, "Interlude") || strings.Count(nav, "Chapter 2") != 1 {
		t.Fatalf("override chose wrong membership: %s", nav)
	}
}

func TestMissingRebuildInputBlocksTheRelatedReplacement(t *testing.T) {
	s, _ := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	volume := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	before := mustReadFile(t, volume)
	inspect, err := s.InspectSource(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, release := range inspect.Releases {
		if release.ProviderReleaseID == "a1" {
			if err := os.Remove(release.RawPayloadRef); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.Config.Series[0].Output.Bundling = "none"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	s.Providers = provider.NewRegistry()
	result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild")
	if err == nil || len(result.Blocked) == 0 {
		t.Fatalf("missing input not reported: %+v %v", result, err)
	}
	if !bytes.Equal(before, mustReadFile(t, volume)) {
		t.Fatal("blocked rebuild replaced the old volume")
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(files) != 1 {
		t.Fatalf("blocked transition partially published: %v", files)
	}
}

func TestVolumeSplitFailureRetainsOldVolumeOnThatTarget(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 4}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	for _, n := range []string{"3", "4"} {
		extra := docs[0]
		extra.Normalized.ProviderReleaseID = "a" + n
		extra.Normalized.Title = "Alpha Saga Chapter " + n
		docs = append(docs, extra)
	}
	upstream.docs["alpha"] = docs
	second := config.PublisherConfig{ID: "second", Kind: "filesystem", Path: t.TempDir(), Enabled: true}
	s.Config.Publishers = append(s.Config.Publishers, second)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(second.Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	before := mustReadFile(t, oldPath)
	conflict := filepath.Join(second.Path, "alpha", "alpha-saga", "alpha-saga-vol02.epub")
	if err := os.WriteFile(conflict, []byte("user file"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Config.Series[0].Output.ChaptersPerVolume = 2
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err == nil {
		t.Fatal("expected incomplete replacement")
	}
	if !bytes.Equal(before, mustReadFile(t, oldPath)) {
		t.Fatal("failed split overwrote the only delivered copy of chapters 3 and 4")
	}
	if got := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(got) != 2 {
		t.Fatalf("independent target did not finish: %v", got)
	}
	if err := os.Remove(conflict); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(context.Background(), "", "second", false, "retry"); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, mustReadFile(t, oldPath)) {
		t.Fatal("retry did not finish split")
	}
	if nav := string(epubNavigation(t, conflict)); !strings.Contains(nav, "Chapter 4") {
		t.Fatal("split lost chapter 4")
	}
}

func TestChangingVolumeSizeWaitsForRebuildAndRetainsAllChapters(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	third := docs[0]
	third.Normalized.Title = "Alpha Saga Chapter 3"
	third.Normalized.ProviderReleaseID = "a3"
	upstream.docs["alpha"] = append(docs, third)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	volume := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	before := mustReadFile(t, volume)
	s.Config.Series[0].Output.ChaptersPerVolume = 3
	result, err := s.RunOnce(context.Background(), "", "", "run")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, mustReadFile(t, volume)) || len(findFiles(t, s.Config.Publishers[0].Path, ".epub")) != 2 {
		t.Fatal("normal run regrouped frozen chapters")
	}
	if len(result.Publish.Volumes) == 0 || result.Publish.Volumes[0].Status != "pending_rebuild" {
		t.Fatalf("missing regroup explanation: %+v", result.Publish.Volumes)
	}
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
		t.Fatal(err)
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(files) != 1 {
		t.Fatalf("regroup left extra files: %v", files)
	}
	if nav := string(epubNavigation(t, volume)); !strings.Contains(nav, "Chapter 3") {
		t.Fatal("regroup lost chapter 3")
	}
}

func TestLegacyHookRejectsVolumeBeforeAnyTargetDelivery(t *testing.T) {
	s, _ := newReaderService(t)
	log := filepath.Join(t.TempDir(), "legacy.json")
	s.Config.Publishers = append(s.Config.Publishers, config.PublisherConfig{ID: "legacy", Kind: "exec", Enabled: true, Command: []string{"sh", "-c", "cat >> \"$1\"", "hook", log}})
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	before := mustReadFile(t, log)
	files := findFiles(t, s.Config.Publishers[0].Path, ".html")
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err == nil || !strings.Contains(err.Error(), "protocol_version") {
		t.Fatalf("expected hook upgrade error: %v", err)
	}
	if !bytes.Equal(before, mustReadFile(t, log)) {
		t.Fatal("legacy hook called before compatibility validation")
	}
	if got := findFiles(t, s.Config.Publishers[0].Path, ".html"); !reflect.DeepEqual(got, files) {
		t.Fatal("old files changed before validation")
	}
	if got := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(got) != 0 {
		t.Fatalf("other target received replacements before validation: %v", got)
	}
}

func TestVersionedHookRetriesVolumeBeforeRetiringSingle(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	root := t.TempDir()
	script := filepath.Join(root, "hook.py")
	log := filepath.Join(root, "events.jsonl")
	fail := filepath.Join(root, "fail")
	body := `import json,sys,pathlib
event=json.load(sys.stdin)
with open(sys.argv[1],"a") as log: log.write(json.dumps(event)+"\n")
if event["action"]=="publish" and event.get("volume") and pathlib.Path(sys.argv[2]).exists(): sys.exit(1)
`
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Config.Publishers = append(s.Config.Publishers, config.PublisherConfig{ID: "hook", Kind: "exec", Command: []string{"python3", script, log, fail}, Enabled: true, ProtocolVersion: 2})
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	upstream.docs["alpha"] = docs[:1]
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fail, []byte("fail"), 0o644); err != nil {
		t.Fatal(err)
	}
	upstream.docs["alpha"] = docs
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err == nil {
		t.Fatal("failed volume delivery must fail run")
	}
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(mustReadFile(t, log))), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	for _, event := range events {
		if event["action"] == "supersede" {
			t.Fatal("hook retired single before replacement acknowledgement")
		}
	}
	failedID := events[len(events)-1]["event_id"]
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(context.Background(), "", "hook", false, "retry"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(mustReadFile(t, log))), "\n")
	var publishEvent, retireEvent map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-2]), &publishEvent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &retireEvent); err != nil {
		t.Fatal(err)
	}
	if failedID == nil || failedID != publishEvent["event_id"] {
		t.Fatal("retried hook event must keep its ID")
	}
	if publishEvent["action"] != "publish" || retireEvent["action"] != "supersede" || retireEvent["previous"] == nil || retireEvent["replacements"] == nil {
		t.Fatalf("invalid replacement events: %v %v", publishEvent, retireEvent)
	}
}

func TestDisablingBundlingKeepsCompletedVolumeUntilExplicitRebuild(t *testing.T) {
	s, _ := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	volume := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	before := mustReadFile(t, volume)
	s.Config.Series[0].Output.Bundling = "none"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, mustReadFile(t, volume)) {
		t.Fatal("normal run dismantled a completed volume")
	}
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "run --rebuild"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Publishers[0].Path, ".epub")
	var names []string
	for _, file := range files {
		names = append(names, filepath.Base(file))
	}
	if want := []string{"alpha-saga-ch0001.epub", "alpha-saga-ch0002.epub"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("explicit rebuild must replace volume with singles: %v", names)
	}
}

func TestIntentionalGapAndLateChapterRequireExplicitVolumeRebuild(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 3, IntentionalGaps: []config.ChapterGap{{Chapter: 2, Reason: "author skipped this number"}}}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	docs[1].Normalized.Title = "Alpha Saga Chapter 3"
	upstream.docs["alpha"] = docs
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	volume := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	before := mustReadFile(t, volume)
	if front := string(epubEntry(t, volume, "contents.xhtml")); !strings.Contains(front, "author skipped this number") {
		t.Fatalf("intentional gap not disclosed: %s", front)
	}
	late := docs[0]
	late.Normalized.ProviderReleaseID = "late-2"
	late.Normalized.Title = "Alpha Saga Chapter 2"
	upstream.docs["alpha"] = append(docs, late)
	result, err := s.RunOnce(context.Background(), "", "", "run")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, mustReadFile(t, volume)) {
		t.Fatal("normal sync changed completed volume")
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(files) != 2 {
		t.Fatalf("late chapter must remain available as a single: %v", files)
	}
	if len(result.Publish.Volumes) != 1 || result.Publish.Volumes[0].Status != "pending_rebuild" {
		t.Fatalf("late chapter not reported: %+v", result.Publish.Volumes)
	}
	s.Providers = provider.NewRegistry()
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "run --rebuild"); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, mustReadFile(t, volume)) {
		t.Fatal("explicit rebuild did not include late chapter")
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(files) != 1 {
		t.Fatalf("rebuild did not retire late single: %v", files)
	}
}

func TestAuthorBooksOverrideFixedSizeAndOrderRestartedChapters(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 50}
	s.Config.Series[0].Books = []config.BookConfig{{ID: "one", Number: 1, FirstChapter: 1, LastChapter: 2}, {ID: "two", Number: 2, FirstChapter: 1, LastChapter: 1}}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	docs[0].Normalized.Title = "Alpha Saga Book 1 Chapter 1"
	docs[1].Normalized.Title = "Alpha Saga Book 1 Chapter 2"
	third := docs[0]
	third.Normalized.ProviderReleaseID = "b1"
	third.Normalized.Title = "Alpha Saga Book 2 Chapter 1"
	upstream.docs["alpha"] = append(docs, third)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Publishers[0].Path, ".epub")
	var names []string
	for _, file := range files {
		names = append(names, filepath.Base(file))
	}
	if want := []string{"alpha-saga-bk01.epub", "alpha-saga-bk02.epub"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("author books: %v, want %v", names, want)
	}
	opf := string(epubEntry(t, files[1], ".opf"))
	if !strings.Contains(opf, `name="calibre:series_index" content="3"`) {
		t.Fatalf("book 2 must follow the two chapters of book 1: %s", opf)
	}
}

func TestLaterChapterDoesNotCloseAVolumeWithAMissingChapter(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 3}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	docs[1].Normalized.Title = "Alpha Saga Chapter 3"
	later := docs[1]
	later.Normalized.ProviderReleaseID = "a4"
	later.Normalized.Title = "Alpha Saga Chapter 4"
	upstream.docs["alpha"] = append(docs, later)
	result, err := s.RunOnce(context.Background(), "", "", "run")
	if err != nil {
		t.Fatal(err)
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(files) != 3 {
		t.Fatalf("gaps must retain singles: %v", files)
	}
	for _, plan := range result.Publish.Volumes {
		if plan.First == 1 && reflect.DeepEqual(plan.Missing, []int{2}) && plan.Status == "open" {
			return
		}
	}
	t.Fatalf("missing chapter was not explained: %+v", result.Publish.Volumes)
}

func TestCompleteVolumeReplacesItsPreviouslyPublishedChapters(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	upstream.docs["alpha"] = docs[:1]
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(files) != 1 || filepath.Base(files[0]) != "alpha-saga-ch0001.epub" {
		t.Fatalf("open range must remain a single: %v", files)
	}
	upstream.docs["alpha"] = docs
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Publishers[0].Path, ".epub")
	if len(files) != 1 || filepath.Base(files[0]) != "alpha-saga-vol01.epub" {
		t.Fatalf("completed range must replace singles: %v", files)
	}
	nav := string(epubNavigation(t, files[0]))
	if !strings.Contains(nav, "Alpha Saga - Chapter 1") || !strings.Contains(nav, "Alpha Saga - Chapter 2") {
		t.Fatalf("volume TOC lost chapters: %s", nav)
	}
}

func TestCollisionNamesConvergeAcrossIncrementalAndReversedDiscovery(t *testing.T) {
	var outcomes [][]string
	for _, incremental := range []bool{true, false} {
		s, upstream := newReaderService(t)
		docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
		docs[1].Normalized.Title = "Alpha Saga Chapter 1 duplicate"
		if incremental {
			upstream.docs["alpha"] = docs[:1]
			if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
				t.Fatal(err)
			}
		}
		upstream.docs["alpha"] = []provider.ReleaseDocument{docs[1], docs[0]}
		if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, file := range findFiles(t, s.Config.Publishers[0].Path, ".html") {
			names = append(names, filepath.Base(file))
		}
		if len(names) != 2 {
			t.Fatalf("collision lost a chapter: %v", names)
		}
		for _, name := range names {
			if name == "alpha-saga-ch0001.html" {
				t.Fatalf("collision left an arrival-dependent unsuffixed name: %v", names)
			}
		}
		outcomes = append(outcomes, names)
	}
	if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
		t.Fatalf("discovery order changes names: %v", outcomes)
	}
}

func TestOfflineRebuildRenamesChaptersAndRetiresOldPaths(t *testing.T) {
	s, _ := newReaderService(t)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	s.Config.Series[0].Title = "Renamed Saga"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	s.Providers = provider.NewRegistry()
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "run --rebuild"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range findFiles(t, s.Config.Publishers[0].Path, ".html") {
		names = append(names, filepath.Base(file))
	}
	want := []string{"renamed-saga-ch0001.html", "renamed-saga-ch0002.html"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("rebuild output: %v, want %v", names, want)
	}
}

func TestRebuildRetiresLegacyRecordsForMultipleEditionsAtOnePath(t *testing.T) {
	s, upstream := newReaderService(t)
	ctx := context.Background()
	if _, err := s.RunOnce(ctx, "", "", "initial edition"); err != nil {
		t.Fatal(err)
	}
	original, err := s.ListPublishRecords(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	upstream.docs["alpha"][0].Normalized.TextHTML = "<p>Corrected chapter</p>"
	if _, err := s.RunOnce(ctx, "", "", "corrected edition"); err != nil {
		t.Fatal(err)
	}
	// Older installations retained published records for overwritten editions.
	for _, old := range original {
		old.Record.Status = domain.PublishStatusPublished
		if err := s.Repo.UpsertPublishRecord(ctx, old.Record); err != nil {
			t.Fatal(err)
		}
	}
	s.Config.Series[0].Title = "Renamed Saga"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	s.Providers = provider.NewRegistry()
	if _, err := s.Rebuild(ctx, app.RebuildOptions{DryRun: true}, "preview legacy rename"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rebuild(ctx, app.RebuildOptions{}, "apply legacy rename"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Publishers[0].Path, ".html")
	if len(files) != 2 {
		t.Fatalf("got %d files, want both renamed chapters", len(files))
	}
	for _, file := range files {
		if !strings.HasPrefix(filepath.Base(file), "renamed-saga-") {
			t.Fatalf("legacy path was retained: %s", file)
		}
	}
}

func TestPublicationPreservesUserModifiedDestination(t *testing.T) {
	s, upstream := newReaderService(t)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-ch0001.html")
	if err := os.WriteFile(path, []byte("my edited chapter"), 0o644); err != nil {
		t.Fatal(err)
	}
	upstream.docs["alpha"][0].Normalized.TextHTML = "<p>New upstream edition</p>"
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err == nil {
		t.Fatal("user-modified destination must block replacement")
	}
	if got := string(mustReadFile(t, path)); got != "my edited chapter" {
		t.Fatalf("overwrote user file: %s", got)
	}
}

func TestChapterCorrectionRetainsPreviousArchivedEdition(t *testing.T) {
	s, upstream := newReaderService(t)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	records, err := s.ListPublishRecords(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, record := range records {
		before[record.Artifact.StorageRef] = mustReadFile(t, record.Artifact.StorageRef)
	}
	upstream.docs["alpha"][0].Normalized.TextHTML = "<p>Corrected chapter</p>"
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	for path, content := range before {
		if !bytes.Equal(mustReadFile(t, path), content) {
			t.Fatalf("correction overwrote archived edition %s", path)
		}
	}
}

func TestChapterNamesUseBoundedMarkersAndNoRoutineIdentitySuffix(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"][0].Normalized.Title = "Alpha Saga Chaper 790"
	upstream.docs["alpha"][1].Normalized.Title = "Alpha Saga Challenge 2"
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range findFiles(t, s.Config.Publishers[0].Path, ".html") {
		names = append(names, filepath.Base(file))
	}
	want := []string{"alpha-saga-2026-04-02-alpha-saga-challenge-2.html", "alpha-saga-ch0790.html"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("chapter names: %v, want %v", names, want)
	}
}

func TestPublishedChaptersHaveDistinctTitlesAndSeriesPositions(t *testing.T) {
	s, _ := newReaderService(t)
	s.Config.Series[0].Output.Format = "epub"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Publishers[0].Path, ".epub")
	if len(files) != 2 {
		t.Fatalf("expected two chapter EPUBs, got %v", files)
	}
	want := map[string]string{"Alpha Saga - Chapter 1": "1", "Alpha Saga - Chapter 2": "2"}
	for _, file := range files {
		var pkg struct {
			Metadata struct {
				Title   string `xml:"title"`
				Creator string `xml:"creator"`
				Date    string `xml:"date"`
				Meta    []struct {
					Property string `xml:"property,attr"`
					Name     string `xml:"name,attr"`
					Content  string `xml:"content,attr"`
					Value    string `xml:",chardata"`
				} `xml:"meta"`
			} `xml:"metadata"`
		}
		if err := xml.Unmarshal(epubEntry(t, file, ".opf"), &pkg); err != nil {
			t.Fatal(err)
		}
		position, ok := want[pkg.Metadata.Title]
		if !ok {
			t.Fatalf("chapter title is not distinguishable: %q", pkg.Metadata.Title)
		}
		delete(want, pkg.Metadata.Title)
		if pkg.Metadata.Creator != "Alpha Author" || pkg.Metadata.Date == "" {
			t.Fatalf("missing chapter author/date: %+v", pkg.Metadata)
		}
		values := map[string]string{}
		for _, meta := range pkg.Metadata.Meta {
			values[meta.Property] = meta.Value
			if meta.Name != "" {
				values[meta.Name] = meta.Content
			}
		}
		for key, expected := range map[string]string{
			"belongs-to-collection": "Alpha Saga", "collection-type": "series", "group-position": position,
			"calibre:series": "Alpha Saga", "calibre:series_index": position,
		} {
			if values[key] != expected {
				t.Fatalf("%s: %s = %q, want %q", file, key, values[key], expected)
			}
		}
	}
}

func epubNavigation(t *testing.T, filename string) []byte {
	t.Helper()
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(epubEntry(t, filename, "META-INF/container.xml"), &container); err != nil {
		t.Fatal(err)
	}
	if len(container.Rootfiles) != 1 {
		t.Fatal("expected one EPUB package")
	}
	packagePath := container.Rootfiles[0].FullPath
	var pkg struct {
		Items []struct {
			Href       string `xml:"href,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"manifest>item"`
	}
	if err := xml.Unmarshal(epubEntry(t, filename, packagePath), &pkg); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, item := range pkg.Items {
		if !strings.Contains(" "+item.Properties+" ", " nav ") {
			continue
		}
		entry := filepath.ToSlash(filepath.Join(filepath.Dir(packagePath), item.Href))
		for _, file := range reader.File {
			if file.Name != entry {
				continue
			}
			stream, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(stream)
			_ = stream.Close()
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	t.Fatal("EPUB has no declared navigation document")
	return nil
}

func epubEntry(t *testing.T, filename, suffix string) []byte {
	t.Helper()
	z, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	for _, f := range z.File {
		if !strings.HasSuffix(f.Name, suffix) {
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	t.Fatalf("%s missing entry ending %s", filename, suffix)
	return nil
}

func TestPublicationFailurePreservesSuccessfulDeliveryAndReportsFailedRun(t *testing.T) {
	s, _ := newReaderService(t)
	s.Config.Publishers = append(s.Config.Publishers, config.PublisherConfig{
		ID: "broken-hook", Kind: "exec", Command: []string{"false"}, Enabled: true,
	})
	result, err := s.RunOnce(context.Background(), "", "", "run")
	if err == nil {
		t.Fatal("run with a failing publisher must return an error")
	}
	if result.Publish.Failed != 2 || result.Publish.Published != 2 {
		t.Fatalf("must retain both successful and failed delivery results: %+v", result.Publish)
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".html"); len(files) != 2 {
		t.Fatalf("successful target lost chapters: %v", files)
	}
	run, err := s.ExplainRun(context.Background(), result.Publish.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Run.Status != domain.RunStatusFailed {
		t.Fatalf("failed delivery recorded as %s", run.Run.Status)
	}
}

func newReaderService(t *testing.T) (*app.Service, *stubRuleAuthoringProvider) {
	t.Helper()
	upstream := newStubRuleAuthoringProvider()
	s := newRuleAuthoringService(t, upstream)
	s.Config.Sources = []config.SourceConfig{upstream.suggestions[0].Source}
	s.Config.Publishers = []config.PublisherConfig{{
		ID: "library", Kind: "filesystem", Path: filepath.Join(s.Roots.StateDir, "library"), Enabled: true,
	}}
	s.Config.Series = []config.SeriesConfig{{
		ID: "alpha-saga", Title: "Alpha Saga", Authors: []string{"Alpha Author"},
		Output: config.SeriesOutputConfig{Format: "preserve", PrefaceMode: "none"},
		Inputs: []config.SeriesInputConfig{{
			Source: "alpha", Priority: 10, MatchType: "title_regex", MatchValue: "^Alpha Saga",
			ReleaseRole: "chapter", ContentStrategy: "text_post",
		}},
	}}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	return s, upstream
}

func TestBookLabelNeedsAnExplicitEndpointBeforeVolumePublication(t *testing.T) {
	s, upstream := newReaderService(t)
	zero := 0
	s.Config.Defaults.MinBodyChars = &zero
	s.Config.Series[0].Source = "alpha"
	s.Config.Series[0].Inputs = nil
	s.Config.Series[0].Books = []config.BookConfig{{ID: "arrival", Number: 1, Collection: &config.CollectionSelector{Name: "Arrival"}}}
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 50}
	s.Config.Rules = nil
	s.Config.Rules = s.Config.CompileRules()
	upstream.docs["alpha"] = upstream.docs["alpha"][:2]
	for i := range upstream.docs["alpha"] {
		upstream.docs["alpha"][i].Normalized.Collections = []string{"Arrival"}
	}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	volumes, err := s.Repo.ListVolumeEditions(context.Background())
	if err != nil || len(volumes) != 0 {
		t.Fatalf("label invented a completed book: %+v %v", volumes, err)
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(files) != 2 {
		t.Fatalf("open book did not retain singles: %v", files)
	}
	s.Config.Series[0].Books[0].LastChapter = 2
	s.Config.Rules = nil
	s.Config.Rules = s.Config.CompileRules()
	s.Providers = provider.NewRegistry()
	preview, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
	if err != nil || len(preview.Publish.Volumes) != 1 || preview.Publish.Volumes[0].Status != "complete" {
		t.Fatalf("declared coverage did not complete: %+v %v", preview, err)
	}
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
		t.Fatal(err)
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(files) != 1 {
		t.Fatalf("completed book did not replace singles: %v", files)
	}
}

func TestCapturedAttachmentIdentityIsStableInTheRebuildPlan(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Inputs[0].ContentStrategy = "attachment_only"
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	path := filepath.Join(t.TempDir(), "Alpha Chapter 1.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\nFictional captured attachment\n%%EOF"), 0600); err != nil {
		t.Fatal(err)
	}
	upstream.docs["alpha"][0].Normalized.Attachments = []domain.Attachment{{FileName: filepath.Base(path), LocalPath: path, MIMEType: "application/pdf"}}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	plan, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Publish.Items) != 1 || plan.Publish.Items[0].Action != "unchanged" {
		t.Fatalf("capturing a digest changed the selected-content decision: %+v", plan)
	}
}

func TestLegacyContentSelectionDoesNotInventLibraryReplacements(t *testing.T) {
	s, _ := newReaderService(t)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.Repo.ListPublishCandidates(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		raw := mustReadFile(t, candidate.Artifact.MetadataRef)
		var meta map[string]any
		if err := json.Unmarshal(raw, &meta); err != nil {
			t.Fatal(err)
		}
		delete(meta["decision"].(map[string]any), "selected_content")
		raw, err = json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(candidate.Artifact.MetadataRef, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range plan.Publish.Items {
		if item.Action != "unchanged" {
			t.Fatalf("legacy selection metadata invented a replacement: %+v", item)
		}
	}
}
