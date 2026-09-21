package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/publish"
)

type RebuildOptions struct {
	SourceID string `json:"source,omitempty"`
	TargetID string `json:"target,omitempty"`
	SeriesID string `json:"series,omitempty"`
	DryRun   bool   `json:"dry_run"`
}

type RebuildResult struct {
	Scope   RebuildOptions        `json:"scope"`
	Notices []string              `json:"notices,omitempty"`
	Pending []deliveryPlan        `json:"pending_deliveries,omitempty"`
	RunID   string                `json:"run_id,omitempty"`
	Plans   []domain.SyncItemPlan `json:"plans"`
	Publish domain.PublishResult  `json:"publish"`
	Blocked []string              `json:"blocked,omitempty"`
}

func (s *Service) Rebuild(ctx context.Context, options RebuildOptions, command string) (result RebuildResult, err error) {
	result.Scope = options
	if err := s.validatePublishTargets(options.SourceID, options.TargetID, options.SeriesID, true); err != nil {
		return result, err
	}
	sources := selectSources(s.Config.Sources, options.SourceID)
	if len(sources) == 0 {
		return result, fmt.Errorf("no enabled sources match %q", options.SourceID)
	}
	if len(selectPublishers(s.Config.Publishers, options.TargetID)) == 0 {
		return result, fmt.Errorf("no enabled publishers match %q", options.TargetID)
	}
	if options.SeriesID != "" {
		found := false
		for _, rule := range s.Config.Rules {
			if rule.SeriesID == options.SeriesID || rule.TrackKey == options.SeriesID {
				found = true
			}
		}
		if !found {
			return result, fmt.Errorf("unknown series %q", options.SeriesID)
		}
	}
	if options.DryRun {
		return s.previewRebuild(ctx, options)
	}
	recorder, err := observe.Start(ctx, s.Repo, command, options.SourceID, options.DryRun, s.observeOptions())
	if err != nil {
		return result, err
	}
	result.RunID = recorder.RunID()
	blockedSeries := map[string]bool{}
	defer func() {
		status, summary := domain.RunStatusSucceeded, fmt.Sprintf("replanned=%d blocked=%d", len(result.Plans), len(result.Blocked))
		if err != nil {
			status, summary = domain.RunStatusFailed, err.Error()
		}
		if finishErr := recorder.Finish(ctx, status, summary); err == nil {
			err = finishErr
		}
	}()
	for _, source := range sources {
		history, historyErr := s.authoringDecisions(ctx, source.ID, nil)
		if historyErr != nil {
			result.Blocked = append(result.Blocked, source.ID+": "+historyErr.Error())
			return result, fmt.Errorf("rebuild history unavailable for %s: %w", source.ID, historyErr)
		}
		storedSource, readErr := s.Repo.GetSource(ctx, source.ID)
		if readErr != nil {
			return result, readErr
		}
		releases, readErr := s.Repo.ListReleases(ctx, source.ID)
		if readErr != nil {
			return result, readErr
		}
		for _, release := range releases {
			bundle, bundleErr := s.Repo.GetReleaseBundle(ctx, release.ID)
			if bundleErr != nil {
				return result, bundleErr
			}
			oldSeries := ""
			if bundle != nil {
				oldSeries = bundle.Track.TrackKey
			}
			normalized, loadErr := s.loadStoredNormalized(ctx, release)

			if loadErr != nil {
				if options.SeriesID != "" && oldSeries != options.SeriesID {
					continue
				}
				blockedSeries[oldSeries] = true
				result.Blocked = append(result.Blocked, fmt.Sprintf("%s: %v", release.ProviderReleaseID, loadErr))
				continue
			}
			decision := s.authoringDecision(source.ID, normalized, history)
			if options.SeriesID != "" && decision.SeriesID != options.SeriesID {
				continue
			}
			raw, loadErr := os.ReadFile(release.RawPayloadRef)
			if loadErr != nil {
				blockedSeries[oldSeries], blockedSeries[decision.TrackKey] = true, true
				result.Blocked = append(result.Blocked, fmt.Sprintf("%s: %v", release.ProviderReleaseID, loadErr))
				continue
			}
			plan, _, _, rebuildErr := s.handleRelease(ctx, recorder, source, provider.ReleaseDocument{Normalized: normalized, RawJSON: raw}, decision, options.DryRun, true)
			if rebuildErr != nil {
				blockedSeries[oldSeries], blockedSeries[decision.TrackKey] = true, true
				result.Blocked = append(result.Blocked, fmt.Sprintf("%s: %v", release.ProviderReleaseID, rebuildErr))
				continue
			}
			result.Plans = append(result.Plans, plan)
		}
		if storedSource != nil && !options.DryRun {
			if err := s.Repo.UpsertSource(ctx, *storedSource); err != nil {
				return result, err
			}
		}
	}
	result.Publish, err = s.publish(ctx, options.SourceID, options.TargetID, options.SeriesID, options.DryRun, true, command+" publish", blockedSeries)
	if err != nil {
		return result, err
	}
	if len(result.Blocked) > 0 {
		return result, fmt.Errorf("rebuild incomplete: %s", strings.Join(result.Blocked, "; "))
	}
	return result, nil
}

