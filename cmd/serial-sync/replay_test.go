package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

func TestPreviewUsesMainConfigAndComparesFixedStoredCorpus(t *testing.T) {
	root, fixture, path, base := setupReplayFixture(t)
	write := func(path, body string) { t.Helper(); writeReplayFile(t, path, body) }
	write(filepath.Join(root, "artifacts", "obsolete.normalized.json"), "not current catalog data")
	type preview struct {
		TotalPosts     int               `json:"total_posts"`
		Materializable int               `json:"materializable"`
		Binding        app.ReplayBinding `json:"binding"`
		Comparison     struct {
			Classification  []json.RawMessage            `json:"classification"`
			OutputPolicy    []json.RawMessage            `json:"output_policy"`
			RequiresRebuild bool                         `json:"requires_rebuild"`
			AppliedLibrary  app.AppliedLibraryComparison `json:"applied_library"`
		} `json:"comparison"`
	}
	for _, input := range [][]string{{"--stored"}, {"--workspace", filepath.Join(fixture, "dump")}} {
		before := treeHashes(t, root)
		output, err := captureRun(t, append([]string{"--config", path, "setup", "preview", "--format", "json", "--show-posts"}, input...)...)
		if err != nil {
			t.Fatal(err)
		}
		var got preview
		if err := json.Unmarshal([]byte(output), &got); err != nil {
			t.Fatal(err)
		}
		if got.Binding.ConfigHash == "" || got.Binding.CaptureHash == "" || got.Binding.SoftwareVersion == "" {
			t.Fatalf("missing replay binding: %s", output)
		}
		if got.TotalPosts != 4 || got.Materializable != 2 {
			t.Fatalf("preview did not use main config/catalog: %s", output)
		}
		if !reflect.DeepEqual(before, treeHashes(t, root)) {
			t.Fatal("preview wrote state")
		}
	}
	for _, tc := range []struct {
		name, body    string
		class, policy int
	}{
		{"identical", base, 0, 0},
		{"author", strings.Replace(base, `authors=["Fictional Author"]`, `authors=["Another Author"]`, 1), 0, 2},
		{"format", strings.Replace(base, `format="preserve"`, `format="epub"`, 1), 0, 2},
		{"disabled", strings.Replace(base, "enabled=true", "enabled=false", 1), 2, 4},
		{"disabled destination", strings.Replace(base, "path="+fmt.Sprintf("%q", filepath.Join(root, "published"))+"\nenabled=true", "path="+fmt.Sprintf("%q", filepath.Join(root, "published"))+"\nenabled=false", 1), 0, 4},
		{"removed source", base[:strings.Index(base, "[[sources]]")] + `[[sources]]
id="unused"
provider="patreon"
url="https://example.invalid/unused"
enabled=false
` + base[strings.Index(base, "[[publishers]]"):strings.Index(base, "[[series]]")], 2, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := filepath.Join(root, "candidate.toml")
			write(candidate, tc.body)
			before := treeHashes(t, root)
			output, err := captureRun(t, "--config", candidate, "setup", "preview", "--stored", "--compare", path, "--format", "json")
			if err != nil {
				t.Fatal(err)
			}
			var got preview
			if err := json.Unmarshal([]byte(output), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Comparison.Classification) != tc.class || len(got.Comparison.OutputPolicy) != tc.policy {
				t.Fatalf("wrong diff partitions: %s", output)
			}
			if strings.HasPrefix(tc.name, "disabled") || tc.name == "removed source" {
				if got.Comparison.RequiresRebuild || len(got.Comparison.AppliedLibrary.Candidate.Actions) != 0 {
					t.Fatalf("excluded scope requires impossible rebuild: %s", output)
				}
			}
			if !reflect.DeepEqual(before, treeHashes(t, root)) {
				t.Fatal("comparison wrote state")
			}
		})
	}
}

func setupReplayFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("SERIAL_SYNC_CONTAINER", "false")
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	original, err := filepath.Abs("../../testdata/fixtures/authoring")
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "fixture")
	if err := os.CopyFS(fixture, os.DirFS(original)); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf(`[runtime]
store_dsn=%q
artifact_root=%q
log_root=%q
support_root=%q
[[auth_profiles]]
id="fixture"
provider="patreon"
mode="fixture"
[[sources]]
id="fictional-author"
provider="patreon"
url="https://www.patreon.com/c/FictionalAuthor/posts"
auth_profile="fixture"
fixture_dir=%q
enabled=true
[[sources]]
id="unused"
provider="patreon"
url="https://example.invalid/unused"
enabled=false
[[publishers]]
id="library"
kind="filesystem"
path=%q
enabled=true
[[series]]
id="harbor"
title="The Glass Harbor"
authors=["Fictional Author"]
[series.output]
format="preserve"
[[series.inputs]]
source="fictional-author"
match_type="title_regex"
match_value="^Harbor"
release_role="chapter"
content_strategy="text_post"
`, filepath.Join(root, "state.db"), filepath.Join(root, "artifacts"), filepath.Join(root, "logs"), filepath.Join(root, "support"), filepath.Join(fixture, "provider"), filepath.Join(root, "published"))
	path := filepath.Join(root, "config.toml")
	writeReplayFile(t, path, base)
	if _, err := captureRun(t, "--config", path, "run"); err != nil {
		t.Fatal(err)
	}
	return root, fixture, path, base
}
func writeReplayFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCandidatesSurviveRunsAndReopenOnlyForNewEvidence(t *testing.T) {
	root, fixture, path, _ := setupReplayFixture(t)
	list := func() []domain.DiscoveryCandidate {
		t.Helper()
		before := treeHashes(t, root)
		output, err := captureRun(t, "--config", path, "setup", "candidates", "--format", "json")
		if err != nil {
			t.Fatal(err)
		}
		var candidates []domain.DiscoveryCandidate
		if err := json.Unmarshal([]byte(output), &candidates); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, treeHashes(t, root)) {
			t.Fatal("listing candidates wrote state")
		}
		return candidates
	}
	initial := list()
	var candidate domain.DiscoveryCandidate
	for _, item := range initial {
		for _, id := range item.MemberReleaseIDs {
			if id == "3" {
				candidate = item
			}
		}
	}
	if candidate.ID == "" {
		t.Fatalf("first new story was not reported: %+v", initial)
	}
	if _, err := captureRun(t, "--config", path, "setup", "candidates", "dismiss", candidate.ID, "--reason", "A deliberate repost"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "--config", path, "run"); err != nil {
		t.Fatal(err)
	}
	for _, item := range list() {
		if item.ID == candidate.ID && (item.Status != "resolved" || !item.FirstObserved.Equal(candidate.FirstObserved)) {
			t.Fatalf("unchanged lookback reopened or lost first observation: %+v", item)
		}
	}
	before := treeHashes(t, root)
	if _, err := captureRun(t, "--config", path, "setup", "preview", "--stored", "--format", "json"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, treeHashes(t, root)) {
		t.Fatal("preview changed dismissal state")
	}
	raw, err := os.ReadFile(filepath.Join(fixture, "provider", "posts", "3.json"))
	if err != nil {
		t.Fatal(err)
	}
	newer := strings.ReplaceAll(string(raw), `"id": "3"`, `"id": "5"`)
	newer = strings.ReplaceAll(newer, "Chapter 1", "Chapter 2")
	newer = strings.ReplaceAll(newer, "2026-01-03", "2026-06-03")
	writeReplayFile(t, filepath.Join(fixture, "provider", "posts", "5.json"), newer)
	output, err := captureRun(t, "--config", path, "run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, candidate.ID) {
		t.Fatalf("changed candidate absent from run summary: %s", output)
	}
	found := false
	for _, item := range list() {
		if item.ID == candidate.ID {
			found = true
			if item.Status == "resolved" || len(item.MemberReleaseIDs) != 2 || !item.FirstObserved.Equal(candidate.FirstObserved) {
				t.Fatalf("new member did not reopen stable candidate: %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("candidate identity changed across restart")
	}
}

func TestComparisonReportsBodyVersusAttachmentSelection(t *testing.T) {
	root, fixture, path, base := setupReplayFixture(t)
	postsPath := filepath.Join(fixture, "dump", "posts.ndjson")
	data, err := os.ReadFile(postsPath)
	if err != nil {
		t.Fatal(err)
	}
	var records []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var record struct {
			Normalized domain.NormalizedRelease `json:"normalized"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		record.Normalized.Attachments = []domain.Attachment{{FileName: "story.epub"}}
		encoded, _ := json.Marshal(record)
		records = append(records, string(encoded))
	}
	writeReplayFile(t, postsPath, strings.Join(records, "\n"))
	writeReplayFile(t, path, strings.Replace(base, `content_strategy="text_post"`, `content_strategy="text_plus_attachments"`, 1))
	candidate := filepath.Join(root, "candidate.toml")
	writeReplayFile(t, candidate, strings.Replace(base, `content_strategy="text_post"`, `content_strategy="attachment_only"`, 1))
	output, err := captureRun(t, "--config", candidate, "setup", "preview", "--workspace", filepath.Join(fixture, "dump"), "--compare", path, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result app.RulesPreviewResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Comparison == nil || len(result.Comparison.Classification) != 2 || !result.Comparison.RequiresRebuild {
		t.Fatalf("content change disappeared: %s", output)
	}
	for _, change := range result.Comparison.Classification {
		if change.Before.SelectedContent.Kind != "body" || change.After.SelectedContent.Kind != "attachment" {
			t.Fatalf("wrong canonical content explanation: %+v", change)
		}
	}
}

func TestDiscoveryReportsFetchedPostsWhenMaterializationFails(t *testing.T) {
	root, fixture, path, base := setupReplayFixture(t)
	raw, err := os.ReadFile(filepath.Join(fixture, "provider", "posts", "1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var post map[string]any
	if err := json.Unmarshal(raw, &post); err != nil {
		t.Fatal(err)
	}
	data := post["data"].(map[string]any)
	data["attributes"].(map[string]any)["published_at"] = "2026-09-01T00:00:00Z"
	data["relationships"].(map[string]any)["attachments_media"] = map[string]any{"data": []any{map[string]any{"type": "media", "id": "missing"}}}
	post["included"] = append(post["included"].([]any), map[string]any{"type": "media", "id": "missing", "attributes": map[string]any{"file_name": "missing.epub", "mimetype": "application/epub+zip", "download_url": "https://example.invalid/missing.epub"}})
	encoded, _ := json.Marshal(post)
	writeReplayFile(t, filepath.Join(fixture, "provider", "posts", "1.json"), string(encoded))
	newPost := strings.ReplaceAll(string(raw), `"id": "1"`, `"id": "5"`)
	newPost = strings.ReplaceAll(newPost, "Harbor - Chapter 1", "Star Garden - Chapter 1")
	newPost = strings.ReplaceAll(newPost, "2026-01-01", "2026-01-05")
	writeReplayFile(t, filepath.Join(fixture, "provider", "posts", "5.json"), newPost)
	writeReplayFile(t, path, strings.Replace(base, `content_strategy="text_post"`, `content_strategy="attachment_only"`, 1))
	output, err := captureRun(t, "--config", path, "run")
	if err == nil {
		t.Fatalf("missing attachment should fail materialization: %s", output)
	}
	listed, err := captureRun(t, "--config", path, "setup", "candidates", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var candidates []domain.DiscoveryCandidate
	if err := json.Unmarshal([]byte(listed), &candidates); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range candidates {
		for _, id := range candidate.MemberReleaseIDs {
			if id == "5" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("materialization failure hid fetched evidence: %s", listed)
	}
	before := treeHashes(t, root)
	preview, err := captureRun(t, "--config", path, "setup", "preview", "--stored", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var replay app.RulesPreviewResult
	if err := json.Unmarshal([]byte(preview), &replay); err != nil {
		t.Fatal(err)
	}
	if replay.TotalPosts != 4 {
		t.Fatalf("fixture did not fail before the new post reached the catalog: %s", preview)
	}
	for _, candidate := range replay.Candidates {
		for _, id := range candidate.MemberReleaseIDs {
			if id == "5" && candidate.Status == "resolved" {
				t.Fatalf("missing captured input falsely resolved a candidate: %+v", candidate)
			}
		}
	}
	if !reflect.DeepEqual(before, treeHashes(t, root)) {
		t.Fatal("replay wrote state")
	}
}

func TestGroupedAuthoringPreviewAndRebuildAgree(t *testing.T) {
	root, _, path, base := setupReplayFixture(t)
	candidate := strings.Replace(base, "match_type=\"title_regex\"\nmatch_value=\"^Harbor\"", "collections=[\"Harbor\"]\nmin_body_chars=1500", 1)
	candidate += "\n[[overrides]]\nsource=\"fictional-author\"\nrelease_id=\"3\"\nseries=\"harbor\"\nreason=\"Confirmed bonus fiction\"\n"
	writeReplayFile(t, path, candidate)
	before := treeHashes(t, root)
	output, err := captureRun(t, "--config", path, "setup", "preview", "--stored", "--suggest", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var preview app.RulesPreviewResult
	if err := json.Unmarshal([]byte(output), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Materializable != 3 {
		t.Fatalf("preview did not explain guarded labels and override: %s", output)
	}
	if !reflect.DeepEqual(before, treeHashes(t, root)) {
		t.Fatal("suggestions wrote config/state")
	}
	if _, err := captureRun(t, "--config", path, "run", "--rebuild"); err != nil {
		t.Fatal(err)
	}
	repo, err := sqlite.OpenReadOnly(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	releases, err := repo.ListReleases(context.Background(), "fictional-author")
	if err != nil {
		t.Fatal(err)
	}
	for _, release := range releases {
		bundle, err := repo.GetReleaseBundle(context.Background(), release.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := "harbor"
		if release.ProviderReleaseID == "4" {
			want = "unmatched"
		}
		if bundle.Track.TrackKey != want {
			t.Fatalf("run assigned %s to %s, want %s", release.ProviderReleaseID, bundle.Track.TrackKey, want)
		}
	}
}

func TestStandalonePreviewInheritsMainOutputDefaults(t *testing.T) {
	_, fixture, path, base := setupReplayFixture(t)
	writeReplayFile(t, path, "[defaults]\nformat=\"epub\"\n"+base)
	series := `[[series]]
id="harbor"
title="Harbor"
source="fictional-author"
[series.output]
bundling="volume"
chapters_per_volume=2
[[series.inputs]]
title_patterns=["^Harbor"]
`
	writeReplayFile(t, filepath.Join(fixture, "dump", "series.toml"), series)
	output, err := captureRun(t, "--config", path, "setup", "preview", "--workspace", filepath.Join(fixture, "dump"), "--series-file", "series.toml", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result app.RulesPreviewResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	if result.Materializable != 2 || result.SourcesFileHash == "" {
		t.Fatalf("standalone defaults/binding missing: %s", output)
	}
}

func TestOfflineEnrichmentPreservesCapturedBytesAndPublication(t *testing.T) {
	root, _, path, _ := setupReplayFixture(t)
	repo, err := sqlite.OpenReadOnly(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	releases, err := repo.ListReleases(context.Background(), "fictional-author")
	if err != nil {
		t.Fatal(err)
	}
	contentHashes := map[string]string{}
	for _, release := range releases {
		contentHashes[release.ProviderReleaseID] = release.ContentHash
	}
	repo.Close()
	published := treeHashes(t, filepath.Join(root, "published"))
	artifacts := treeHashes(t, filepath.Join(root, "artifacts"))
	if _, err := captureRun(t, "--config", path, "setup", "enrich"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(artifacts, treeHashes(t, filepath.Join(root, "artifacts"))) || !reflect.DeepEqual(published, treeHashes(t, filepath.Join(root, "published"))) {
		t.Fatal("enrichment rewrote captured or published bytes")
	}
	if _, err := captureRun(t, "--config", path, "run"); err != nil {
		t.Fatal(err)
	}
	repo, err = sqlite.OpenReadOnly(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	current, err := repo.ListReleases(context.Background(), "fictional-author")
	if err != nil {
		t.Fatal(err)
	}
	for _, release := range current {
		if release.ContentHash != contentHashes[release.ProviderReleaseID] {
			t.Fatal("enrichment changed content hash")
		}
	}
	if !reflect.DeepEqual(published, treeHashes(t, filepath.Join(root, "published"))) {
		t.Fatal("enrichment republished unchanged content")
	}
	labels, err := repo.ListLabelObservations(context.Background())
	if err != nil || len(labels) == 0 {
		t.Fatalf("durable identity observations missing: %+v %v", labels, err)
	}
}

func TestHeldReplayAndNewRunDecisionAgree(t *testing.T) {
	_, fixture, path, base := setupReplayFixture(t)
	base = strings.Replace(base, "enabled=true", "ignore_labels=[\"Harbor\"]\nenabled=true", 1)
	base = strings.Replace(base, "match_type=\"title_regex\"\nmatch_value=\"^Harbor\"", "match_type=\"fallback\"\nhold_candidates=true", 1)
	writeReplayFile(t, path, base)
	raw, err := os.ReadFile(filepath.Join(fixture, "provider", "posts", "1.json"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := strings.ReplaceAll(string(raw), `"id": "1"`, `"id": "5"`)
	fresh = strings.ReplaceAll(fresh, "Harbor - Chapter 1", "Star Garden - Chapter 1")
	fresh = strings.ReplaceAll(fresh, "2026-01-01", "2026-05-01")
	writeReplayFile(t, filepath.Join(fixture, "provider", "posts", "5.json"), fresh)
	if _, err := captureRun(t, "--config", path, "run"); err != nil {
		t.Fatal(err)
	}
	output, err := captureRun(t, "--config", path, "setup", "preview", "--stored", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var preview app.RulesPreviewResult
	if err := json.Unmarshal([]byte(output), &preview); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, creator := range preview.Creators {
		for _, post := range creator.Preview.Posts {
			if post.ProviderReleaseID == "5" {
				found = true
				if post.TrackKey != "unmatched" || len(post.Explanation.HeldReasons) == 0 {
					t.Fatalf("preview failed to hold: %+v", post)
				}
			}
		}
	}
	if !found {
		t.Fatal("new release missing")
	}
	repo, err := sqlite.OpenReadOnly(filepath.Join(filepath.Dir(path), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	release, err := repo.GetReleaseByProviderID(context.Background(), "fictional-author", "5")
	if err != nil || release == nil {
		t.Fatalf("new held release not captured: %v", err)
	}
	bundle, err := repo.GetReleaseBundle(context.Background(), release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Track.TrackKey != "unmatched" || len(bundle.Artifacts) > 0 {
		t.Fatalf("run published a held release: %+v", bundle)
	}
}

func TestMissingHistoryStopsHeldRebuildBeforePublication(t *testing.T) {
	root, _, path, base := setupReplayFixture(t)
	repo, err := sqlite.OpenReadOnly(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	releases, err := repo.ListReleases(context.Background(), "fictional-author")
	if err != nil {
		t.Fatal(err)
	}
	repo.Close()
	if err := os.Remove(releases[0].NormalizedPayloadRef); err != nil {
		t.Fatal(err)
	}
	base = strings.Replace(base, `authors=["Fictional Author"]`, `authors=["Changed Author"]`, 1)
	writeReplayFile(t, path, base+"hold_candidates=true\n")
	before := treeHashes(t, filepath.Join(root, "published"))
	_, err = captureRun(t, "--config", path, "run", "--rebuild")
	if err == nil || !strings.Contains(err.Error(), "history unavailable") {
		t.Fatalf("missing history was not blocked: %v", err)
	}
	if !reflect.DeepEqual(before, treeHashes(t, filepath.Join(root, "published"))) {
		t.Fatal("failed history analysis changed published files")
	}
}

func TestComparisonUsesTheRebuildPlannerAndReportsActualLibraryChanges(t *testing.T) {
	root, _, path, base := setupReplayFixture(t)
	candidate := filepath.Join(root, "candidate.toml")
	writeReplayFile(t, candidate, strings.Replace(base, `title="The Glass Harbor"`, `title="Harbor Renamed"`, 1))
	before := treeHashes(t, root)
	output, err := captureRun(t, "--config", candidate, "setup", "preview", "--stored", "--compare", path, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var preview app.RulesPreviewResult
	if err := json.Unmarshal([]byte(output), &preview); err != nil {
		t.Fatal(err)
	}
	comparison := preview.Comparison.AppliedLibrary
	if comparison.Status != "evaluated" || len(comparison.Baseline.Actions) != 0 {
		t.Fatalf("bad baseline: %+v", comparison)
	}
	actions := map[string]int{}
	for _, item := range comparison.Candidate.Actions {
		actions[item.Action]++
	}
	if actions["add"] != 2 || actions["retire"] != 2 {
		t.Fatalf("rename did not plan additions and retirements: %+v", comparison.Candidate)
	}
	if !reflect.DeepEqual(before, treeHashes(t, root)) {
		t.Fatal("applied-library comparison wrote files")
	}
	service, close, err := bootstrapMode(candidate, true)
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	plan, err := service.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Publish.Items, comparison.Candidate.Actions) {
		t.Fatalf("comparison differs from rebuild dry run: %+v versus %+v", plan.Publish.Items, comparison.Candidate.Actions)
	}
}
