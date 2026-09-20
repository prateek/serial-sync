package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/publish"
)

type volumeGroup struct {
	gaps      []config.ChapterGap
	plan      domain.VolumePlan
	title     string
	chapters  []domain.PublishCandidate
	sequences map[string]domain.Sequence
}

func (s *Service) prepareVolumes(ctx context.Context, sourceFilter, seriesFilter string, rebuild bool, blockedSeries map[string]bool) ([]domain.VolumePlan, error) {
	candidates, err := s.Repo.ListPublishCandidates(ctx, "")
	if err != nil {
		return nil, err
	}
	existing, err := s.Repo.ListVolumeEditions(ctx)
	if err != nil {
		return nil, err
	}
	inputs := map[string]domain.NormalizedRelease{}
	for _, candidate := range candidates {
		if !s.sourceInScope(candidate.Source.ID, sourceFilter, rebuild) {
			continue
		}
		data, err := os.ReadFile(candidate.Release.NormalizedPayloadRef)
		var normalized domain.NormalizedRelease
		if err == nil && json.Unmarshal(data, &normalized) == nil {
			inputs[candidate.Release.ID] = normalized
		}
	}
	plans, _, err := s.evaluateVolumes(ctx, volumeEvaluation{sourceFilter: sourceFilter, seriesFilter: seriesFilter, rebuild: rebuild, materialize: true, blockedSeries: blockedSeries, candidates: candidates, existing: existing, inputs: inputs})
	return plans, err
}

type volumeEvaluation struct {
	sourceFilter, seriesFilter string
	rebuild, materialize       bool
	blockedSeries              map[string]bool
	candidates                 []domain.PublishCandidate
	existing                   []domain.VolumeEdition
	inputs                     map[string]domain.NormalizedRelease
}