func (s *Service) previewRebuild(ctx context.Context, options RebuildOptions) (RebuildResult, error) {
	result := RebuildResult{Scope: options, Publish: domain.PublishResult{DryRun: true}}
	candidates, err := s.Repo.ListPublishCandidates(ctx, "")
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		seen[candidate.Release.ID] = true
	}
	for _, source := range selectSources(s.Config.Sources, options.SourceID) {
		releases, err := s.Repo.ListReleases(ctx, source.ID)
		if err != nil {
			return result, err
		}
		for _, release := range releases {
			if seen[release.ID] {
				continue
			}
			bundle, err := s.Repo.GetReleaseBundle(ctx, release.ID)
			if err != nil {
				return result, err
			}
			if bundle != nil {
				candidates = append(candidates, domain.PublishCandidate{Source: bundle.Source, Release: release, Track: bundle.Track, Assignment: bundle.Assignment})
			}
		}
	}
	existing, err := s.Repo.ListVolumeEditions(ctx)
	if err != nil {
		return result, err
	}
	inputs := map[string]domain.NormalizedRelease{}
	blocked := map[string]bool{}
	histories := map[string]map[string]classify.ExplainedDecision{}
	historyErrors := map[string]error{}
	for _, candidate := range candidates {
		if _, ok := histories[candidate.Source.ID]; !ok {
			histories[candidate.Source.ID], historyErrors[candidate.Source.ID] = s.authoringDecisions(ctx, candidate.Source.ID, nil)
		}
	}
	for i := range candidates {
		candidate := &candidates[i]
		if !s.sourceInScope(candidate.Source.ID, options.SourceID, true) {
			continue
		}
		release := candidate.Release
		normalized, loadErr := s.loadStoredNormalized(ctx, release)

		decision := s.authoringDecision(candidate.Source.ID, normalized, histories[candidate.Source.ID])
		if loadErr == nil {
			loadErr = historyErrors[candidate.Source.ID]
		}
		if options.SeriesID != "" && decision.SeriesID != options.SeriesID && candidate.Track.TrackKey != options.SeriesID {
			continue
		}
		if loadErr == nil {
			_, loadErr = os.ReadFile(release.RawPayloadRef)
		}
		if loadErr == nil {
			loadErr = checkStoredAttachment(normalized, decision)
		}
		if loadErr != nil {
			result.Blocked = append(result.Blocked, fmt.Sprintf("%s: %v", release.ProviderReleaseID, loadErr))
			blocked[candidate.Track.TrackKey], blocked[decision.TrackKey] = true, true
			continue
		}
		inputs[release.ID] = normalized
		if !classify.CanMaterialize(normalized, decision) {
			candidate.Artifact.ID = ""
			continue
		}
		track, err := s.resolveTrack(ctx, candidate.Source, decision)
		if err != nil {
			return result, err
		}
		name := artifact.PreviewFilename(track, release, normalized, decision)
		plan := domain.SyncItemPlan{SourceID: candidate.Source.ID, ProviderReleaseID: release.ProviderReleaseID, Title: release.Title, TrackKey: track.TrackKey, Filename: name, Action: "rebuild"}
		if artifactMatches(candidate.Artifact, release, track, decision) {
			plan.Action = "unchanged"
		} else {
			candidate.Artifact.ID = "planned:" + release.ID
			candidate.Artifact.SHA256 = ""
		}
		candidate.Track = track
		candidate.Assignment.ReleaseRole = decision.ReleaseRole
		candidate.Artifact.Filename = name
		if decision.OutputFormat == domain.OutputFormatEPUB {
			candidate.Artifact.MIMEType = "application/epub+zip"
		}
		result.Plans = append(result.Plans, plan)
	}
	materializable := candidates[:0]
	for _, candidate := range candidates {
		if candidate.Artifact.ID != "" {
			materializable = append(materializable, candidate)
		}
	}
	candidates = materializable
	var volumes []domain.VolumeEdition
	result.Publish.Volumes, volumes, err = s.evaluateVolumes(ctx, volumeEvaluation{sourceFilter: options.SourceID, seriesFilter: options.SeriesID, rebuild: true, blockedSeries: blocked, candidates: candidates, existing: existing, inputs: inputs})
	if err != nil {
		result.Blocked = append(result.Blocked, err.Error())
	}
	candidates, err = s.volumeCandidates(ctx, candidates, volumes)
	if err != nil {
		return result, err
	}
	if err := resolveCollisionNames(candidates); err != nil {
		return result, err
	}
	selected := make([]domain.PublishCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !s.sourceInScope(candidate.Source.ID, options.SourceID, true) || options.SeriesID != "" && candidate.Track.TrackKey != options.SeriesID || blocked[candidate.Track.TrackKey] {
			continue
		}
		selected = append(selected, candidate)
	}
	targets := selectPublishers(s.Config.Publishers, options.TargetID)
	if err := s.validateLegacyReplacements(ctx, targets, selected); err != nil {
		return result, err
	}
	for _, target := range targets {
		pending, err := s.pendingDelivery(ctx, target, options.SourceID, options.SeriesID, true)
		if err != nil {
			result.Blocked = append(result.Blocked, err.Error())
			continue
		}
		if pending != nil {
			result.Pending = append(result.Pending, *pending)
		}
		records, err := s.Repo.ListPublishRecords(ctx, "", target.ID)
		if err != nil {
			return result, err
		}
		desired, covered := map[string]bool{}, map[string]bool{}
		identity := func(ref, artifactID, filename string) string {
			if normalizedPublisherKind(target.Kind) == "exec" {
				return artifactID + "\x00" + filename
			}
			return ref
		}
		for _, candidate := range selected {
			kind, ref, signature, err := publishTargetIdentity(target, candidate)
			if err != nil {
				return result, err
			}
			desired[identity(ref, candidate.Artifact.ID, candidate.Artifact.Filename)] = true
			if candidate.Volume != nil {
				for _, member := range candidate.Volume.Members {
					covered[member.ReleaseID] = true
				}
			} else {
				covered[candidate.Release.ID] = true
			}
			action := "add"
			for _, record := range records {
				if record.Record.Status != domain.PublishStatusPublished {
					continue
				}
				if kind == "filesystem" && record.Record.TargetRef == ref || kind == "exec" && record.Record.Filename == candidate.Artifact.Filename {
					action = "replace"
				}
				if candidate.Artifact.ID == record.Artifact.ID && candidate.Artifact.SHA256 != "" && record.Record.PublishHash == publish.PublishHash(target.ID, candidate.Artifact.SHA256, signature) {
					action = "unchanged"
					if kind == "filesystem" {
						hash, err := publish.FileHash(ref)
						if err != nil || hash != candidate.Artifact.SHA256 {
							action = "repair"
						}
					}
					break
				}
			}
			message := ""
			if kind == "filesystem" {
				if _, readErr := publish.CheckFilesystemDestination(ref, ownedHashesForPath(records, ref)); readErr != nil {
					action = "blocked"
					message = fmt.Sprintf("target %s: %v", target.ID, readErr)
					result.Blocked = append(result.Blocked, message)
				}
			}
			result.Publish.Items = append(result.Publish.Items, domain.PublishItemResult{ArtifactID: candidate.Artifact.ID, TargetID: target.ID, TargetKind: kind, TargetRef: ref, Action: action, Message: message})
		}
		for _, old := range records {
			if old.Record.Status != domain.PublishStatusPublished || desired[identity(old.Record.TargetRef, old.Artifact.ID, old.Record.Filename)] {
				continue
			}
			complete := covered[old.Release.ID]
			for _, volume := range existing {
				if volume.Artifact.ID == old.Artifact.ID {
					complete = true
					for _, member := range volume.Members {
						if !covered[member.ReleaseID] {
							complete = false
						}
					}
				}
			}
			if complete {
				item := domain.PublishItemResult{ArtifactID: old.Artifact.ID, TargetID: target.ID, TargetKind: old.Record.TargetKind, TargetRef: old.Record.TargetRef, Action: "retire"}
				if old.Record.TargetKind == "filesystem" {
					current, checkErr := publish.FileHash(old.Record.TargetRef)
					if checkErr != nil || current != "" && current != old.Artifact.SHA256 {
						item.Action = "blocked"
						item.Message = fmt.Sprintf("target %s retirement ownership conflict: %s", target.ID, old.Record.TargetRef)
						result.Blocked = append(result.Blocked, item.Message)
					}
				}
				result.Publish.Items = append(result.Publish.Items, item)
			}
		}
	}
	for _, item := range result.Publish.Items {
		if item.Action == "replace" || item.Action == "retire" {
			result.Notices = append(result.Notices, "Replacing delivered books may reset saved reading positions; check a small reader sample before a larger migration.")
			break
		}
	}
	if len(result.Blocked) > 0 {
		return result, fmt.Errorf("rebuild incomplete: %s", strings.Join(result.Blocked, "; "))
	}
	return result, nil
}

