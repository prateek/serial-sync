package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/publish"
	"github.com/prateek/serial-sync/internal/store"
)

type Service struct {
	Config     *config.Config
	Roots      config.Roots
	ConfigPath string
	Repo       store.Repository
	Providers  *provider.Registry
	Files      *artifact.Materializer
}

type SourceInspect struct {
	ConfigSource config.SourceConfig `json:"config_source"`
	StoredSource *domain.Source      `json:"stored_source,omitempty"`
	Tracks       []domain.StoryTrack `json:"tracks"`
	Releases     []domain.Release    `json:"releases"`
}

type AuthBootstrapItem struct {
	SourceID      string           `json:"source_id"`
	Provider      string           `json:"provider"`
	AuthProfileID string           `json:"auth_profile_id"`
	AuthState     domain.AuthState `json:"auth_state"`
	Action        string           `json:"action"`
	Message       string           `json:"message,omitempty"`
}

type AuthBootstrapResult struct {
	RunID        string              `json:"run_id"`
	Verified     int                 `json:"verified"`
	Bootstrapped int                 `json:"bootstrapped"`
	Failed       int                 `json:"failed"`
	Items        []AuthBootstrapItem `json:"items"`
}

type AuthImportItem struct {
	SourceID      string           `json:"source_id"`
	Provider      string           `json:"provider"`
	AuthProfileID string           `json:"auth_profile_id"`
	SessionPath   string           `json:"session_path"`
	AuthState     domain.AuthState `json:"auth_state"`
	Action        string           `json:"action"`
	Message       string           `json:"message,omitempty"`
}

type AuthImportResult struct {
	RunID     string           `json:"run_id"`
	Imported  int              `json:"imported"`
	Validated int              `json:"validated"`
	Failed    int              `json:"failed"`
	Items     []AuthImportItem `json:"items"`
}

