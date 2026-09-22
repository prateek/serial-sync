package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/sequence"
)

type volumeGroup struct {
	gaps      []config.ChapterGap
	plan      domain.VolumePlan
	title     string
	chapters  []domain.PublishCandidate
	sequences map[string]domain.Sequence
}

func (s *Service) prepareVolumes(ctx context.Context, sourceFilter, seriesFilter string, rebuild bool, blockedSeries map[string]bool, syncedHistories map[string]map[string]classify.ExplainedDecision) ([]domain.VolumePlan, error) {
	candidates, err := s.Repo.ListPublishCandidates(ctx, "")
	if err != nil {
		return nil, err
	}
	existing, err := s.Repo.ListVolumeEditions(ctx)
	if err != nil {
		return nil, err
	}
	// Histories only matter for sources feeding a volume-bundled series, and a
	// run that just decided the sources passes its decisions through instead
	// of re-deciding them here.
	volumeSources := map[string]bool{}
	rules := s.Config.Compiled()
	for _, series := range s.Config.Series {
		if s.Config.SeriesOutput(series).Bundling != "volume" {
			continue
		}
		for _, source := range rules.SourcesRoutingTo(series.ID) {
			volumeSources[source] = true
		}
	}
	inputs := map[string]domain.NormalizedRelease{}
	histories := map[string]map[string]classify.ExplainedDecision{}
	for _, candidate := range candidates {
		if !s.sourceInScope(candidate.Source.ID, sourceFilter, rebuild, s.Config) {
			continue
		}
		normalized, err := s.loadStoredNormalized(ctx, candidate.Release)
		if err == nil {
			inputs[candidate.Release.ID] = normalized
		}
		if _, ok := histories[candidate.Source.ID]; ok {
			continue
		}
		if passed, ok := syncedHistories[candidate.Source.ID]; ok {
			histories[candidate.Source.ID] = passed
		} else if volumeSources[candidate.Source.ID] {
			histories[candidate.Source.ID], err = s.authoringDecisions(ctx, s.Config, candidate.Source.ID, nil)
			if err != nil {
				return nil, err
			}
		}
	}
	plans, _, err := s.evaluateVolumes(ctx, volumeEvaluation{sourceFilter: sourceFilter, seriesFilter: seriesFilter, rebuild: rebuild, blockedSeries: blockedSeries, candidates: candidates, existing: existing, inputs: inputs, histories: histories, series: s.Config.Series}, true)
	return plans, err
}

type volumeEvaluation struct {
	sourceFilter, seriesFilter string
	rebuild                    bool
	blockedSeries              map[string]bool
	candidates                 []domain.PublishCandidate
	existing                   []domain.VolumeEdition
	inputs                     map[string]domain.NormalizedRelease
	histories                  map[string]map[string]classify.ExplainedDecision
	scope                      map[string]bool
	sourcesByRelease           map[string]string
	archiveValid               map[string]bool
	series                     []config.SeriesConfig
	config                     *config.Config
	// publication resolves the metadata a volume edition carries; the pure
	// planner accepts it rather than reading config assets itself.
	publication func(sourceID string, release domain.NormalizedRelease, decision domain.TrackDecision) (*domain.PublicationMetadata, error)
}

// volumePlanResult is the planner's one result value.
type volumePlanResult struct {
	Plans       []domain.VolumePlan
	Editions    []domain.VolumeEdition
	Builds      []volumeBuild
	Removed     []string
	Failures    []error
	BlockSeries bool
}

type volumeBuild struct {
	edition  domain.VolumeEdition
	title    string
	chapters []domain.PublishCandidate
}

