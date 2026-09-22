package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func TestHookRestoresPreviouslyRetiredChapter(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	root := t.TempDir()
	script := filepath.Join(root, "hook.py")
	hook := `import hashlib, json, pathlib, shutil, sys
event = json.load(sys.stdin)
root = pathlib.Path(sys.argv[1])
seen = root / 'seen.json'
output = root / 'output'
output.mkdir(exist_ok=True)
acknowledged = json.loads(seen.read_text()) if seen.exists() else []
if event['event_id'] in acknowledged:
    sys.exit(0)
if event['action'] == 'publish':
    artifact = event['artifact']
    shutil.copyfile(artifact['storage_ref'], output / artifact['filename'])
else:
    previous = event['previous']
    path = output / previous['record']['filename']
    if path.exists() and hashlib.sha256(path.read_bytes()).hexdigest() == previous['artifact']['sha256']:
        path.unlink()
acknowledged.append(event['event_id'])
seen.write_text(json.dumps(acknowledged))
`
	if err := os.WriteFile(script, []byte(hook), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Config.Publishers = []config.PublisherConfig{{ID: "hook", Kind: "exec", Enabled: true, ProtocolVersion: 2, Command: []string{"python3", script, root}}}
	original := upstream.docs["alpha"][0].Normalized.TextHTML
	var originalOutput []byte
	for i, body := range []string{original, "<p>Corrected chapter body.</p>", original, "<p>Corrected chapter body.</p>", original} {
		upstream.docs["alpha"][0].Normalized.TextHTML = body
		if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
			t.Fatalf("revision %d: %v", i, err)
		}
		files := findFiles(t, filepath.Join(root, "output"), ".html")
		if len(files) != 1 {
			t.Fatalf("revision %d left %d readable chapters, want 1", i, len(files))
		}
		content := mustReadFile(t, files[0])
		if i == 0 {
			originalOutput = content
		} else if bytes.Equal(content, originalOutput) != (body == original) {
			t.Fatalf("revision %d did not install the requested chapter", i)
		}
	}
}

func TestCyclicRegroupRecoversWithDistinctTitle(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:2]
	upstream.docs["alpha"][0].Normalized.Title = "Alpha Saga Book 1 Chapter 1"
	upstream.docs["alpha"][1].Normalized.Title = "Alpha Saga Book 2 Chapter 1"
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 50}
	s.Config.Series[0].Books = []config.BookConfig{{ID: "one", Number: 1, LastChapter: 1}, {ID: "two", Number: 2, LastChapter: 1}}

	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	oldFiles := map[string][]byte{}
	for _, path := range findFiles(t, s.Config.Publishers[0].Path, ".epub") {
		oldFiles[path] = mustReadFile(t, path)
	}
	s.Config.Series[0].SequenceOverrides = []config.SequenceOverride{{Source: "alpha", ReleaseID: "a1", BookID: "two"}, {Source: "alpha", ReleaseID: "a2", BookID: "one"}}

	result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild")
	if err == nil || len(result.Publish.Items) == 0 || !strings.Contains(result.Publish.Items[0].Message, "cyclic") {
		t.Fatalf("expected rejected cycle: %+v %v", result.Publish, err)
	}
	for path, content := range oldFiles {
		if !bytes.Equal(content, mustReadFile(t, path)) {
			t.Fatalf("rejected cycle changed %s", path)
		}
	}
	for _, title := range []string{"Alpha Recovery", "Alpha Saga"} {
		s.Config.Series[0].Title = title

		if result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
			t.Fatalf("rebuild under %q: %+v %v", title, result.Publish, err)
		}
		files := findFiles(t, s.Config.Publishers[0].Path, ".epub")
		if len(files) != 2 {
			t.Fatalf("rebuild under %q left %d files, want 2", title, len(files))
		}
		for _, path := range files {
			if !strings.Contains(string(epubEntry(t, path, ".opf")), title) {
				t.Fatalf("rebuild under %q retained old edition %s", title, path)
			}
		}
	}
}

