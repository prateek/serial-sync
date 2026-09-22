package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
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
	return s.rebuildWith(ctx, options, command, s.Config)
}

// rebuildWith is the rebuild engine with the config explicit; Rebuild runs it
// against the service config and workspace replay against a dump-scoped one.
func (s *Service) rebuildWith(ctx context.Context, options RebuildOptions, command string, cfg *config.Config) (result RebuildResult, err error) {
	result.Scope = options
	scope := deliveryScope{SourceID: options.SourceID, SeriesID: options.SeriesID, Rebuild: true}
	if err := s.validatePublishTargets(cfg, scope, options.TargetID); err != nil {
		return result, err
	}
	sources := selectSources(cfg.Sources, options.SourceID)
	if len(sources) == 0 {
		return result, fmt.Errorf("no enabled sources match %q", options.SourceID)
	}
	if len(selectPublishers(cfg.Publishers, options.TargetID)) == 0 {
		return result, fmt.Errorf("no enabled publishers match %q", options.TargetID)
	}
	if options.SeriesID != "" {
		if !cfg.Compiled().SeriesExists(options.SeriesID) {
			return result, fmt.Errorf("unknown series %q", options.SeriesID)
		}
	}
	if options.DryRun {
		return s.previewRebuild(ctx, options, cfg)
	}
	recorder, err := observe.Start(ctx, s.Repo, command, options.SourceID, false, s.observeOptions())
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
	rules := cfg.Compiled()
	decisions := NewRunDecisions(s, cfg)
	for _, source := range sources {
		_, historyErr := decisions.History(ctx, source.ID)
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
			decision := authoringDecisionFor(source.ID, normalized, decisions.Histories()[source.ID], cfg, rules)
			if options.SeriesID != "" && decision.SeriesID != options.SeriesID && oldSeries != options.SeriesID {
				continue
			}
			raw, loadErr := os.ReadFile(release.RawPayloadRef)
			if loadErr != nil {
				blockedSeries[oldSeries], blockedSeries[decision.TrackKey] = true, true
				result.Blocked = append(result.Blocked, fmt.Sprintf("%s: %v", release.ProviderReleaseID, loadErr))
				continue
			}
			plan, _, _, rebuildErr := s.intakeHandleRelease(ctx, recorder, source, provider.ReleaseDocument{Normalized: normalized, RawJSON: raw}, decision, false, true)
			if rebuildErr != nil {
				blockedSeries[oldSeries], blockedSeries[decision.TrackKey] = true, true
				result.Blocked = append(result.Blocked, fmt.Sprintf("%s: %v", release.ProviderReleaseID, rebuildErr))
				continue
			}
			if plan.Action == "blocked" {
				blockedSeries[oldSeries], blockedSeries[decision.TrackKey] = true, true
				result.Blocked = append(result.Blocked, fmt.Sprintf("%s: %v", release.ProviderReleaseID, plan.BlockReason))
				continue
			}
			result.Plans = append(result.Plans, plan)
		}
		if storedSource != nil {
			if err := s.Repo.UpsertSource(ctx, *storedSource); err != nil {
				return result, err
			}
		}
	}
	result.Publish, err = s.publish(ctx, scope, options.TargetID, false, command+" publish", blockedSeries, decisions)
	if err != nil {
		return result, err
	}
	if len(result.Blocked) > 0 {
		return result, fmt.Errorf("rebuild incomplete: %s", strings.Join(result.Blocked, "; "))
	}
	return result, nil
}