// planSeriesVolumes is the pure volume planner for one series. It takes the
// sequenced chapters, active editions and the series output config as values
// and returns the open and frozen groups, the groups a rebuild would retire,
// and the completed groups as volume editions plus the builds that would
// materialize them. It performs no reads, builds or store writes; the caller
// supplies the member-to-source map and archive integrity verdicts, and
// activation materializes and stores the returned builds.
func planSeriesVolumes(series config.SeriesConfig, output config.SeriesOutputConfig, request volumeEvaluation) (volumePlanResult, error) {
	sourceFilter, seriesFilter := request.sourceFilter, request.seriesFilter
	rebuild := request.rebuild
	candidates, existing, inputs := request.candidates, request.existing, request.inputs
	cfg := request.config
	rules := cfg.Compiled()
	active := map[string]domain.VolumeEdition{}
	for _, volume := range existing {
		if volume.Active {
			active[volume.SeriesID+"/"+volume.GroupID] = volume
		}
	}
	var plans []domain.VolumePlan
	var newEditions []domain.VolumeEdition
	var builds []volumeBuild
	var removedGroups []string
	var failures []error
	if seriesFilter != "" && series.ID != seriesFilter {
		return volumePlanResult{}, nil
	}
	if sourceFilter != "" || rebuild {
		selected := false
		for _, candidate := range candidates {
			if candidate.Track.TrackKey == series.ID && request.scope[candidate.Source.ID] {
				selected = true
			}
		}
		for _, old := range active {
			if old.SeriesID == series.ID && request.scope[old.SourceID] {
				selected = true
			}
		}
		if !selected {
			return volumePlanResult{}, nil
		}
	}
	if rebuild {
		mixedScope := false
		for _, old := range active {
			if old.SeriesID != series.ID {
				continue
			}
			selected, excluded := false, false
			for _, member := range old.Members {
				memberSource := request.sourcesByRelease[member.ReleaseID]
				if request.scope[memberSource] {
					selected = true
				} else {
					excluded = true
				}
			}
			if selected && excluded {
				mixedScope = true
				plans = append(plans, domain.VolumePlan{SeriesID: series.ID, GroupID: old.GroupID, First: old.First, Last: old.Last, Filename: old.Artifact.Filename, Status: "blocked", Reason: "replacement needs excluded sources"})
				failures = append(failures, fmt.Errorf("volume %s: replacement needs excluded sources", old.GroupID))
			}
		}
		if mixedScope {
			return volumePlanResult{Plans: plans, Editions: newEditions, Builds: builds, Removed: removedGroups, Failures: failures, BlockSeries: true}, nil
		}
	}
	failureStart := len(failures)
	retainedGroups := map[string]bool{}
	if output.Bundling != "volume" {
		for _, old := range active {
			if old.SeriesID != series.ID || !request.scope[old.SourceID] {
				continue
			}
			plan := domain.VolumePlan{SeriesID: series.ID, GroupID: old.GroupID, First: old.First, Last: old.Last, Filename: old.Artifact.Filename, Status: "pending_rebuild", Reason: "bundling disabled; rebuild to publish singles"}
			if rebuild {
				ready := true
				for _, member := range old.Members {
					_, present := inputs[member.ReleaseID]
					selected := false
					for _, candidate := range candidates {
						if candidate.Release.ID == member.ReleaseID && request.scope[candidate.Source.ID] {
							selected = true
						}
					}
					if !present || !selected {
						ready = false
						break
					}
				}
				if ready {
					removedGroups = append(removedGroups, old.GroupID)
					plan.Status = "singles"
				} else {
					failures = append(failures, fmt.Errorf("volume %s cannot be replaced from the selected stored inputs", old.GroupID))
				}
			}
			plans = append(plans, plan)
		}
		return volumePlanResult{Plans: plans, Editions: newEditions, Builds: builds, Removed: removedGroups, Failures: failures}, nil
	}
	if output.Format != "epub" || output.ChaptersPerVolume < 1 {
		return volumePlanResult{}, fmt.Errorf("series %s: volume bundling requires epub and a positive chapter count", series.ID)
	}
	groups := map[string]*volumeGroup{}
	hasBooks := false
	for _, candidate := range candidates {
		if candidate.Track.TrackKey != series.ID || candidate.Assignment.ReleaseRole != domain.ReleaseRoleChapter {
			continue
		}
		var seq domain.Sequence
		if !request.scope[candidate.Source.ID] {
			seq = archivedSequence(candidate, series)
		} else {
			normalized, present := inputs[candidate.Release.ID]
			if !present {
				failures = append(failures, fmt.Errorf("missing stored input for %s", candidate.Release.ProviderReleaseID))
				continue
			}
			decision := authoringDecisionFor(candidate.Source.ID, normalized, request.histories[candidate.Source.ID], cfg, rules)
			if decision.Sequence == nil {
				// A held release is under review: its decision carries no
				// sequence. It fills no chapter slot and the range stays
				// open.
				continue
			}
			seq = *decision.Sequence
		}
		if seq.BookID != "" {
			hasBooks = true
		}
		if seq.Chapter < 1 || seq.KeepSingle {
			continue
		}
		first := 1 + (seq.Chapter-1)/output.ChaptersPerVolume*output.ChaptersPerVolume
		last := first + output.ChaptersPerVolume - 1
		if output.FinalChapter > 0 && output.FinalChapter < last && output.FinalChapter >= first {
			last = output.FinalChapter
		}
		groupID := fmt.Sprintf("range:%d:%d", first, last)
		number := (first-1)/output.ChaptersPerVolume + 1
		filename := artifact.VolumeFilename(series.Title, number)
		title := fmt.Sprintf("%s — Volume %d (Chapters %d–%d)", series.Title, number, first, last)
		reason := ""
		gaps := output.IntentionalGaps
		if seq.BookID != "" {
			groupID = "book:" + seq.BookID
			first, last = 1, 0
			gaps = nil
			for _, book := range series.Books {
				if book.ID == seq.BookID {
					gaps = book.IntentionalGaps
					first = book.FirstChapter
					if first == 0 {
						first = 1
					}
					last = book.LastChapter
					title = book.Title
					if title == "" {
						title = fmt.Sprintf("%s — Book %d (Chapters %d–%d)", series.Title, book.Number, first, last)
					}
					break
				}
			}
			filename = artifact.BookVolumeFilename(series.Title, seq.Book)
			if last < first {
				reason = "book endpoint is unknown"
			}
		}
		group := groups[groupID]
		if group == nil {
			group = &volumeGroup{plan: domain.VolumePlan{SeriesID: series.ID, GroupID: groupID, First: first, Last: last, Status: "open", Reason: reason, Filename: filename}, title: title, gaps: gaps, sequences: map[string]domain.Sequence{}}
			groups[groupID] = group
		}
		group.chapters = append(group.chapters, candidate)
		group.sequences[candidate.Release.ID] = seq
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		plan := group.plan
		if !rebuild {
			overlaps := false
			for _, old := range active {
				if old.SeriesID != series.ID || old.GroupID == key {
					continue
				}
				for _, member := range old.Members {
					if _, exists := group.sequences[member.ReleaseID]; exists {
						overlaps = true
					}
				}
			}
			if overlaps {
				plan.Status, plan.Reason = "pending_rebuild", "new grouping overlaps a frozen volume; rebuild to regroup"
				plans = append(plans, plan)
				continue
			}
		}
		complete := plan.Last >= plan.First && plan.Reason == ""
		selected, excluded := false, false
		seen := map[int]bool{}
		for _, chapter := range group.chapters {
			seq := group.sequences[chapter.Release.ID]
			if !rebuild && chapter.Artifact.MetadataRef != "" && artifact.IsLegacy(chapter.Artifact) {
				complete = false
				plan.Status, plan.Reason = "pending_rebuild", "legacy chapters require an explicit migration rebuild"
			}
			if seen[seq.Chapter] {
				complete = false
				plan.Reason = "duplicate chapter numbers need an explicit selection"
			}
			if seq.Position == 0 {
				complete = false
				plan.Reason = seq.Reason
			}
			if chapter.Artifact.MIMEType != "application/epub+zip" {
				complete = false
				plan.Reason = "chapter does not have an EPUB artifact"
			}
			seen[seq.Chapter] = true
			plan.Present = append(plan.Present, seq.Chapter)
			if request.scope[chapter.Source.ID] {
				selected = true
			} else {
				excluded = true
			}
		}
		sort.Ints(plan.Present)
		if !selected {
			continue
		}
		var notes []string
		intentional := map[int]string{}
		for _, gap := range group.gaps {
			if gap.Chapter >= plan.First && gap.Chapter <= plan.Last && gap.Reason != "" {
				intentional[gap.Chapter] = gap.Reason
			}
		}
		for n := plan.First; n <= plan.Last; n++ {
			if !seen[n] {
				if reason, ok := intentional[n]; ok {
					plan.IntentionalGaps = append(plan.IntentionalGaps, n)
					notes = append(notes, fmt.Sprintf("Chapter %d: %s", n, reason))
					continue
				}
				plan.Missing = append(plan.Missing, n)
				complete = false
			}
		}
		if hasBooks && strings.HasPrefix(key, "range:") {
			complete = false
			plan.Reason = "unassigned chapters need a book mapping"
		}
		if len(plan.Missing) > 0 {
			plan.Reason = "missing expected chapters"
		}
		if excluded {
			complete = false
			plan.Reason = "volume needs excluded sources"
			failures = append(failures, fmt.Errorf("series %s: %s", series.ID, plan.Reason))
		}
		if !complete {
			plans = append(plans, plan)
			continue
		}
		sort.Slice(group.chapters, func(i, j int) bool {
			return group.sequences[group.chapters[i].Release.ID].Chapter < group.sequences[group.chapters[j].Release.ID].Chapter
		})
		var members []domain.VolumeMember
		for _, chapter := range group.chapters {
			members = append(members, domain.VolumeMember{ReleaseID: chapter.Release.ID, ContentHash: chapter.Release.ContentHash, Position: group.sequences[chapter.Release.ID].Position})
		}
		first := group.chapters[0]
		var metadata *domain.PublicationMetadata
		if request.publication != nil {
			var metaErr error
			metadata, metaErr = request.publication(first.Source.ID, inputs[first.Release.ID], domain.TrackDecision{SeriesID: series.ID, BookID: group.sequences[first.Release.ID].BookID, OutputFormat: domain.OutputFormatEPUB})
			if metaErr != nil {
				return volumePlanResult{}, metaErr
			}
		}
		recipe := volumeRecipe(series, metadata)
		recipeHash := hashBytes(recipe)
		prior, exists := active[series.ID+"/"+key]
		archiveValid := request.archiveValid[prior.Artifact.ID]
		if exists && (!rebuild || prior.RecipeHash == recipeHash && reflect.DeepEqual(prior.Members, members) && archiveValid) {
			retainedGroups[key] = true
			plan.Status = "frozen"
			plan.Filename = prior.Artifact.Filename
			if prior.RecipeHash != recipeHash || !reflect.DeepEqual(prior.Members, members) {
				plan.Status = "pending_rebuild"
				plan.Reason = "stored inputs or output settings changed"
			}
			if !archiveValid {
				plan.Status, plan.Reason = "pending_rebuild", "archived volume is missing or changed; rebuild to repair it"
			}
			plans = append(plans, plan)
			continue
		}
		fingerprint, _ := json.Marshal(members)
		volume := domain.VolumeEdition{ID: "volume_" + hashBytes(append(recipe, fingerprint...)), SeriesID: series.ID, SourceID: group.chapters[0].Source.ID, TrackID: group.chapters[0].Track.ID, GroupID: key, First: plan.First, Last: plan.Last, RecipeHash: recipeHash, Active: true, Members: members}
		volume.Notes = notes
		volume.Publication = metadata
		volume.Artifact = domain.Artifact{ID: volume.ID, TrackID: volume.TrackID, Filename: plan.Filename, MIMEType: "application/epub+zip"}
		newEditions = append(newEditions, volume)
		builds = append(builds, volumeBuild{edition: volume, title: group.title, chapters: group.chapters})
		retainedGroups[key] = true
		plan.Status = "complete"
		plans = append(plans, plan)
	}
	if rebuild && len(failures) == failureStart {
		for _, old := range active {
			if old.SeriesID == series.ID && !retainedGroups[old.GroupID] && request.scope[old.SourceID] {
				removedGroups = append(removedGroups, old.GroupID)
			}
		}
	}
	return volumePlanResult{Plans: plans, Editions: newEditions, Builds: builds, Removed: removedGroups, Failures: failures}, nil
}