func TestResumedRegroupKeepsReplacementBlockedUntilItsDependencyDelivers(t *testing.T) {
	s, upstream := newReaderService(t)
	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:3]...)
	// The third fixture post is a note with no body and never materializes;
	// make it a real chapter so the regroup has four materialized releases.
	three := docs[2]
	three.Normalized.Title = "Alpha Saga - Chapter 3"
	three.Normalized.PublishedAt = time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC)
	three.Normalized.TextHTML = "<p>Three</p>"
	three.RawJSON = []byte(`{"id":"a3","title":"Alpha Saga - Chapter 3"}`)
	four := three
	four.Normalized.ProviderReleaseID = "a4"
	four.Normalized.Title = "Alpha Saga - Chapter 4"
	four.Normalized.PublishedAt = time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)
	four.Normalized.TextHTML = "<p>Four</p>"
	four.RawJSON = []byte(`{"id":"a4","title":"Alpha Saga - Chapter 4"}`)
	upstream.docs["alpha"] = docs[:2]

	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}

	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	vol01 := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	old := mustReadFile(t, vol01)

	// Regroup: chapter 2 moves to the second volume while a new release takes
	// its slot, so the new first volume overwrites the old path and depends on
	// the second volume covering chapter 2 first. Sync the new releases, then
	// block the second volume's destination with a directory so its delivery
	// fails while the first volume's replacement is still pending.
	upstream.docs["alpha"] = append(append([]provider.ReleaseDocument(nil), docs[:2]...), three, four)
	s.Config.Series[0].SequenceOverrides = []config.SequenceOverride{
		{Source: "alpha", ReleaseID: "a2", Chapter: 4},
		{Source: "alpha", ReleaseID: "a4", Chapter: 2},
	}

	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	vol02 := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol02.epub")
	if err := os.MkdirAll(vol02, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err == nil {
		t.Fatal("failed volume delivery must fail the rebuild")
	}
	if !bytes.Equal(old, mustReadFile(t, vol01)) {
		t.Fatalf("replacement delivered despite its dependency failing")
	}
	// Resuming the pending plan must keep the same block: the resumed plan is
	// re-planned against the current snapshot, so its dependency map is
	// present and the replacement waits for the other volume.
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err == nil {
		t.Fatal("resumed pending delivery must fail while its dependency fails")
	}
	if !bytes.Equal(old, mustReadFile(t, vol01)) {
		t.Fatalf("resumed plan overwrote the old volume while its dependency was undelivered")
	}
	// Once the dependency can deliver, the replacement follows and the old
	// edition is superseded.
	if err := os.RemoveAll(vol02); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(old, mustReadFile(t, vol01)) {
		t.Fatal("replacement never delivered after its dependency succeeded")
	}
	if _, statErr := os.Stat(vol02); statErr != nil {
		t.Fatalf("dependency volume was not delivered: %v", findFiles(t, s.Config.Publishers[0].Path, ".epub"))
	}
}

func TestPlanSavedByOlderVersionResumesAndDelivers(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:2]
	if _, err := s.Sync(context.Background(), "", false, "sync", nil); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.Repo.ListPublishCandidates(context.Background(), "")
	if err != nil || len(candidates) != 2 {
		t.Fatalf("want two synced candidates: %+v %v", candidates, err)
	}
	// A plan in the shape older versions saved: id, event scope, target and
	// candidates only. Resuming it must still deliver.
	target := s.Config.Publishers[0]
	plan := map[string]any{
		"id":          "delivery_old",
		"event_scope": "delivery_old",
		"target":      target,
		"candidates":  candidates,
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Config.Runtime.ArtifactRoot, "deliveries")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(dir, "delivery_old.json")
	if err := os.WriteFile(payload, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Repo.SavePendingPublish(context.Background(), domain.PendingPublish{ID: "delivery_old", TargetID: target.ID, PayloadRef: payload}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(context.Background(), "", "", false, "publish"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Publishers[0].Path, ".html")
	if len(files) != 2 {
		t.Fatalf("resumed older-shape plan delivered nothing: %v", files)
	}
}

func TestDeletedDeliveredFileComesBackOnTheNextRun(t *testing.T) {
	s, _ := newReaderService(t)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Publishers[0].Path, ".html")
	if len(files) != 2 {
		t.Fatalf("expected two delivered chapters: %v", files)
	}
	gone := files[0]
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gone); err != nil {
		t.Fatalf("the planned repair must re-deliver the deleted file %s: %v", gone, err)
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".html"); len(files) != 2 {
		t.Fatalf("repair run changed the library: %v", files)
	}
}

