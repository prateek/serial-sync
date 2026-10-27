package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
	"github.com/prateek/serial-sync/internal/provider"
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
	cfg := s.Config
	result.Scope = options
	scope := deliveryScope{SourceID: options.SourceID, SeriesID: options.SeriesID, Rebuild: true}
	if err := s.catalogReader().validateRebuild(cfg, options); err != nil {
		return result, err
	}
	if options.DryRun {
		return s.catalogReader().previewRebuild(ctx, options, cfg)
	}
	sources := selectSources(cfg.Sources, options.SourceID)
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
			decision := decisionFromHistory(source.ID, normalized, decisions.Histories()[source.ID], cfg).Decision
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

func (s *catalogReader) previewRebuild(ctx context.Context, options RebuildOptions, cfg *config.Config) (RebuildResult, error) {
	result := RebuildResult{Scope: options, Publish: domain.PublishResult{DryRun: true}}
	candidates, err := s.catalog.ListPublishCandidates(ctx, "")
	if err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		seen[candidate.Release.ID] = true
	}
	for _, source := range selectSources(cfg.Sources, options.SourceID) {
		releases, err := s.catalog.ListReleases(ctx, source.ID)
		if err != nil {
			return result, err
		}
		for _, release := range releases {
			if seen[release.ID] {
				continue
			}
			bundle, err := s.catalog.GetReleaseBundle(ctx, release.ID)
			if err != nil {
				return result, err
			}
			if bundle != nil {
				candidates = append(candidates, domain.PublishCandidate{Source: bundle.Source, Release: release, Track: bundle.Track, Assignment: bundle.Assignment})
			}
		}
	}
	existing, err := s.catalog.ListVolumeEditions(ctx)
	if err != nil {
		return result, err
	}
	inputs := map[string]domain.NormalizedRelease{}
	blocked := map[string]bool{}
	decisions := newReadDecisions(s, cfg)
	historyErrors := map[string]error{}
	for _, candidate := range candidates {
		if _, ok := historyErrors[candidate.Source.ID]; !ok {
			_, historyErrors[candidate.Source.ID] = decisions.History(ctx, candidate.Source.ID)
		}
	}
	for i := range candidates {
		candidate := &candidates[i]
		if !sourceInScope(candidate.Source.ID, options.SourceID, true, cfg) {
			continue
		}
		release := candidate.Release
		normalized, loadErr := s.loadStoredNormalized(ctx, release)

		decision := decisionFromHistory(candidate.Source.ID, normalized, decisions.Histories()[candidate.Source.ID], cfg).Decision
		if loadErr == nil {
			loadErr = historyErrors[candidate.Source.ID]
		}
		if options.SeriesID != "" && decision.SeriesID != options.SeriesID && candidate.Track.TrackKey != options.SeriesID {
			continue
		}
		var raw []byte
		if loadErr == nil {
			raw, loadErr = os.ReadFile(release.RawPayloadRef)
		}
		var assessment intakeAssessment
		if loadErr == nil {
			source, _ := cfg.SourceByID(candidate.Source.ID)
			stored := &StoredArtifactRef{Release: &release, Track: candidate.Track, Assignment: candidate.Assignment}
			if candidate.Artifact.ID != "" {
				stored.Artifact = &candidate.Artifact
			}
			assessment, loadErr = s.assess(ctx, cfg, source, provider.ReleaseDocument{Normalized: normalized, RawJSON: raw}, decision, stored, true)
		}
		if loadErr != nil {
			result.Blocked = append(result.Blocked, fmt.Sprintf("%s: %v", release.ProviderReleaseID, loadErr))
			blocked[candidate.Track.TrackKey], blocked[decision.TrackKey] = true, true
			continue
		}
		inputs[release.ID] = assessment.Normalized
		intake := assessment.Plan
		if !assessment.Materializable {
			candidate.Artifact.ID = ""
			continue
		}
		plan := domain.SyncItemPlan{SourceID: candidate.Source.ID, ProviderReleaseID: release.ProviderReleaseID, Title: release.Title, TrackKey: intake.Track.TrackKey, Filename: intake.ArtifactPlan.Filename, Action: "rebuild"}
		if intake.Action == "noop" {
			plan.Action = "unchanged"
		} else {
			// Planned copies have no generated bytes to compare with delivery records.
			candidate.Planned = true
			candidate.Artifact.SHA256 = ""
		}
		candidate.Release.ContentHash = intake.ContentHash
		candidate.Track = intake.Track
		candidate.Assignment.ReleaseRole = intake.Decision.ReleaseRole
		candidate.Artifact.Filename = intake.ArtifactPlan.Filename
		candidate.Artifact.MIMEType = intake.ArtifactPlan.MIMEType
		result.Plans = append(result.Plans, plan)
	}
	materializable := candidates[:0]
	for _, candidate := range candidates {
		if candidate.Artifact.ID != "" || candidate.Planned {
			materializable = append(materializable, candidate)
		}
	}
	candidates = materializable
	inputsForDelivery := deliveryInputs{chapters: candidates, normalized: inputs, histories: decisions.Histories(), blocked: blocked, editions: existing}
	if err := s.previewDelivery(ctx, cfg, options, inputsForDelivery, &result); err != nil {
		return result, err
	}
	if len(result.Blocked) > 0 {
		return result, fmt.Errorf("rebuild incomplete: %s", strings.Join(result.Blocked, "; "))
	}
	return result, nil
}

func (s *catalogReader) validateRebuild(cfg *config.Config, options RebuildOptions) error {
	scope := deliveryScope{SourceID: options.SourceID, SeriesID: options.SeriesID, Rebuild: true}
	if err := validatePublishTargets(cfg, scope, options.TargetID); err != nil {
		return err
	}
	sources := selectSources(cfg.Sources, options.SourceID)
	if len(sources) == 0 {
		return fmt.Errorf("no enabled sources match %q", options.SourceID)
	}
	if len(selectPublishers(cfg.Publishers, options.TargetID)) == 0 {
		return fmt.Errorf("no enabled publishers match %q", options.TargetID)
	}
	if options.SeriesID != "" {
		if !cfg.Compiled().SeriesExists(options.SeriesID) {
			return fmt.Errorf("unknown series %q", options.SeriesID)
		}
	}
	return nil
}

func (s *catalogReader) planRebuild(ctx context.Context, options RebuildOptions, cfg *config.Config) (RebuildResult, error) {
	if err := s.validateRebuild(cfg, options); err != nil {
		return RebuildResult{Scope: options}, err
	}
	return s.previewRebuild(ctx, options, cfg)
}