// volumeRecipe is the edition identity input: the series configuration, the
// publication metadata fingerprint and the assembly version, so a change to
// any of them plans a new edition.
func volumeRecipe(series config.SeriesConfig, metadata *domain.PublicationMetadata) []byte {
	recipe, _ := json.Marshal(struct {
		Series          config.SeriesConfig
		Metadata        string
		AssemblyVersion int
	}{series, artifact.PublicationFingerprint(metadata), 5})
	return recipe
}

// authoringDecisionFor is the planner's decision lookup: the histories cover
// every in-scope chapter, and this per-release fallback only guards against a
// release that arrived between analysis and use.
func authoringDecisionFor(source string, release domain.NormalizedRelease, history map[string]classify.ExplainedDecision, cfg *config.Config, rules config.RuleSet) domain.TrackDecision {
	if explained, ok := history[release.ProviderReleaseID]; ok {
		return explained.Decision
	}
	explained := classify.Explain(source, release, rules.ForSource(source))
	return sequence.Apply(cfg, source, release, explained.Decision)
}

// evaluateVolumes drives the pure planner with the reads activation needs and
// applies the materialized result to the store.
func (s *Service) evaluateVolumes(ctx context.Context, request volumeEvaluation, materialize bool) (plans []domain.VolumePlan, editions []domain.VolumeEdition, err error) {
	if request.config == nil {
		request.config = s.Config
	}
	if request.publication == nil {
		cfg := request.config
		request.publication = func(sourceID string, release domain.NormalizedRelease, decision domain.TrackDecision) (*domain.PublicationMetadata, error) {
			return publicationMetadataFor(cfg, sourceID, release, decision)
		}
	}
	cfg := request.config
	candidates := request.candidates
	scope := map[string]bool{}
	for _, candidate := range candidates {
		scope[candidate.Source.ID] = s.sourceInScope(candidate.Source.ID, request.sourceFilter, request.rebuild, cfg)
	}
	for _, volume := range request.existing {
		if volume.Active && !scope[volume.SourceID] {
			scope[volume.SourceID] = s.sourceInScope(volume.SourceID, request.sourceFilter, request.rebuild, cfg)
		}
	}
	request.scope = scope
	sourcesByRelease := map[string]string{}
	archiveValid := map[string]bool{}
	for _, candidate := range request.candidates {
		sourcesByRelease[candidate.Release.ID] = candidate.Source.ID
	}
	for _, volume := range request.existing {
		if !volume.Active {
			continue
		}
		for _, member := range volume.Members {
			if sourcesByRelease[member.ReleaseID] != "" {
				continue
			}
			bundle, readErr := s.Repo.GetReleaseBundle(ctx, member.ReleaseID)
			if readErr != nil {
				return plans, nil, readErr
			}
			if bundle != nil {
				sourcesByRelease[member.ReleaseID] = bundle.Source.ID
			}
		}
		if volume.Artifact.StorageRef == "" {
			continue
		}
		intact, checkErr := artifact.IntactOnDisk(volume.Artifact)
		archiveValid[volume.Artifact.ID] = checkErr == nil && intact
	}
	request.sourcesByRelease = sourcesByRelease
	request.archiveValid = archiveValid
	active := map[string]domain.VolumeEdition{}
	for _, volume := range request.existing {
		if volume.Active {
			active[volume.SeriesID+"/"+volume.GroupID] = volume
		}
	}
	projected := map[string]domain.VolumeEdition{}
	for key, volume := range active {
		projected[key] = volume
	}
	defer func() {
		for _, volume := range projected {
			editions = append(editions, volume)
		}
		sort.Slice(editions, func(i, j int) bool { return editions[i].ID < editions[j].ID })
	}()
	var failures []error
	for _, series := range request.series {
		if request.blockedSeries[series.ID] {
			continue
		}
		output := cfg.SeriesOutput(series)
		planned, planErr := planSeriesVolumes(series, output, request)
		if planErr != nil {
			return plans, nil, planErr
		}
		plannedPlans, plannedEditions, plannedBuilds, plannedRemoved, plannedFailures, blockSeries := planned.Plans, planned.Editions, planned.Builds, planned.Removed, planned.Failures, planned.BlockSeries
		plans = append(plans, plannedPlans...)
		if blockSeries || len(plannedFailures) > 0 {
			request.blockedSeries[series.ID] = true
			for i := range plans {
				if plans[i].SeriesID == series.ID && plans[i].Status != "blocked" {
					plans[i].Status = "blocked"
					if plans[i].Reason == "" {
						plans[i].Reason = "related volume failed; previous output retained"
					}
				}
			}
			failures = append(failures, plannedFailures...)
			continue
		}
		if materialize && (len(plannedEditions) > 0 || len(plannedRemoved) > 0) {
			var built []domain.VolumeEdition
			for _, build := range plannedBuilds {
				edition := build.edition
				edition.Artifact, err = s.Files.BuildVolume(ctx, edition, build.title, build.chapters)
				if err != nil {
					failures = append(failures, err)
					break
				}
				built = append(built, edition)
			}
			if len(built) != len(plannedEditions) {
				request.blockedSeries[series.ID] = true
				for i := range plans {
					if plans[i].SeriesID == series.ID {
						plans[i].Status = "blocked"
						if plans[i].Reason == "" {
							plans[i].Reason = "related volume failed; previous output retained"
						}
					}
				}
				continue
			}
			if err := s.Repo.ReplaceVolumes(ctx, series.ID, built, plannedRemoved); err != nil {
				return plans, nil, err
			}
			plannedEditions = built
		}
		for _, groupID := range plannedRemoved {
			delete(projected, series.ID+"/"+groupID)
		}
		for _, edition := range plannedEditions {
			projected[series.ID+"/"+edition.GroupID] = edition
		}
	}
	return plans, nil, errors.Join(failures...)
}

