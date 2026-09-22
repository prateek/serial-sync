package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

// The volume planner is pure: these table tests drive planSeriesVolumes with
// sequenced chapters, active editions and output config as values and need no
// store, provider or EPUB tooling.

func planRequest(series config.SeriesConfig, rebuild bool, candidates []domain.PublishCandidate, existing []domain.VolumeEdition, sequences map[string]domain.Sequence) volumeEvaluation {
	inputs := map[string]domain.NormalizedRelease{}
	histories := map[string]map[string]classify.ExplainedDecision{}
	bySource := map[string][]domain.PublishCandidate{}
	for _, candidate := range candidates {
		inputs[candidate.Release.ID] = domain.NormalizedRelease{ProviderReleaseID: candidate.Release.ProviderReleaseID}
		bySource[candidate.Source.ID] = append(bySource[candidate.Source.ID], candidate)
	}
	for source, items := range bySource {
		history := map[string]classify.ExplainedDecision{}
		for _, candidate := range items {
			seq := sequences[candidate.Release.ID]
			history[candidate.Release.ProviderReleaseID] = classify.ExplainedDecision{Decision: domain.TrackDecision{
				SeriesID:        candidate.Track.TrackKey,
				ReleaseRole:     domain.ReleaseRoleChapter,
				ContentStrategy: domain.ContentStrategyTextPost,
				OutputFormat:    domain.OutputFormatEPUB,
				Sequence:        &seq,
				Matched:         true,
			}}
		}
		histories[source] = history
	}
	sourcesByRelease := map[string]string{}
	for _, candidate := range candidates {
		sourcesByRelease[candidate.Release.ID] = candidate.Source.ID
	}
	return volumeEvaluation{
		rebuild:          rebuild,
		candidates:       candidates,
		existing:         existing,
		inputs:           inputs,
		histories:        histories,
		scope:            map[string]bool{"fictional": true},
		sourcesByRelease: sourcesByRelease,
		archiveValid:     map[string]bool{},
		config:           &config.Config{Sources: []config.SourceConfig{{ID: "fictional", Enabled: true}}},
	}
}

func planChapter(t *testing.T, seriesID, releaseID string, chapter, position int) (domain.PublishCandidate, domain.Sequence) {
	release := domain.Release{ID: releaseID, SourceID: "fictional", ProviderReleaseID: releaseID, Title: fmt.Sprintf("Chapter %d", chapter), ContentHash: "hash-" + releaseID}
	track := domain.StoryTrack{ID: "fictional/" + seriesID, SourceID: "fictional", TrackKey: seriesID, TrackName: seriesID}
	artifact := domain.Artifact{ID: "art-" + releaseID, ReleaseID: releaseID, Filename: releaseID + ".epub", MIMEType: "application/epub+zip"}
	seq := domain.Sequence{Chapter: chapter, Position: position}
	return domain.PublishCandidate{
		Source:     domain.Source{ID: "fictional", Provider: "fixture", CreatorName: "Test Author"},
		Track:      track,
		Release:    release,
		Assignment: domain.ReleaseAssignment{ReleaseID: releaseID, TrackID: track.ID, ReleaseRole: domain.ReleaseRoleChapter, Confidence: 1},
		Artifact:   artifact,
	}, seq
}

func TestPlannerCompletesFixedRangeAndKeepsNextRangeOpen(t *testing.T) {
	series := config.SeriesConfig{ID: "harbor", Title: "Harbor", Source: "fictional"}
	output := config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	var candidates []domain.PublishCandidate
	sequences := map[string]domain.Sequence{}
	for _, row := range []struct {
		id      string
		chapter int
	}{{"r1", 1}, {"r2", 2}, {"r3", 3}} {
		candidate, seq := planChapter(t, series.ID, row.id, row.chapter, row.chapter)
		candidates = append(candidates, candidate)
		sequences[row.id] = seq
	}
	result, err := planSeriesVolumes(series, output, planRequest(series, false, candidates, nil, sequences))
	plans, editions, builds, removed, failures, block := result.Plans, result.Editions, result.Builds, result.Removed, result.Failures, result.BlockSeries
	_ = removed
	if err != nil || block || len(failures) != 0 {
		t.Fatalf("plan error: %v %t %+v", err, block, failures)
	}
	if len(editions) != 1 || len(builds) != 1 {
		t.Fatalf("expected one completed range of 1-2: %+v %+v", plans, editions)
	}
	byID := map[string]domain.VolumePlan{}
	for _, plan := range plans {
		byID[plan.GroupID] = plan
	}
	if got := byID["range:1:2"].Status; got != "complete" {
		t.Fatalf("range:1:2 status = %q, want complete", got)
	}
	if got := byID["range:3:4"].Status; got != "open" {
		t.Fatalf("range:3:4 status = %q, want open", got)
	}
}