func (s *Service) evaluateVolumes(ctx context.Context, request volumeEvaluation) (plans []domain.VolumePlan, editions []domain.VolumeEdition, err error) {
	sourceFilter, seriesFilter := request.sourceFilter, request.seriesFilter
	rebuild, write := request.rebuild, request.materialize
	blockedSeries, candidates, existing, inputs := request.blockedSeries, request.candidates, request.existing, request.inputs
	active := map[string]domain.VolumeEdition{}
	for _, volume := range existing {
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
	for _, series := range s.Config.Series {
		if blockedSeries[series.ID] {
			continue
		}
		if seriesFilter != "" && series.ID != seriesFilter {
			continue
		}
		if sourceFilter != "" || rebuild {
			selected := false
			for _, candidate := range candidates {
				if candidate.Track.TrackKey == series.ID && s.sourceInScope(candidate.Source.ID, sourceFilter, rebuild) {
					selected = true
				}
			}
			for _, old := range active {
				if old.SeriesID == series.ID && s.sourceInScope(old.SourceID, sourceFilter, rebuild) {
					selected = true
				}
			}
			if !selected {
				continue
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
					memberSource := ""
					for _, candidate := range candidates {
						if candidate.Release.ID == member.ReleaseID {
							memberSource = candidate.Source.ID
							break
						}
					}
					if memberSource == "" {
						bundle, err := s.Repo.GetReleaseBundle(ctx, member.ReleaseID)
						if err != nil {
							return plans, nil, err
						}
						if bundle != nil {
							memberSource = bundle.Source.ID
						}
					}
					if s.sourceInScope(memberSource, sourceFilter, rebuild) {
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
				blockedSeries[series.ID] = true
				continue
			}
		}
		failureStart := len(failures)
		planStart := len(plans)
		var newEditions []domain.VolumeEdition
		var removedGroups []string
		activate := func() error {
			if len(failures) != failureStart {
				blockedSeries[series.ID] = true
				for i := planStart; i < len(plans); i++ {
					plans[i].Status = "blocked"
					if plans[i].Reason == "" {
						plans[i].Reason = "related volume failed; previous output retained"
					}
				}
				return nil
			}
			if write && (len(newEditions) > 0 || len(removedGroups) > 0) {
				if err := s.Repo.ReplaceVolumes(ctx, series.ID, newEditions, removedGroups); err != nil {
					return err
				}
			}
			for _, groupID := range removedGroups {
				delete(projected, series.ID+"/"+groupID)
			}
			for _, edition := range newEditions {
				projected[series.ID+"/"+edition.GroupID] = edition
			}
			return nil
		}
		output := config.SeriesOutputDefaults(series.Output)
		retainedGroups := map[string]bool{}
		if output.Bundling != "volume" {
			for _, old := range active {
				if old.SeriesID != series.ID || !s.sourceInScope(old.SourceID, sourceFilter, rebuild) {
					continue
				}
				plan := domain.VolumePlan{SeriesID: series.ID, GroupID: old.GroupID, First: old.First, Last: old.Last, Filename: old.Artifact.Filename, Status: "pending_rebuild", Reason: "bundling disabled; rebuild to publish singles"}
				if rebuild {
					ready := true
					for _, member := range old.Members {
						_, present := inputs[member.ReleaseID]
						selected := false
						for _, candidate := range candidates {
							if candidate.Release.ID == member.ReleaseID && s.sourceInScope(candidate.Source.ID, sourceFilter, rebuild) {
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
			if err := activate(); err != nil {
				return plans, nil, err
			}
			continue
		}
		if output.Format != "epub" || output.ChaptersPerVolume < 1 {
			return plans, nil, fmt.Errorf("series %s: volume bundling requires epub and a positive chapter count", series.ID)
		}
		groups := map[string]*volumeGroup{}
		hasBooks := false
		for _, candidate := range candidates {
			if candidate.Track.TrackKey != series.ID || candidate.Assignment.ReleaseRole != domain.ReleaseRoleChapter {
				continue
			}
			var seq domain.Sequence
			if !s.sourceInScope(candidate.Source.ID, sourceFilter, rebuild) {
				seq = archivedSequence(candidate, series)
			} else {
				normalized, present := inputs[candidate.Release.ID]
				if !present {
					failures = append(failures, fmt.Errorf("missing stored input for %s", candidate.Release.ProviderReleaseID))
					continue
				}
				decision := s.numberedDecision(candidate.Source.ID, normalized, classify.Decide(candidate.Source.ID, normalized, s.Config.RulesForSource(candidate.Source.ID)))
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
				if !rebuild && chapter.Artifact.MetadataRef != "" && legacyArtifact(chapter.Artifact) {
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
				if s.sourceInScope(chapter.Source.ID, sourceFilter, rebuild) {
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
			recipe, _ := json.Marshal(series)
			recipeHash := hashBytes(recipe)
			prior, exists := active[series.ID+"/"+key]
			archiveValid := false
			if exists {
				actual, checkErr := publish.FileHash(prior.Artifact.StorageRef)
				archiveValid = checkErr == nil && actual == prior.Artifact.SHA256
			}
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
			volume.Artifact = domain.Artifact{ID: volume.ID, TrackID: volume.TrackID, Filename: plan.Filename, MIMEType: "application/epub+zip"}
			if write {
				volume.Artifact, err = s.Files.BuildVolume(ctx, volume, group.title, group.chapters)
				if err != nil {
					failures = append(failures, err)
					plan.Reason = err.Error()
					plans = append(plans, plan)
					continue
				}
			}
			newEditions = append(newEditions, volume)
			retainedGroups[key] = true
			plan.Status = "complete"
			plans = append(plans, plan)
		}
		if rebuild && len(failures) == failureStart {
			for _, old := range active {
				if old.SeriesID == series.ID && !retainedGroups[old.GroupID] && s.sourceInScope(old.SourceID, sourceFilter, rebuild) {
					removedGroups = append(removedGroups, old.GroupID)
				}
			}
		}
		if err := activate(); err != nil {
			return plans, nil, err
		}
	}
	return plans, nil, errors.Join(failures...)
}

func (s *Service) volumePublishCandidates(ctx context.Context, chapters []domain.PublishCandidate) ([]domain.PublishCandidate, error) {
	volumes, err := s.Repo.ListVolumeEditions(ctx)
	if err != nil {
		return nil, err
	}
	return s.volumeCandidates(ctx, chapters, volumes)
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