func (s *Service) previewRebuild(ctx context.Context, options RebuildOptions, cfg *config.Config) (RebuildResult, error) {
	result := RebuildResult{Scope: options, Publish: domain.PublishResult{DryRun: true}}
	candidates, err := s.Repo.ListPublishCandidates(ctx, "")
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		seen[candidate.Release.ID] = true
	}
	for _, source := range selectSources(cfg.Sources, options.SourceID) {
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
	decisions := NewRunDecisions(s, cfg)
	historyErrors := map[string]error{}
	for _, candidate := range candidates {
		if _, ok := historyErrors[candidate.Source.ID]; !ok {
			_, historyErrors[candidate.Source.ID] = decisions.History(ctx, candidate.Source.ID)
		}
	}
	rules := cfg.Compiled()
	for i := range candidates {
		candidate := &candidates[i]
		if !s.sourceInScope(candidate.Source.ID, options.SourceID, true, cfg) {
			continue
		}
		release := candidate.Release
		normalized, loadErr := s.loadStoredNormalized(ctx, release)

		decision := authoringDecisionFor(candidate.Source.ID, normalized, decisions.Histories()[candidate.Source.ID], cfg, rules)
		if loadErr == nil {
			decision.Publication, loadErr = publicationMetadataFor(cfg, candidate.Source.ID, normalized, decision)
		}
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
		description := artifact.Describe(track, release, normalized, decision)
		plan := domain.SyncItemPlan{SourceID: candidate.Source.ID, ProviderReleaseID: release.ProviderReleaseID, Title: release.Title, TrackKey: track.TrackKey, Filename: description.FileName, Action: "rebuild"}
		if artifact.IsCurrent(candidate.Artifact, release, track, decision) {
			plan.Action = "unchanged"
		} else {
			// The rebuild artifact does not exist yet. Planned marks the
			// candidate as not-yet-stored: it never clears a delivered
			// record from the desired set and cannot match one as unchanged.
			candidate.Planned = true
			candidate.Artifact.SHA256 = ""
		}
		candidate.Track = track
		candidate.Assignment.ReleaseRole = decision.ReleaseRole
		candidate.Artifact.Filename = description.FileName
		candidate.Artifact.MIMEType = description.MIMEType
		result.Plans = append(result.Plans, plan)
	}
	materializable := candidates[:0]
	for _, candidate := range candidates {
		if candidate.Artifact.ID != "" || candidate.Planned {
			materializable = append(materializable, candidate)
		}
	}
	candidates = materializable
	var volumes []domain.VolumeEdition
	result.Publish.Volumes, volumes, err = s.evaluateVolumes(ctx, volumeEvaluation{sourceFilter: options.SourceID, seriesFilter: options.SeriesID, rebuild: true, blockedSeries: blocked, candidates: candidates, existing: existing, inputs: inputs, histories: decisions.Histories(), series: cfg.Series, config: cfg}, false)
	if err != nil {
		result.Blocked = append(result.Blocked, err.Error())
	}
	targets := selectPublishers(cfg.Publishers, options.TargetID)
	scope := deliveryScope{SourceID: options.SourceID, SeriesID: options.SeriesID, Rebuild: true}
	selected, recordsByTarget, _, err := s.deliverySelection(ctx, cfg, scope, targets, candidates, blocked, volumes)
	if err != nil {
		return result, err
	}
	if err := s.validateLegacyReplacements(ctx, targets, selected, recordsByTarget); err != nil {
		return result, err
	}
	for _, target := range targets {
		pt, targetErr := publish.TargetFor(target)
		if targetErr != nil {
			result.Blocked = append(result.Blocked, targetErr.Error())
			continue
		}
		pending, err := s.pendingDelivery(ctx, cfg, scope, target)
		if err != nil {
			result.Blocked = append(result.Blocked, err.Error())
			continue
		}
		if pending != nil {
			result.Pending = append(result.Pending, *pending)
		}
		plan, planErr := s.planDelivery(ctx, scope, pt, target, selected, librarySnapshot{Records: recordsByTarget[target.ID], Volumes: existing})
		if planErr != nil {
			result.Blocked = append(result.Blocked, planErr.Error())
			continue
		}
		result.Publish.Items = append(result.Publish.Items, plan.Items...)
		result.Blocked = append(result.Blocked, plan.Blocked...)
		for _, item := range plan.Items {
			if item.Action == "replace" || item.Action == "retire" {
				result.Notices = append(result.Notices, "Replacing delivered books may reset saved reading positions; check a small reader sample before a larger migration.")
				break
			}
		}
	}
	if len(result.Blocked) > 0 {
		return result, fmt.Errorf("rebuild incomplete: %s", strings.Join(result.Blocked, "; "))
	}
	return result, nil
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