func TestPlannerFlagsGapsIntentionalsAndDuplicates(t *testing.T) {
	series := config.SeriesConfig{ID: "harbor", Title: "Harbor", Source: "fictional"}
	output := config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 4, IntentionalGaps: []config.ChapterGap{{Chapter: 3, Reason: "author skip"}}}
	var candidates []domain.PublishCandidate
	sequences := map[string]domain.Sequence{}
	for _, row := range []struct {
		id      string
		chapter int
	}{{"r1", 1}, {"r2", 2}, {"r3", 4}, {"r4", 10}, {"r5", 10}, {"r6", 2}} {
		candidate, seq := planChapter(t, series.ID, row.id, row.chapter, row.chapter)
		candidates = append(candidates, candidate)
		sequences[row.id] = seq
	}
	result, err := planSeriesVolumes(series, output, planRequest(series, false, candidates, nil, sequences))
	plans := result.Plans
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]domain.VolumePlan{}
	for _, plan := range plans {
		byID[plan.GroupID] = plan
	}
	first := byID["range:1:4"]
	if first.Status != "open" || first.Reason != "duplicate chapter numbers need an explicit selection" {
		t.Fatalf("duplicate chapter numbers not flagged: %+v", first)
	}
	if len(first.IntentionalGaps) != 1 || first.IntentionalGaps[0] != 3 {
		t.Fatalf("intentional gap not reported: %+v", first)
	}
	second := byID["range:9:12"]
	if second.Status != "open" || len(second.Missing) != 3 || second.Missing[0] != 9 || second.Missing[1] != 11 || second.Missing[2] != 12 {
		t.Fatalf("missing chapters not reported: %+v", second)
	}
}

func TestPlannerFreezesUnchangedAndRebuildsChangedEditions(t *testing.T) {
	series := config.SeriesConfig{ID: "harbor", Title: "Harbor", Source: "fictional"}
	output := config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	candidate, seq := planChapter(t, series.ID, "r1", 1, 1)
	candidate2, seq2 := planChapter(t, series.ID, "r2", 2, 2)
	two := []domain.PublishCandidate{candidate, candidate2}
	recipe := volumeRecipe(series, nil)
	existing := []domain.VolumeEdition{{
		ID: "volume_existing", SeriesID: series.ID, SourceID: "fictional", TrackID: "fictional/harbor", GroupID: "range:1:2", First: 1, Last: 2, RecipeHash: hashBytes(recipe), Active: true,
		Members:  []domain.VolumeMember{{ReleaseID: "r1", ContentHash: "hash-r1", Position: 1}, {ReleaseID: "r2", ContentHash: "hash-r2", Position: 2}},
		Artifact: domain.Artifact{ID: "volume_existing", Filename: "harbor-vol01.epub", StorageRef: "/archive.epub", SHA256: "abc"},
	}}
	request := planRequest(series, false, two, existing, map[string]domain.Sequence{"r1": seq, "r2": seq2})
	request.archiveValid["volume_existing"] = true
	result, err := planSeriesVolumes(series, output, request)
	plans, editions, builds, failures := result.Plans, result.Editions, result.Builds, result.Failures
	if err != nil || len(failures) != 0 || len(plans) != 1 {
		t.Fatalf("freeze plan: %v %+v %+v", err, failures, plans)
	}
	if plans[0].Status != "frozen" || len(editions) != 0 || len(builds) != 0 {
		t.Fatalf("unchanged edition should freeze without rebuilding: %+v %+v", plans, editions)
	}
	two[0].Release.ContentHash = "hash-r1-changed"
	request.rebuild = true
	result, err = planSeriesVolumes(series, output, request)
	plans, editions, builds, removed, failures := result.Plans, result.Editions, result.Builds, result.Removed, result.Failures
	_ = removed
	if err != nil || len(failures) != 0 {
		t.Fatalf("rebuild plan: %v %+v", err, failures)
	}
	if len(plans) != 1 || plans[0].Status != "complete" || len(editions) != 1 || len(builds) != 1 {
		t.Fatalf("rebuild should replace the edition: %+v %+v", plans, editions)
	}
	request.rebuild = false
	request.archiveValid["volume_existing"] = false
	result, err = planSeriesVolumes(series, output, request)
	plans = result.Plans
	if err != nil || len(plans) != 1 || plans[0].Status != "pending_rebuild" || !strings.Contains(plans[0].Reason, "archived volume") {
		t.Fatalf("corrupt archive should require repair: %+v", plans)
	}
}