func TestBlockedDestinationFailsTheRunAfterAResumedPlan(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:2]
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.Repo.ListPublishCandidates(context.Background(), "")
	if err != nil || len(candidates) != 2 {
		t.Fatalf("want two delivered candidates: %+v %v", candidates, err)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Artifact.Filename < candidates[j].Artifact.Filename })
	// A saved plan that covers only the first chapter resumes cleanly, so the
	// executor re-plans the full selection on its second attempt.
	target := s.Config.Publishers[0]
	plan := map[string]any{"id": "delivery_partial", "event_scope": "delivery_partial", "target": target, "candidates": candidates[:1]}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Config.Runtime.ArtifactRoot, "deliveries")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(dir, "delivery_partial.json")
	if err := os.WriteFile(payload, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Repo.SavePendingPublish(context.Background(), domain.PendingPublish{ID: "delivery_partial", TargetID: target.ID, PayloadRef: payload}); err != nil {
		t.Fatal(err)
	}
	// The user edited the second chapter's delivered file, so its destination
	// holds unowned bytes and the fresh plan blocks it.
	second := candidates[1]
	edited := filepath.Join(target.Path, second.Source.ID, second.Track.TrackKey, second.Artifact.Filename)
	content := append(mustReadFile(t, edited), []byte("user-edit")...)
	if err := os.WriteFile(edited, content, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := s.Publish(context.Background(), "", "", false, "publish")
	if err == nil {
		t.Fatalf("run with a blocked destination reported success: %+v", result)
	}
	blocked := false
	for _, item := range result.Items {
		if item.Action == "blocked" && item.TargetRef == edited {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("blocked destination was not reported: %+v", result.Items)
	}
	if !bytes.Equal(content, mustReadFile(t, edited)) {
		t.Fatal("blocked run overwrote the user's edit")
	}
	pending, err := s.Repo.GetPendingPublish(context.Background(), target.ID)
	if err != nil || pending == nil {
		t.Fatalf("blocked plan must stay pending for the fix-and-retry loop: %+v %v", pending, err)
	}
}

func TestLegacyHookKeepsSameNameCorrectionRecordsPublished(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:2]
	log := filepath.Join(t.TempDir(), "legacy.jsonl")
	s.Config.Publishers = []config.PublisherConfig{{ID: "legacy", Kind: "exec", Enabled: true, Command: []string{"sh", "-c", "cat >> \"$1\"; echo >> \"$1\"", "hook", log}}}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	// A corrected body keeps the chapter's title and therefore its filename;
	// a legacy single-file hook receives the new file but has no supersede
	// event, so the old delivery record must stay published rather than be
	// marked superseded behind the hook's back.
	upstream.docs["alpha"][0].Normalized.TextHTML = "<p>Corrected opening.</p>"
	upstream.docs["alpha"][0].Normalized.EditedAt = upstream.docs["alpha"][0].Normalized.EditedAt.Add(time.Hour)
	dry, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range dry.Publish.Items {
		if item.Action == "retire" {
			t.Fatalf("dry run planned a retirement a legacy hook cannot perform: %+v", dry.Publish.Items)
		}
	}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	records, err := s.Repo.ListPublishRecords(context.Background(), "", "legacy")
	if err != nil {
		t.Fatal(err)
	}
	published := 0
	for _, record := range records {
		if record.Record.Status == domain.PublishStatusSuperseded {
			t.Fatalf("legacy hook record marked superseded without a supersede event: %+v", record.Record)
		}
		if record.Record.Status == domain.PublishStatusPublished {
			published++
		}
	}
	if published != 3 {
		t.Fatalf("want the two originals and the correction published: %+v", records)
	}
	if strings.Contains(string(mustReadFile(t, log)), `"supersede"`) {
		t.Fatal("legacy hook received a supersede event")
	}
}

func TestLegacyRuleRoutedVolumeUsesHoldAwareHistory(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	// Route the source into the volume-bundled series through a legacy rule
	// rather than a series input; both styles must reach the same history.
	s.Config.Series[0].Inputs = nil
	s.Config.Rules = []config.RuleConfig{{Source: "alpha", TrackKey: "alpha-saga", TrackName: "Alpha Saga", MatchType: "fallback", ReleaseRole: "chapter", ContentStrategy: "text_post", OutputFormat: "epub", CanonicalAuthor: "Alpha Author"}}
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	if files := findFiles(t, s.Config.Publishers[0].Path, ".epub"); len(files) != 1 {
		t.Fatalf("first run should publish the assigned chapter as a single: %v", files)
	}
	s.Config.Rules[0].HoldCandidates = true
	upstream.docs["alpha"] = []provider.ReleaseDocument{}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	// A standalone publish builds the volume histories itself: the held first
	// chapter must fill no slot even though it now closes the declared range.
	s.Config.Series[0].Output.FinalChapter = 1
	if _, err := s.Publish(context.Background(), "", "", false, "publish"); err != nil {
		t.Fatal(err)
	}
	if volumes, err := s.Repo.ListVolumeEditions(context.Background()); err != nil || len(volumes) != 0 {
		t.Fatalf("legacy-routed source was decided without its hold-aware history: %+v %v", volumes, err)
	}
}