type RunEventFilter struct {
	Level      string `json:"level,omitempty"`
	Component  string `json:"component,omitempty"`
	EntityKind string `json:"entity_kind,omitempty"`
	EntityID   string `json:"entity_id,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type RunEventList struct {
	RunID   string               `json:"run_id"`
	Filter  RunEventFilter       `json:"filter"`
	Count   int                  `json:"count"`
	Events  []domain.EventRecord `json:"events"`
	LogText string               `json:"log_text,omitempty"`
	LogJSON string               `json:"log_json,omitempty"`
}

type RunForensics struct {
	Run                 domain.RunRecord     `json:"run"`
	LogText             string               `json:"log_text,omitempty"`
	LogJSON             string               `json:"log_json,omitempty"`
	InfoEvents          int                  `json:"info_events"`
	WarningEvents       int                  `json:"warning_events"`
	ErrorEvents         int                  `json:"error_events"`
	RetryEvents         int                  `json:"retry_events"`
	EventPayloadCount   int                  `json:"event_payload_count"`
	ComponentCounts     map[string]int       `json:"component_counts"`
	EntityCounts        map[string]int       `json:"entity_counts"`
	PhaseTimingsMS      map[string]int64     `json:"phase_timings_ms,omitempty"`
	ClassifiedMatched   int                  `json:"classified_matched"`
	ClassifiedUnmatched int                  `json:"classified_unmatched"`
	ReleaseSynced       int                  `json:"release_synced"`
	ReleaseUnchanged    int                  `json:"release_unchanged"`
	PublishPlanned      int                  `json:"publish_planned"`
	PublishSkipped      int                  `json:"publish_skipped"`
	PublishHeld         int                  `json:"publish_held,omitempty"`
	PublishSucceeded    int                  `json:"publish_succeeded"`
	PublishFailed       int                  `json:"publish_failed"`
	ProgressHighlights  []string             `json:"progress_highlights,omitempty"`
	Highlights          []string             `json:"highlights"`
	RecentErrors        []domain.EventRecord `json:"recent_errors,omitempty"`
}

type RunOnceResult struct {
	Sync    domain.SyncResult    `json:"sync"`
	Publish domain.PublishResult `json:"publish"`
}

func New(cfg *config.Config, roots config.Roots, configPath string, repo store.Repository, providers *provider.Registry) *Service {
	return &Service{
		Config:     cfg,
		Roots:      roots,
		ConfigPath: configPath,
		Repo:       repo,
		Providers:  providers,
		Files:      artifact.New(cfg.Runtime.ArtifactRoot),
	}
}

func (s *Service) Sync(ctx context.Context, sourceFilter string, dryRun bool, command string, decisions *RunDecisions) (result domain.SyncResult, err error) {
	if decisions == nil {
		decisions = NewRunDecisions(s, s.Config)
	}
	recorder, err := observe.Start(ctx, s.Repo, command, sourceFilter, dryRun, s.observeOptions())
	if err != nil {
		return domain.SyncResult{}, err
	}
	ctx = withRecorderProgress(ctx, recorder)
	result = domain.SyncResult{RunID: recorder.RunID()}
	defer func() {
		if err != nil {
			_ = recorder.Finish(context.WithoutCancel(ctx), domain.RunStatusFailed, err.Error())
		}
	}()
	sources := selectSources(s.Config.Sources, sourceFilter)
	if len(sources) == 0 {
		err = fmt.Errorf("no enabled sources match %q", sourceFilter)
		return result, err
	}
	var observed []observedBatch
	defer func() { s.reportObservedCandidates(ctx, observed, dryRun, &result) }()
	for _, sourceCfg := range sources {
		client, ok := s.Providers.Get(sourceCfg.Provider)
		if !ok {
			err = fmt.Errorf("no provider registered for %q", sourceCfg.Provider)
			return result, err
		}
		if auth, ok := s.Config.AuthProfileByID(sourceCfg.AuthProfile); ok {
			storedSource, getErr := s.Repo.GetSource(ctx, sourceCfg.ID)
			if getErr != nil {
				err = getErr
				return result, err
			}
			listResult, listErr := client.ListReleases(ctx, auth, sourceCfg, storedSource)
			_ = recorder.EventDataK(ctx, "info", observe.KindAuthState, "auth state "+string(listResult.AuthState), "source", sourceCfg.ID, map[string]any{
				"source_id":   sourceCfg.ID,
				"auth_state":  listResult.AuthState,
				"provider":    sourceCfg.Provider,
				"sync_cursor": strings.TrimSpace(listResult.SyncCursor) != "",
			})
			if listErr != nil {
				err = listErr
				_ = recorder.Event(ctx, "error", "provider", listErr.Error(), "source", sourceCfg.ID)
				return result, err
			}
			_ = recorder.EventDataK(ctx, "info", observe.KindReleasesFetched, fmt.Sprintf("fetched %d releases", len(listResult.Documents)), "source", sourceCfg.ID, map[string]any{
				"source_id":        sourceCfg.ID,
				"discovered_count": len(listResult.Documents),
			})
			observedAt := time.Now().UTC()
			observed = append(observed, observedBatch{Source: sourceCfg.ID, Documents: listResult.Documents, At: observedAt})
			batch := &observed[len(observed)-1]
			sourceDecisions, decideErr := s.decideSource(ctx, sourceCfg.ID, listResult.Documents, observedAt, s.Config)
			if decideErr != nil {
				// The deferred candidate pass recomputes with a cancel-tolerant
				// context so a canceled or otherwise interrupted sync still
				// retains fetched evidence.
				return result, decideErr
			}
			batch.Decisions = sourceDecisions
			// The rest of the run (volumes, delivery) reuses these decisions
			// instead of re-deciding the same releases from the store.
			decisions.Observe(sourceCfg.ID, sourceDecisions)
			rules := s.Config.Compiled()
			for _, doc := range listResult.Documents {
				result.Discovered++
				if !dryRun && doc.Normalized.Enrichment != nil {
					if err := s.saveEnrichment(ctx, sourceCfg.ID, doc.Normalized); err != nil {
						// Enrichment persistence is advisory; the in-memory
						// enrichment still feeds this run's decisions.
						result.DiscoveryNotices = append(result.DiscoveryNotices, fmt.Sprintf("%s enrichment persistence failed: %v", sourceCfg.ID, err))
					}
				}
				decision := authoringDecisionFor(sourceCfg.ID, doc.Normalized, sourceDecisions, s.Config, rules)
				classificationKind := observe.KindClassifyMatched
				classificationMessage := "classified release"
				if !decision.Matched {
					classificationKind = observe.KindClassifyUnmatched
					classificationMessage = "release unmatched fallback"
				}
				_ = recorder.EventDataK(ctx, "info", classificationKind, classificationMessage, "release", doc.Normalized.ProviderReleaseID, map[string]any{
					"source_id":           sourceCfg.ID,
					"provider_release_id": doc.Normalized.ProviderReleaseID,
					"title":               doc.Normalized.Title,
					"matched":             decision.Matched,
					"rule_id":             decision.RuleID,
					"track_key":           decision.TrackKey,
					"track_name":          decision.TrackName,
					"release_role":        decision.ReleaseRole,
					"content_strategy":    decision.ContentStrategy,
					"tags":                doc.Normalized.Tags,
					"collections":         doc.Normalized.Collections,
				})
				preparedDoc := doc
				if classify.CanMaterialize(doc.Normalized, decision) {
					var authState domain.AuthState
					var prepErr error
					preparedDoc, authState, prepErr = client.PrepareRelease(ctx, auth, sourceCfg, doc, decision)
					_ = recorder.Event(ctx, "info", "provider", "auth state "+string(authState), "source", sourceCfg.ID)
					if prepErr != nil {
						err = prepErr
						_ = recorder.Event(ctx, "error", "provider", prepErr.Error(), "source", sourceCfg.ID)
						return result, err
					}
				}
				action, changed, materialized, handleErr := s.intakeHandleRelease(ctx, recorder, sourceCfg, preparedDoc, decision, dryRun, false)
				if handleErr != nil {
					err = handleErr
					return result, err
				}
				result.Plans = append(result.Plans, action)
				if changed {
					result.Changed++
				} else {
					result.Unchanged++
				}
				if materialized {
					result.MaterializedArtifacts++
				}
			}

			if !dryRun {
				sourceState := mergeSourceSyncState(sourceCfg, storedSource, listResult.Documents, listResult.SyncCursor)
				if sourceState != nil {
					if upsertErr := s.Repo.UpsertSource(ctx, *sourceState); upsertErr != nil {
						err = upsertErr
						return result, err
					}
				}
			}
			continue
		}
		err = fmt.Errorf("source %q references missing auth profile %q", sourceCfg.ID, sourceCfg.AuthProfile)
		return result, err
	}

	summary := fmt.Sprintf("discovered=%d changed=%d unchanged=%d materialized=%d", result.Discovered, result.Changed, result.Unchanged, result.MaterializedArtifacts)
	if finishErr := recorder.Finish(ctx, domain.RunStatusSucceeded, summary); finishErr != nil {
		return result, finishErr
	}
	return result, nil
}

func (s *Service) BootstrapAuth(ctx context.Context, sourceFilter, authFilter string, force bool, command string) (AuthBootstrapResult, error) {
	scope := strings.TrimSpace(sourceFilter)
	if strings.TrimSpace(authFilter) != "" {
		scope = strings.TrimSpace(authFilter)
	}
	recorder, err := observe.Start(ctx, s.Repo, command, scope, false, s.observeOptions())
	if err != nil {
		return AuthBootstrapResult{}, err
	}
	ctx = withRecorderProgress(ctx, recorder)
	result := AuthBootstrapResult{RunID: recorder.RunID()}
	var failures []string
	sources := selectSources(s.Config.Sources, sourceFilter)
	if authFilter != "" {
		filtered := make([]config.SourceConfig, 0, len(sources))
		for _, source := range sources {
			if source.AuthProfile == authFilter {
				filtered = append(filtered, source)
			}
		}
		sources = filtered
	}
	if len(s.Config.Sources) == 0 && sourceFilter == "" {
		result, err = s.bootstrapProfile(ctx, authFilter, force, result.RunID)
		status := domain.RunStatusSucceeded
		if err != nil {
			status = domain.RunStatusFailed
		}
		finishErr := recorder.Finish(context.WithoutCancel(ctx), status, "profile authentication")
		return result, errors.Join(err, finishErr)
	}
	if len(sources) == 0 {
		err = fmt.Errorf("no enabled sources match source=%q auth_profile=%q", sourceFilter, authFilter)
		_ = recorder.Finish(ctx, domain.RunStatusFailed, err.Error())
		return result, err
	}
	for _, sourceCfg := range sources {
		client, ok := s.Providers.Get(sourceCfg.Provider)
		if !ok {
			err = fmt.Errorf("no provider registered for %q", sourceCfg.Provider)
			failures = append(failures, err.Error())
			result.Failed++
			result.Items = append(result.Items, AuthBootstrapItem{
				SourceID:      sourceCfg.ID,
				Provider:      sourceCfg.Provider,
				AuthProfileID: sourceCfg.AuthProfile,
				AuthState:     domain.AuthStateReauthRequired,
				Action:        "failed",
				Message:       err.Error(),
			})
			_ = recorder.Event(ctx, "error", "auth", err.Error(), "source", sourceCfg.ID)
			continue
		}
		auth, ok := s.Config.AuthProfileByID(sourceCfg.AuthProfile)
		if !ok {
			err = fmt.Errorf("source %q references missing auth profile %q", sourceCfg.ID, sourceCfg.AuthProfile)
			failures = append(failures, err.Error())
			result.Failed++
			result.Items = append(result.Items, AuthBootstrapItem{
				SourceID:      sourceCfg.ID,
				Provider:      sourceCfg.Provider,
				AuthProfileID: sourceCfg.AuthProfile,
				AuthState:     domain.AuthStateReauthRequired,
				Action:        "failed",
				Message:       err.Error(),
			})
			_ = recorder.Event(ctx, "error", "auth", err.Error(), "source", sourceCfg.ID)
			continue
		}
		boot, bootErr := client.BootstrapAuth(ctx, auth, sourceCfg, force)
		item := AuthBootstrapItem{
			SourceID:      sourceCfg.ID,
			Provider:      sourceCfg.Provider,
			AuthProfileID: auth.ID,
			AuthState:     boot.State,
			Action:        firstNonEmptyAction(boot.Action, "verified"),
		}
		if bootErr != nil {
			item.Message = bootErr.Error()
			item.Action = "failed"
			result.Failed++
			failures = append(failures, fmt.Sprintf("%s: %s", sourceCfg.ID, bootErr.Error()))
			_ = recorder.Event(ctx, "error", "auth", bootErr.Error(), "source", sourceCfg.ID)
		} else {
			switch item.Action {
			case "bootstrapped":
				result.Bootstrapped++
			default:
				result.Verified++
			}
			_ = recorder.Event(ctx, "info", "auth", item.Action+" auth session", "source", sourceCfg.ID)
		}
		result.Items = append(result.Items, item)
	}
	status := domain.RunStatusSucceeded
	summary := fmt.Sprintf("verified=%d bootstrapped=%d failed=%d", result.Verified, result.Bootstrapped, result.Failed)
	if len(failures) > 0 {
		status = domain.RunStatusFailed
	}
	if finishErr := recorder.Finish(ctx, status, summary); finishErr != nil {
		return result, finishErr
	}
	if len(failures) > 0 {
		return result, fmt.Errorf("auth bootstrap failed for %d source(s): %s", len(failures), strings.Join(failures, "; "))
	}
	return result, nil
}

func (s *Service) ImportAuthSession(ctx context.Context, sourceFilter, authFilter, sessionFile, command string) (AuthImportResult, error) {
	recorder, err := observe.Start(ctx, s.Repo, command, strings.TrimSpace(authFilter), false, s.observeOptions())
	if err != nil {
		return AuthImportResult{}, err
	}
	ctx = withRecorderProgress(ctx, recorder)
	result := AuthImportResult{RunID: recorder.RunID()}
	defer func() {
		if err != nil {
			_ = recorder.Finish(ctx, domain.RunStatusFailed, err.Error())
		}
	}()
	sessionBytes, err := os.ReadFile(sessionFile)
	if err != nil {
		return result, fmt.Errorf("read session bundle %s: %w", sessionFile, err)
	}
	sources := selectSources(s.Config.Sources, sourceFilter)
	if authFilter != "" {
		filtered := make([]config.SourceConfig, 0, len(sources))
		for _, source := range sources {
			if source.AuthProfile == authFilter {
				filtered = append(filtered, source)
			}
		}
		sources = filtered
	}
	if len(sources) == 0 {
		err = fmt.Errorf("no enabled sources match source=%q auth_profile=%q", sourceFilter, authFilter)
		return result, err
	}
	importedAuthProfiles := map[string]bool{}
	var failures []string
	for _, sourceCfg := range sources {
		client, ok := s.Providers.Get(sourceCfg.Provider)
		if !ok {
			err = fmt.Errorf("no provider registered for %q", sourceCfg.Provider)
			failures = append(failures, err.Error())
			result.Failed++
			result.Items = append(result.Items, AuthImportItem{
				SourceID:      sourceCfg.ID,
				Provider:      sourceCfg.Provider,
				AuthProfileID: sourceCfg.AuthProfile,
				AuthState:     domain.AuthStateReauthRequired,
				Action:        "failed",
				Message:       err.Error(),
			})
			_ = recorder.Event(ctx, "error", "auth", err.Error(), "source", sourceCfg.ID)
			continue
		}
		auth, ok := s.Config.AuthProfileByID(sourceCfg.AuthProfile)
		if !ok {
			err = fmt.Errorf("source %q references missing auth profile %q", sourceCfg.ID, sourceCfg.AuthProfile)
			failures = append(failures, err.Error())
			result.Failed++
			result.Items = append(result.Items, AuthImportItem{
				SourceID:      sourceCfg.ID,
				Provider:      sourceCfg.Provider,
				AuthProfileID: sourceCfg.AuthProfile,
				AuthState:     domain.AuthStateReauthRequired,
				Action:        "failed",
				Message:       err.Error(),
			})
			_ = recorder.Event(ctx, "error", "auth", err.Error(), "source", sourceCfg.ID)
			continue
		}
		action := "validated"
		if !importedAuthProfiles[auth.ID] {
			if err := writeSessionBundle(auth.SessionPath, sessionBytes); err != nil {
				failures = append(failures, err.Error())
				result.Failed++
				result.Items = append(result.Items, AuthImportItem{
					SourceID:      sourceCfg.ID,
					Provider:      sourceCfg.Provider,
					AuthProfileID: auth.ID,
					SessionPath:   auth.SessionPath,
					AuthState:     domain.AuthStateReauthRequired,
					Action:        "failed",
					Message:       err.Error(),
				})
				_ = recorder.Event(ctx, "error", "auth", err.Error(), "source", sourceCfg.ID)
				continue
			}
			importedAuthProfiles[auth.ID] = true
			action = "imported"
			result.Imported++
			_ = recorder.Event(ctx, "info", "auth", "imported session bundle", "source", sourceCfg.ID)
		}
		authState, validateErr := client.ValidateSession(ctx, auth, sourceCfg)
		item := AuthImportItem{
			SourceID:      sourceCfg.ID,
			Provider:      sourceCfg.Provider,
			AuthProfileID: auth.ID,
			SessionPath:   auth.SessionPath,
			AuthState:     authState,
			Action:        action,
		}
		if validateErr != nil {
			item.Action = "failed"
			item.Message = validateErr.Error()
			result.Failed++
			failures = append(failures, validateErr.Error())
			_ = recorder.Event(ctx, "error", "auth", validateErr.Error(), "source", sourceCfg.ID)
		} else {
			result.Validated++
			_ = recorder.Event(ctx, "info", "auth", action+" session bundle", "source", sourceCfg.ID)
		}
		result.Items = append(result.Items, item)
	}
	status := domain.RunStatusSucceeded
	summary := fmt.Sprintf("imported=%d validated=%d failed=%d", result.Imported, result.Validated, result.Failed)
	if len(failures) > 0 {
		status = domain.RunStatusFailed
	}
	if finishErr := recorder.Finish(ctx, status, summary); finishErr != nil {
		return result, finishErr
	}
	if len(failures) > 0 {
		return result, fmt.Errorf("session import failed for %d source(s): %s", len(failures), strings.Join(failures, "; "))
	}
	return result, nil
}

func (s *Service) Publish(ctx context.Context, sourceFilter, targetFilter string, dryRun bool, command string) (domain.PublishResult, error) {
	return s.publish(ctx, deliveryScope{SourceID: sourceFilter, SeriesID: "", Rebuild: false}, targetFilter, dryRun, command, nil, nil)
}

// publish takes the run's decisions value: a sync pass that just decided the
// sources hands those decisions to volume planning through it rather than
// re-deciding the same releases.
func (s *Service) publish(ctx context.Context, scope deliveryScope, targetFilter string, dryRun bool, command string, blockedSeries map[string]bool, decisions *RunDecisions) (result domain.PublishResult, err error) {
	if decisions == nil {
		decisions = NewRunDecisions(s, s.Config)
	}
	if err := s.validatePublishTargets(s.Config, scope, targetFilter); err != nil {
		return domain.PublishResult{}, err
	}
	recorder, err := observe.Start(ctx, s.Repo, command, scope.SourceID, dryRun, s.observeOptions())
	if err != nil {
		return domain.PublishResult{}, err
	}
	ctx = withRecorderProgress(ctx, recorder)
	result = domain.PublishResult{RunID: recorder.RunID(), DryRun: dryRun}
	defer func() {
		if err != nil {
			_ = recorder.Finish(ctx, domain.RunStatusFailed, err.Error())
		}
	}()
	targets := selectPublishers(s.Config.Publishers, targetFilter)
	if len(targets) == 0 {
		err = fmt.Errorf("no enabled publishers match %q", targetFilter)
		return result, err
	}
	if !dryRun {
		if blockedSeries == nil {
			blockedSeries = map[string]bool{}
		}
		var volumeErr error
		result.Volumes, volumeErr = s.prepareVolumes(ctx, scope.SourceID, scope.SeriesID, scope.Rebuild, blockedSeries, decisions)
		if volumeErr != nil {
			result.Failed++
			result.Items = append(result.Items, domain.PublishItemResult{Action: "failed", Message: volumeErr.Error()})
		}
	}
	candidates, err := s.Repo.ListPublishCandidates(ctx, "")
	if err != nil {
		return result, err
	}
	selected, recordsByTarget, editions, err := s.deliverySelection(ctx, s.Config, scope, targets, candidates, blockedSeries, nil)
	if err != nil {
		return result, err
	}
	if err := s.validateLegacyReplacements(ctx, targets, selected, recordsByTarget); err != nil {
		return result, err
	}
	if !scope.Rebuild {
		if err := s.validateFrozenNames(ctx, targets, selected, recordsByTarget); err != nil {
			return result, err
		}
	}
	for _, target := range targets {
		pt, targetErr := publish.TargetFor(target)
		if targetErr != nil {
			result.Failed++
			result.Items = append(result.Items, domain.PublishItemResult{TargetID: target.ID, Action: "blocked", Message: targetErr.Error()})
			continue
		}
		outcome, execErr := s.executeDelivery(ctx, recorder, scope, pt, target, selected, recordsByTarget[target.ID], editions, dryRun)
		if execErr != nil {
			return result, execErr
		}
		outcome.fold(&result)
	}
	summaryVerb := "published"
	if dryRun {
		summaryVerb = "planned"
	}
	summary := fmt.Sprintf("%s=%d skipped=%d failed=%d", summaryVerb, result.Published, result.Skipped, result.Failed)
	status := domain.RunStatusSucceeded
	if result.Failed > 0 {
		status = domain.RunStatusFailed
	}
	if finishErr := recorder.Finish(ctx, status, summary); finishErr != nil {
		return result, finishErr
	}
	if result.Failed > 0 {
		return result, fmt.Errorf("publication incomplete: %s (run %s)", summary, result.RunID)
	}
	return result, nil
}

// deliverySelection turns a target's chapter candidates into the selected
// delivery set: volume candidates replace their members' chapters, collisions
// resolve, the scope filter applies, and the library snapshot loads once.
// Publish and the rebuild preview share this step.
func (s *Service) deliverySelection(ctx context.Context, cfg *config.Config, scope deliveryScope, targets []config.PublisherConfig, chapters []domain.PublishCandidate, blockedSeries map[string]bool, existing []domain.VolumeEdition) (selected []domain.PublishCandidate, recordsByTarget map[string][]domain.PublishRecordBundle, volumes []domain.VolumeEdition, err error) {
	if existing == nil {
		existing, err = s.Repo.ListVolumeEditions(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	candidates, err := s.volumeCandidates(ctx, chapters, existing)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := resolveCollisionNames(candidates); err != nil {
		return nil, nil, nil, err
	}
	selected = make([]domain.PublishCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if blockedSeries[candidate.Track.TrackKey] {
			continue
		}
		if !s.sourceInScope(candidate.Source.ID, scope.SourceID, scope.Rebuild, cfg) || scope.SeriesID != "" && candidate.Track.TrackKey != scope.SeriesID {
			continue
		}
		selected = append(selected, candidate)
	}
	recordsByTarget = map[string][]domain.PublishRecordBundle{}
	for _, target := range targets {
		records, err := s.Repo.ListPublishRecords(ctx, "", target.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		recordsByTarget[target.ID] = records
	}
	return selected, recordsByTarget, existing, nil
}

func (s *Service) RunOnce(ctx context.Context, sourceFilter, targetFilter, command string) (result RunOnceResult, err error) {
	defer func() {
		if err == nil {
			return
		}
		notificationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for _, target := range selectPublishers(s.Config.Publishers, targetFilter) {
			if notificationErr := publish.RunLifecycle(notificationCtx, target.LifecycleCommand, publish.LifecycleEvent{Action: "complete", RunID: result.Sync.RunID, TargetID: target.ID, Succeeded: false}); notificationErr != nil {
				err = errors.Join(err, notificationErr)
			}
		}
	}()
	if err := s.validatePublishTargets(s.Config, deliveryScope{SourceID: sourceFilter, SeriesID: "", Rebuild: false}, targetFilter); err != nil {
		return result, err
	}
	runDecisions := NewRunDecisions(s, s.Config)
	syncResult, err := s.Sync(ctx, sourceFilter, false, command+" sync", runDecisions)
	result.Sync = syncResult
	if err != nil {
		return result, err
	}
	publishResult, err := s.publish(ctx, deliveryScope{SourceID: sourceFilter, SeriesID: "", Rebuild: false}, targetFilter, false, command+" publish", nil, runDecisions)
	result.Publish = publishResult
	if err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) resolveTrack(ctx context.Context, source domain.Source, decision domain.TrackDecision) (domain.StoryTrack, error) {
	existing, err := s.Repo.GetTrackBySourceAndKey(ctx, source.ID, decision.TrackKey)
	if err != nil {
		return domain.StoryTrack{}, err
	}
	canonicalAuthor := firstNonEmpty(decision.CanonicalAuthor, source.CreatorName)
	seriesMeta := buildSeriesMeta(decision)
	if existing != nil {
		existing.TrackName = decision.TrackName
		existing.CanonicalAuthor = canonicalAuthor
		existing.SeriesMeta = seriesMeta
		existing.OutputPolicy = string(decision.OutputFormat)
		existing.UpdatedAt = time.Now().UTC()
		return *existing, nil
	}
	return domain.StoryTrack{
		ID:              "trk_" + uuid.NewString(),
		SourceID:        source.ID,
		TrackKey:        decision.TrackKey,
		TrackName:       decision.TrackName,
		CanonicalAuthor: canonicalAuthor,
		SeriesMeta:      seriesMeta,
		OutputPolicy:    string(decision.OutputFormat),
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}, nil
}

func buildSeriesMeta(decision domain.TrackDecision) string {
	meta := map[string]any{
		"series_id":        decision.SeriesID,
		"output_format":    decision.OutputFormat,
		"preface_mode":     decision.PrefaceMode,
		"content_strategy": decision.ContentStrategy,
	}
	if strings.TrimSpace(decision.CanonicalAuthor) != "" {
		meta["canonical_author"] = decision.CanonicalAuthor
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return string(data)
}

func (s *Service) InspectSource(ctx context.Context, id string) (*SourceInspect, error) {
	cfgSource, ok := s.Config.SourceByID(id)
	if !ok {
		return nil, fmt.Errorf("unknown source %q", id)
	}
	stored, err := s.Repo.GetSource(ctx, id)
	if err != nil {
		return nil, err
	}
	tracks, err := s.Repo.ListTracks(ctx, id)
	if err != nil {
		return nil, err
	}
	releases, err := s.Repo.ListReleases(ctx, id)
	if err != nil {
		return nil, err
	}
	return &SourceInspect{ConfigSource: cfgSource, StoredSource: stored, Tracks: tracks, Releases: releases}, nil
}

func (s *Service) InspectRun(ctx context.Context, id string) (*domain.RunBundle, error) {
	run, err := s.Repo.GetRunBundle(ctx, id)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, fmt.Errorf("unknown run %q", id)
	}
	events, err := s.loadRunEvents(id, run.Events)
	if err != nil {
		return nil, err
	}
	run.Events = events
	return run, nil
}

func (s *Service) ListRuns(ctx context.Context, limit int) ([]domain.RunRecord, error) {
	return s.Repo.ListRuns(ctx, limit)
}

func (s *Service) ListRunEvents(ctx context.Context, runID string, filter RunEventFilter) (*RunEventList, error) {
	bundle, err := s.InspectRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	events := make([]domain.EventRecord, 0, len(bundle.Events))
	for _, event := range bundle.Events {
		if !matchesRunEventFilter(event, filter) {
			continue
		}
		events = append(events, event)
	}
	if filter.Limit > 0 && len(events) > filter.Limit {
		events = events[:filter.Limit]
	}
	return &RunEventList{
		RunID:   runID,
		Filter:  filter,
		Count:   len(events),
		Events:  events,
		LogText: filepath.Join(s.Config.Runtime.LogRoot, runID+".log"),
		LogJSON: filepath.Join(s.Config.Runtime.LogRoot, runID+".jsonl"),
	}, nil
}

func (s *Service) ExplainRun(ctx context.Context, runID string) (*RunForensics, error) {
	bundle, err := s.InspectRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	// The run record is the durable log, and internal/observe owns reading it.
	// Forensics is that read, named for the CLI; nothing here re-derives counts.
	record, err := observe.ReadRun(s.Config.Runtime.LogRoot, runID)
	if err != nil {
		return nil, err
	}
	result := &RunForensics{
		Run:                 bundle.Run,
		LogText:             filepath.Join(s.Config.Runtime.LogRoot, runID+".log"),
		LogJSON:             filepath.Join(s.Config.Runtime.LogRoot, runID+".jsonl"),
		InfoEvents:          record.InfoEvents,
		WarningEvents:       record.WarningEvents,
		ErrorEvents:         record.ErrorEvents,
		RetryEvents:         record.RetryEvents,
		EventPayloadCount:   record.EventPayloadCount,
		ComponentCounts:     record.ComponentCounts,
		EntityCounts:        record.EntityCounts,
		PhaseTimingsMS:      record.PhaseTimingsMS,
		ClassifiedMatched:   record.KindCounts[observe.KindClassifyMatched],
		ClassifiedUnmatched: record.KindCounts[observe.KindClassifyUnmatched],
		ReleaseSynced:       record.KindCounts[observe.KindReleaseSynced],
		ReleaseUnchanged:    record.KindCounts[observe.KindReleaseUnchanged],
		PublishPlanned:      record.KindCounts[observe.KindPublishPlanned],
		PublishSkipped:      record.KindCounts[observe.KindPublishSkipped],
		PublishHeld:         record.KindCounts[observe.KindPublishHeld],
		PublishSucceeded:    record.KindCounts[observe.KindPublishCompleted],
		PublishFailed:       record.KindCounts[observe.KindPublishFailed],
		ProgressHighlights:  record.ProgressHighlights,
	}
	for _, event := range record.RecentErrors {
		result.RecentErrors = append(result.RecentErrors, eventRecordFromLog(event))
	}
	result.Highlights = append(result.Highlights, explainRunHighlights(bundle, result)...)
	return result, nil
}

func (s *Service) ListPublishRecords(ctx context.Context, sourceFilter, targetFilter string) ([]domain.PublishRecordBundle, error) {
	return s.Repo.ListPublishRecords(ctx, sourceFilter, targetFilter)
}

func (s *Service) InspectPublishRecord(ctx context.Context, id string) (*domain.PublishRecordBundle, error) {
	record, err := s.Repo.GetPublishRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("unknown publish record %q", id)
	}
	return record, nil
}

func (s *Service) SupportBundle(ctx context.Context, runID string) (string, error) {
	bundle, err := s.InspectRun(ctx, runID)
	if err != nil {
		return "", err
	}
	forensics, err := s.ExplainRun(ctx, runID)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(s.Config.Runtime.SupportRoot, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	runJSON, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), runJSON, 0o644); err != nil {
		return "", err
	}
	configJSON, err := json.MarshalIndent(redactConfigForSupport(s.Config), "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "config.redacted.json"), configJSON, 0o644); err != nil {
		return "", err
	}
	forensicsJSON, err := json.MarshalIndent(forensics, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "forensics.json"), forensicsJSON, 0o644); err != nil {
		return "", err
	}
	logFiles := make([]string, 0, 2)
	for _, logPath := range []string{
		filepath.Join(s.Config.Runtime.LogRoot, runID+".log"),
		filepath.Join(s.Config.Runtime.LogRoot, runID+".jsonl"),
	} {
		if relPath, err := copySupportFile(logPath, filepath.Join(dir, "logs")); err != nil {
			return "", err
		} else if relPath != "" {
			logFiles = append(logFiles, relPath)
		}
	}
	releaseIDs, artifactIDs := collectRunEntityIDs(bundle)
	sourceIDs := collectRunSourceIDs(bundle)
	for _, sourceID := range sourceIDs {
		source, err := s.Repo.GetSource(ctx, sourceID)
		if err != nil || source == nil {
			continue
		}
		sourceJSON, err := json.MarshalIndent(source, "", "  ")
		if err != nil {
			return "", err
		}
		sourcePath := filepath.Join(dir, "sources", sourceID+".json")
		if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(sourcePath, sourceJSON, 0o644); err != nil {
			return "", err
		}
	}
	for _, releaseID := range releaseIDs {
		releaseBundle, err := s.Repo.GetReleaseBundle(ctx, releaseID)
		if err != nil || releaseBundle == nil {
			continue
		}
		releaseJSON, err := json.MarshalIndent(releaseBundle, "", "  ")
		if err != nil {
			return "", err
		}
		releasePath := filepath.Join(dir, "releases", releaseID+".json")
		if err := os.MkdirAll(filepath.Dir(releasePath), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(releasePath, releaseJSON, 0o644); err != nil {
			return "", err
		}
		if _, err := copySupportFile(releaseBundle.Release.NormalizedPayloadRef, filepath.Join(dir, "payloads", "releases", releaseID)); err != nil {
			return "", err
		}
		if _, err := copySupportFile(releaseBundle.Release.RawPayloadRef, filepath.Join(dir, "payloads", "releases", releaseID)); err != nil {
			return "", err
		}
		for _, artifact := range releaseBundle.Artifacts {
			if _, err := copySupportFile(artifact.MetadataRef, filepath.Join(dir, "payloads", "artifacts", artifact.ID)); err != nil {
				return "", err
			}
			if _, err := copySupportFile(artifact.NormalizedRef, filepath.Join(dir, "payloads", "artifacts", artifact.ID)); err != nil {
				return "", err
			}
			if _, err := copySupportFile(artifact.RawRef, filepath.Join(dir, "payloads", "artifacts", artifact.ID)); err != nil {
				return "", err
			}
		}
	}
	eventPayloads := make([]string, 0, len(bundle.Events))
	for _, event := range bundle.Events {
		if event.PayloadRef == "" {
			continue
		}
		relPath, err := copySupportFile(event.PayloadRef, filepath.Join(dir, "payloads", "events", event.ID))
		if err != nil {
			return "", err
		}
		if relPath != "" {
			eventPayloads = append(eventPayloads, relPath)
		}
	}
	manifest := map[string]any{
		"run_id":              runID,
		"generated_at":        time.Now().UTC(),
		"config_path":         redactPathForSupport(s.ConfigPath),
		"source_ids":          sourceIDs,
		"log_files":           logFiles,
		"release_ids":         releaseIDs,
		"artifact_ids":        artifactIDs,
		"event_payload_files": eventPayloads,
		"forensics_file":      filepath.Join(dir, "forensics.json"),
		"redactions": []string{
			"auth env var names are redacted",
			"session paths and runtime storage paths are redacted",
			"exec publisher arguments after argv[0] are redacted",
		},
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestJSON, 0o644); err != nil {
		return "", err
	}
	readme := "serial-sync support bundle\nrun_id=" + runID + "\nconfig_path=" + redactPathForSupport(s.ConfigPath) + "\nredactions=env names, session paths, runtime storage paths, exec args\n"
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte(readme), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Service) selectAuthProfile(authFilter string) (config.AuthProfile, error) {
	if strings.TrimSpace(authFilter) != "" {
		auth, ok := s.Config.AuthProfileByID(strings.TrimSpace(authFilter))
		if !ok {
			return config.AuthProfile{}, fmt.Errorf("unknown auth profile %q", authFilter)
		}
		return auth, nil
	}
	if len(s.Config.AuthProfiles) == 1 {
		return s.Config.AuthProfiles[0], nil
	}
	return config.AuthProfile{}, fmt.Errorf("multiple auth profiles are configured; pass --auth-profile")
}

func (s *Service) observeOptions() observe.Options {
	return observe.Options{
		LogRoot: s.Config.Runtime.LogRoot,
	}
}

func (s *Service) sourceInScope(sourceID, sourceFilter string, rebuild bool, cfg *config.Config) bool {
	if sourceFilter != "" && sourceID != sourceFilter {
		return false
	}
	if !rebuild {
		return true
	}
	for _, source := range cfg.Sources {
		if source.ID == sourceID {
			return source.Enabled
		}
	}
	return false
}

func selectSources(all []config.SourceConfig, sourceFilter string) []config.SourceConfig {
	var out []config.SourceConfig
	for _, source := range all {
		if !source.Enabled {
			continue
		}
		if sourceFilter != "" && source.ID != sourceFilter {
			continue
		}
		out = append(out, source)
	}
	return out
}

func selectPublishers(all []config.PublisherConfig, targetFilter string) []config.PublisherConfig {
	var out []config.PublisherConfig
	for _, publisher := range all {
		if !publisher.Enabled {
			continue
		}
		if targetFilter != "" && publisher.ID != targetFilter {
			continue
		}
		out = append(out, publisher)
	}
	return out
}

func hashBytes(input []byte) string {
	sum := sha256.Sum256(input)
	return hex.EncodeToString(sum[:])
}

func hashableNormalizedRelease(release domain.NormalizedRelease) domain.NormalizedRelease {
	cloned := release
	cloned.Enrichment = nil
	if len(release.Attachments) == 0 {
		return cloned
	}
	cloned.Attachments = make([]domain.Attachment, len(release.Attachments))
	copy(cloned.Attachments, release.Attachments)
	for idx := range cloned.Attachments {
		cloned.Attachments[idx].DownloadURL = ""
		cloned.Attachments[idx].LocalPath = ""
	}
	return cloned
}

func prettyJSON(input []byte) []byte {
	var payload any
	if err := json.Unmarshal(input, &payload); err != nil {
		return input
	}
	pretty, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return input
	}
	return pretty
}

func mergeSourceSyncState(sourceCfg config.SourceConfig, stored *domain.Source, docs []provider.ReleaseDocument, syncCursor string) *domain.Source {
	if stored == nil && len(docs) == 0 {
		return nil
	}
	merged := domain.Source{
		ID:            sourceCfg.ID,
		Provider:      sourceCfg.Provider,
		SourceURL:     sourceCfg.URL,
		AuthProfileID: sourceCfg.AuthProfile,
		Enabled:       sourceCfg.Enabled,
	}
	if stored != nil {
		merged = *stored
		merged.Provider = sourceCfg.Provider
		merged.SourceURL = sourceCfg.URL
		merged.AuthProfileID = sourceCfg.AuthProfile
		merged.Enabled = sourceCfg.Enabled
	}
	if len(docs) > 0 {
		sample := docs[0].Normalized
		if strings.TrimSpace(sample.SourceType) != "" {
			merged.SourceType = sample.SourceType
		}
		if strings.TrimSpace(sample.CreatorID) != "" {
			merged.CreatorID = sample.CreatorID
		}
		if strings.TrimSpace(sample.CreatorName) != "" {
			merged.CreatorName = sample.CreatorName
		}
	}
	merged.SyncCursor = syncCursor
	merged.LastSyncedAt = time.Now().UTC()
	return &merged
}

func collectRunEntityIDs(bundle *domain.RunBundle) ([]string, []string) {
	releaseSet := map[string]struct{}{}
	artifactSet := map[string]struct{}{}
	for _, event := range bundle.Events {
		switch event.EntityKind {
		case "release":
			if strings.TrimSpace(event.EntityID) != "" {
				releaseSet[event.EntityID] = struct{}{}
			}
		case "artifact":
			if strings.TrimSpace(event.EntityID) != "" {
				artifactSet[event.EntityID] = struct{}{}
			}
		}
	}
	releaseIDs := make([]string, 0, len(releaseSet))
	for id := range releaseSet {
		releaseIDs = append(releaseIDs, id)
	}
	artifactIDs := make([]string, 0, len(artifactSet))
	for id := range artifactSet {
		artifactIDs = append(artifactIDs, id)
	}
	sort.Strings(releaseIDs)
	sort.Strings(artifactIDs)
	return releaseIDs, artifactIDs
}

func collectRunSourceIDs(bundle *domain.RunBundle) []string {
	sourceSet := map[string]struct{}{}
	for _, event := range bundle.Events {
		if event.EntityKind != "source" {
			continue
		}
		if strings.TrimSpace(event.EntityID) == "" {
			continue
		}
		sourceSet[event.EntityID] = struct{}{}
	}
	sourceIDs := make([]string, 0, len(sourceSet))
	for id := range sourceSet {
		sourceIDs = append(sourceIDs, id)
	}
	sort.Strings(sourceIDs)
	return sourceIDs
}

func copySupportFile(src, dstDir string) (string, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return "", nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", err
	}
	dstPath := filepath.Join(dstDir, filepath.Base(src))
	if err := os.WriteFile(dstPath, data, 0o644); err != nil {
		return "", err
	}
	return dstPath, nil
}

func writeSessionBundle(dst string, payload []byte) error {
	if strings.TrimSpace(dst) == "" {
		return errors.New("session destination path is required")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, payload, 0o600)
}

func redactConfigForSupport(cfg *config.Config) *config.Config {
	redacted := *cfg
	redacted.Runtime.StoreDSN = redactPathForSupport(cfg.Runtime.StoreDSN)
	redacted.Runtime.LogRoot = redactPathForSupport(cfg.Runtime.LogRoot)
	redacted.Runtime.ArtifactRoot = redactPathForSupport(cfg.Runtime.ArtifactRoot)
	redacted.Runtime.SupportRoot = redactPathForSupport(cfg.Runtime.SupportRoot)
	redacted.AuthProfiles = append([]config.AuthProfile(nil), cfg.AuthProfiles...)
	for idx := range redacted.AuthProfiles {
		redacted.AuthProfiles[idx].UsernameEnv = redactEnvName(redacted.AuthProfiles[idx].UsernameEnv)
		redacted.AuthProfiles[idx].PasswordEnv = redactEnvName(redacted.AuthProfiles[idx].PasswordEnv)
		redacted.AuthProfiles[idx].TOTPSecretEnv = redactEnvName(redacted.AuthProfiles[idx].TOTPSecretEnv)
		redacted.AuthProfiles[idx].SessionPath = redactPathForSupport(redacted.AuthProfiles[idx].SessionPath)
	}
	redacted.Publishers = append([]config.PublisherConfig(nil), cfg.Publishers...)
	for idx := range redacted.Publishers {
		redacted.Publishers[idx].Path = redactPathForSupport(redacted.Publishers[idx].Path)
		redacted.Publishers[idx].Command = redactCommand(redacted.Publishers[idx].Command)
	}
	redacted.Sources = append([]config.SourceConfig(nil), cfg.Sources...)
	for idx := range redacted.Sources {
		redacted.Sources[idx].FixtureDir = redactPathForSupport(redacted.Sources[idx].FixtureDir)
	}
	redacted.Rules = append([]config.RuleConfig(nil), cfg.Rules...)
	return &redacted
}

func redactEnvName(input string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	return "<redacted-env>"
}

func redactCommand(command []string) []string {
	if len(command) == 0 {
		return nil
	}
	if len(command) == 1 {
		return []string{filepath.Base(command[0])}
	}
	return []string{filepath.Base(command[0]), "<redacted-args>"}
}

func redactPathForSupport(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	base := filepath.Base(input)
	if base == "." || base == string(filepath.Separator) {
		return "<redacted>"
	}
	return filepath.Join("<redacted>", base)
}

func matchesRunEventFilter(event domain.EventRecord, filter RunEventFilter) bool {
	if value := strings.TrimSpace(filter.Level); value != "" && !strings.EqualFold(event.Level, value) {
		return false
	}
	if value := strings.TrimSpace(filter.Component); value != "" && !strings.EqualFold(event.Component, value) {
		return false
	}
	if value := strings.TrimSpace(filter.EntityKind); value != "" && !strings.EqualFold(event.EntityKind, value) {
		return false
	}
	if value := strings.TrimSpace(filter.EntityID); value != "" && event.EntityID != value {
		return false
	}
	return true
}

func explainRunHighlights(bundle *domain.RunBundle, summary *RunForensics) []string {
	highlights := make([]string, 0, 6)
	if summary.ErrorEvents == 0 {
		highlights = append(highlights, "no error-level events recorded")
	} else {
		highlights = append(highlights, fmt.Sprintf("%d error event(s) recorded", summary.ErrorEvents))
	}
	if summary.ClassifiedUnmatched > 0 {
		highlights = append(highlights, fmt.Sprintf("%d release(s) fell through to unmatched fallback", summary.ClassifiedUnmatched))
	}
	if summary.PublishSkipped > 0 {
		highlights = append(highlights, fmt.Sprintf("%d publish action(s) were skipped as already up to date", summary.PublishSkipped))
	}
	if summary.PublishHeld > 0 {
		highlights = append(highlights, fmt.Sprintf("%d revised release(s) were held because an earlier version was already handed off", summary.PublishHeld))
	}
	if summary.PublishFailed > 0 {
		highlights = append(highlights, fmt.Sprintf("%d publish error(s) occurred", summary.PublishFailed))
	}
	if bundle.Run.Status == domain.RunStatusSucceeded && summary.ReleaseSynced == 0 && summary.ReleaseUnchanged > 0 {
		highlights = append(highlights, "run was effectively a no-op sync")
	}
	if bundle.Run.DryRun {
		highlights = append(highlights, "run was dry-run only")
	}
	return highlights
}

func FormatSyncResult(result domain.SyncResult) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("run_id: %s", result.RunID))
	lines = append(lines, FormatCandidates(result.Candidates))
	lines = append(lines, result.DiscoveryNotices...)
	lines = append(lines, fmt.Sprintf("discovered=%d changed=%d unchanged=%d materialized=%d", result.Discovered, result.Changed, result.Unchanged, result.MaterializedArtifacts))
	for _, item := range result.Plans {
		lines = append(lines, fmt.Sprintf("- [%s] %s -> %s (%s, %s)", item.Action, item.ProviderReleaseID, item.TrackKey, item.Strategy, item.Filename))
	}
	return strings.Join(lines, "\n")
}

func FormatPublishResult(result domain.PublishResult) string {
	verb := "published"
	if result.DryRun {
		verb = "planned"
	}
	lines := []string{
		fmt.Sprintf("run_id: %s", result.RunID),
		fmt.Sprintf("%s=%d skipped=%d failed=%d", verb, result.Published, result.Skipped, result.Failed),
	}
	for _, volume := range result.Volumes {
		lines = append(lines, formatVolumePlan(volume))
	}
	for _, item := range result.Items {
		line := fmt.Sprintf("- [%s] %s -> %s (%s, %s)", item.Action, item.ArtifactID, item.TargetID, item.TargetKind, item.TargetRef)
		if strings.TrimSpace(item.Message) != "" {
			line += " :: " + item.Message
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func FormatAuthBootstrapResult(result AuthBootstrapResult) string {
	lines := []string{
		fmt.Sprintf("run_id: %s", result.RunID),
		fmt.Sprintf("verified=%d bootstrapped=%d failed=%d", result.Verified, result.Bootstrapped, result.Failed),
	}
	for _, item := range result.Items {
		line := fmt.Sprintf("- [%s] %s -> %s (%s)", item.Action, item.SourceID, item.AuthProfileID, item.AuthState)
		if strings.TrimSpace(item.Message) != "" {
			line += " :: " + item.Message
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func FormatAuthImportResult(result AuthImportResult) string {
	lines := []string{
		fmt.Sprintf("run_id: %s", result.RunID),
		fmt.Sprintf("imported=%d validated=%d failed=%d", result.Imported, result.Validated, result.Failed),
	}
	for _, item := range result.Items {
		line := fmt.Sprintf("- [%s] %s -> %s (%s)", item.Action, item.SourceID, item.AuthProfileID, item.AuthState)
		if strings.TrimSpace(item.Message) != "" {
			line += " :: " + item.Message
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func FormatRunOnceResult(result RunOnceResult) string {
	return strings.Join([]string{
		"sync:",
		indentBlock(FormatSyncResult(result.Sync), "  "),
		"",
		"publish:",
		indentBlock(FormatPublishResult(result.Publish), "  "),
	}, "\n")
}

func FormatRunForensics(result RunForensics) string {
	lines := []string{
		fmt.Sprintf("run_id: %s", result.Run.ID),
		fmt.Sprintf("status=%s command=%s dry_run=%t", result.Run.Status, result.Run.Command, result.Run.DryRun),
		fmt.Sprintf("started_at=%s", result.Run.StartedAt.Format(time.RFC3339)),
		fmt.Sprintf("summary=%s", result.Run.Summary),
		fmt.Sprintf("logs: %s | %s", result.LogText, result.LogJSON),
		fmt.Sprintf(
			"classify matched=%d unmatched=%d | sync changed=%d unchanged=%d | publish planned=%d skipped=%d succeeded=%d failed=%d",
			result.ClassifiedMatched,
			result.ClassifiedUnmatched,
			result.ReleaseSynced,
			result.ReleaseUnchanged,
			result.PublishPlanned,
			result.PublishSkipped,
			result.PublishSucceeded,
			result.PublishFailed,
		),
	}
	if len(result.Highlights) > 0 {
		lines = append(lines, "highlights:")
		for _, highlight := range result.Highlights {
			lines = append(lines, "- "+highlight)
		}
	}
	if len(result.RecentErrors) > 0 {
		lines = append(lines, "recent_errors:")
		for _, event := range result.RecentErrors {
			lines = append(lines, fmt.Sprintf("- %s [%s/%s] %s", event.Timestamp.Format(time.RFC3339), event.Component, event.EntityID, event.Message))
		}
	}
	return strings.Join(lines, "\n")
}

func indentBlock(value, prefix string) string {
	lines := strings.Split(value, "\n")
	for idx := range lines {
		lines[idx] = prefix + lines[idx]
	}
	return strings.Join(lines, "\n")
}

func firstNonEmptyAction(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