func (s *Service) volumeCandidates(ctx context.Context, chapters []domain.PublishCandidate, volumes []domain.VolumeEdition) ([]domain.PublishCandidate, error) {
	covered := map[string]bool{}
	var result []domain.PublishCandidate
	for _, volume := range volumes {
		if !volume.Active {
			continue
		}
		var source *domain.Source
		var track *domain.StoryTrack
		for _, chapter := range chapters {
			if chapter.Source.ID == volume.SourceID {
				value := chapter.Source
				source = &value
			}
			if chapter.Track.ID == volume.TrackID {
				value := chapter.Track
				track = &value
			}
		}
		var err error
		if source == nil {
			source, err = s.Repo.GetSource(ctx, volume.SourceID)
			if err != nil {
				return nil, err
			}
		}
		if track == nil {
			track, err = s.Repo.GetTrack(ctx, volume.TrackID)
			if err != nil {
				return nil, err
			}
		}
		if source == nil || track == nil {
			return nil, fmt.Errorf("missing source/track for volume %s", volume.ID)
		}
		for _, member := range volume.Members {
			covered[member.ReleaseID] = true
		}
		result = append(result, domain.PublishCandidate{Source: *source, Track: *track, Artifact: volume.Artifact, Volume: &volume})
	}
	for _, chapter := range chapters {
		if !covered[chapter.Release.ID] {
			result = append(result, chapter)
		}
	}
	return result, nil
}