func TestPlannerGroupsBookChaptersAndKeepsExplicitSingles(t *testing.T) {
	series := config.SeriesConfig{ID: "harbor", Title: "Harbor", Source: "fictional", Books: []config.BookConfig{{ID: "one", Number: 1, LastChapter: 2}}}
	output := config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	var candidates []domain.PublishCandidate
	sequences := map[string]domain.Sequence{}
	add := func(id string, book string, chapter int) {
		candidate, seq := planChapter(t, series.ID, id, chapter, chapter)
		seq.BookID = book
		seq.Book = 1
		candidates = append(candidates, candidate)
		sequences[id] = seq
	}
	add("r1", "one", 1)
	add("r2", "one", 2)
	// An explicitly kept single never joins a book group.
	candidate, seq := planChapter(t, series.ID, "r3", 3, 3)
	seq.KeepSingle = true
	candidates = append(candidates, candidate)
	sequences["r3"] = seq
	result, err := planSeriesVolumes(series, output, planRequest(series, false, candidates, nil, sequences))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]domain.VolumePlan{}
	for _, plan := range result.Plans {
		byID[plan.GroupID] = plan
	}
	if len(byID) != 1 {
		t.Fatalf("book chapters should group into the book: %+v", result.Plans)
	}
	if got := byID["book:one"].Status; got != "complete" {
		t.Fatalf("book:one status = %q, want complete", got)
	}
	if len(result.Editions) != 1 || result.Editions[0].GroupID != "book:one" || result.Builds[0].title != "Harbor — Book 1 (Chapters 1–2)" {
		t.Fatalf("book volume build: %+v %+v", result.Editions, result.Builds)
	}
}

func TestPlannerFinalChapterTruncationAndBundlingDisabled(t *testing.T) {
	final := config.SeriesConfig{ID: "harbor", Title: "Harbor", Source: "fictional"}
	out := config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 10, FinalChapter: 1}
	var candidates []domain.PublishCandidate
	sequences := map[string]domain.Sequence{}
	for _, row := range []struct {
		id      string
		chapter int
	}{{"r1", 1}, {"r2", 2}} {
		candidate, seq := planChapter(t, final.ID, row.id, row.chapter, row.chapter)
		candidates = append(candidates, candidate)
		sequences[row.id] = seq
	}
	result, err := planSeriesVolumes(final, out, planRequest(final, false, candidates, nil, sequences))
	if err != nil {
		t.Fatal(err)
	}
	first := result.Plans[0]
	if first.GroupID != "range:1:1" || first.Status != "complete" || len(result.Editions) != 1 || result.Editions[0].Last != 1 {
		t.Fatalf("final chapter should truncate the range: %+v %+v", result.Plans, result.Editions)
	}
	// With bundling disabled, an existing completed edition must wait for an
	// explicit rebuild and a rebuild retires it once its chapters are selected.
	recipe, _ := json.Marshal(final)
	existing := []domain.VolumeEdition{{
		ID: "volume_old", SeriesID: final.ID, SourceID: "fictional", TrackID: "fictional/harbor", GroupID: "range:1:2", First: 1, Last: 2, RecipeHash: hashBytes(recipe), Active: true,
		Members:  []domain.VolumeMember{{ReleaseID: "r1", ContentHash: "hash-r1", Position: 1}, {ReleaseID: "r2", ContentHash: "hash-r2", Position: 2}},
		Artifact: domain.Artifact{ID: "volume_old", Filename: "harbor-vol01.epub"},
	}}
	plain := config.SeriesOutputConfig{Format: "epub", Bundling: "preserve", PrefaceMode: "none"}
	request := planRequest(final, false, candidates, existing, sequences)
	request.archiveValid["volume_old"] = true
	result, err = planSeriesVolumes(final, plain, request)
	if err != nil || len(result.Plans) != 1 || result.Plans[0].Status != "pending_rebuild" || !strings.Contains(result.Plans[0].Reason, "bundling disabled") {
		t.Fatalf("disabling bundling keeps the edition until a rebuild: %+v %v", result.Plans, err)
	}
	request.rebuild = true
	result, err = planSeriesVolumes(final, plain, request)
	if err != nil || len(result.Removed) != 1 || result.Removed[0] != "range:1:2" {
		t.Fatalf("rebuild with bundling disabled retires the edition: %+v %v", result.Removed, err)
	}
}