func artifactMatches(current domain.Artifact, release domain.Release, track domain.StoryTrack, decision domain.TrackDecision) bool {
	var meta struct {
		OutputVersion int                       `json:"output_version"`
		Track         domain.StoryTrack         `json:"track"`
		Release       domain.Release            `json:"release"`
		Decision      domain.TrackDecision      `json:"decision"`
		Normalized    *domain.NormalizedRelease `json:"normalized"`
	}
	data, err := os.ReadFile(current.MetadataRef)
	if err != nil || json.Unmarshal(data, &meta) != nil || meta.OutputVersion != 2 {
		return false
	}
	if meta.Decision.SelectedContent == nil && meta.Normalized != nil {
		selected := classify.SelectContent(*meta.Normalized, meta.Decision)
		meta.Decision.SelectedContent = &selected
	}
	return meta.Release.ContentHash == release.ContentHash && meta.Track.TrackName == track.TrackName && meta.Track.CanonicalAuthor == track.CanonicalAuthor && reflect.DeepEqual(meta.Decision, decision)
}

func legacyArtifact(current domain.Artifact) bool {
	var meta struct {
		OutputVersion int `json:"output_version"`
	}
	data, err := os.ReadFile(current.MetadataRef)
	return err != nil || json.Unmarshal(data, &meta) != nil || meta.OutputVersion < 2
}

func checkStoredAttachment(normalized domain.NormalizedRelease, decision domain.TrackDecision) error {
	if decision.ContentStrategy != domain.ContentStrategyAttachmentOnly && decision.ContentStrategy != domain.ContentStrategyAttachmentPreferred {
		return nil
	}
	attachment, selected := classify.SelectAttachment(normalized, decision)
	if !selected {
		return nil
	}
	if attachment.LocalPath == "" {
		return fmt.Errorf("selected attachment %s has no stored local_path", attachment.FileName)
	}
	data, err := os.ReadFile(attachment.LocalPath)
	if err != nil {
		return err
	}
	if attachment.SHA256 != "" && hashBytes(data) != attachment.SHA256 {
		return fmt.Errorf("stored attachment hash mismatch: %s", attachment.LocalPath)
	}
	return nil
}
