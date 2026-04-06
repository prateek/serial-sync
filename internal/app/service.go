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
	toml "github.com/pelletier/go-toml/v2"
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

type TrackInspect struct {
	Track    domain.StoryTrack `json:"track"`
	Releases []domain.Release  `json:"releases"`
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

type DiscoveryConfigSnippet struct {
	Sources []config.SourceConfig `json:"sources" toml:"sources"`
	Rules   []config.RuleConfig   `json:"rules" toml:"rules"`
}

type SourceDiscoverResult struct {
	RunID         string                      `json:"run_id"`
	Provider      string                      `json:"provider"`
	AuthProfileID string                      `json:"auth_profile_id"`
	AuthState     domain.AuthState            `json:"auth_state"`
	Options       provider.DiscoverOptions    `json:"options"`
	Suggestions   []provider.SourceSuggestion `json:"suggestions"`
	Snippet       DiscoveryConfigSnippet      `json:"snippet"`
	SnippetTOML   string                      `json:"snippet_toml"`
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

func (s *Service) Sync(ctx context.Context, sourceFilter string, dryRun bool, command string) (domain.SyncResult, error) {
	recorder, err := observe.Start(ctx, s.Repo, command, sourceFilter, dryRun, s.observeOptions())
	if err != nil {
		return domain.SyncResult{}, err
	}
	ctx = withRecorderProgress(ctx, recorder)
	result := domain.SyncResult{RunID: recorder.RunID()}
	defer func() {
		if err != nil {
			_ = recorder.Finish(ctx, domain.RunStatusFailed, err.Error())
		}
	}()
	sources := selectSources(s.Config.Sources, sourceFilter)
	if len(sources) == 0 {
		err = fmt.Errorf("no enabled sources match %q", sourceFilter)
		return result, err
	}
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
			_ = recorder.EventData(ctx, "info", "provider", "auth state "+string(listResult.AuthState), "source", sourceCfg.ID, map[string]any{
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
			_ = recorder.EventData(ctx, "info", "provider", fmt.Sprintf("fetched %d releases", len(listResult.Documents)), "source", sourceCfg.ID, map[string]any{
				"source_id":        sourceCfg.ID,
				"discovered_count": len(listResult.Documents),
			})
			for _, doc := range listResult.Documents {
				result.Discovered++
				decision := classify.Decide(sourceCfg.ID, doc.Normalized, s.Config.RulesForSource(sourceCfg.ID))
				classificationMessage := "classified release"
				if !decision.Matched {
					classificationMessage = "release unmatched fallback"
				}
				_ = recorder.EventData(ctx, "info", "classify", classificationMessage, "release", doc.Normalized.ProviderReleaseID, map[string]any{
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
				action, changed, materialized, handleErr := s.handleRelease(ctx, recorder, sourceCfg, preparedDoc, decision, dryRun)
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

func (s *Service) DiscoverSources(ctx context.Context, authFilter string, options provider.DiscoverOptions, command string) (SourceDiscoverResult, error) {
	scope := strings.TrimSpace(authFilter)
	recorder, err := observe.Start(ctx, s.Repo, command, scope, false, s.observeOptions())
	if err != nil {
		return SourceDiscoverResult{}, err
	}
	ctx = withRecorderProgress(ctx, recorder)
	result := SourceDiscoverResult{RunID: recorder.RunID(), Options: options}
	defer func() {
		if err != nil {
			_ = recorder.Finish(ctx, domain.RunStatusFailed, err.Error())
		}
	}()

	auth, err := s.selectAuthProfile(authFilter)
	if err != nil {
		return result, err
	}
	client, ok := s.Providers.Get(auth.Provider)
	if !ok {
		err = fmt.Errorf("no provider registered for %q", auth.Provider)
		return result, err
	}
	discovered, err := client.DiscoverSources(ctx, auth, s.Config.Sources, options)
	result.Provider = discovered.Provider
	result.AuthProfileID = auth.ID
	result.AuthState = discovered.AuthState
	result.Suggestions = discovered.Suggestions
	_ = recorder.Event(ctx, "info", "discover", "auth state "+string(discovered.AuthState), "auth_profile", auth.ID)
	if err != nil {
		return result, err
	}
	snippet := DiscoveryConfigSnippet{}
	for _, suggestion := range discovered.Suggestions {
		if suggestion.AlreadyConfigured && !options.IncludeConfigured {
			continue
		}
		snippet.Sources = append(snippet.Sources, suggestion.Source)
		snippet.Rules = append(snippet.Rules, suggestion.SuggestedRules...)
	}
	result.Snippet = snippet
	if len(snippet.Sources) > 0 || len(snippet.Rules) > 0 {
		payload, marshalErr := toml.Marshal(snippet)
		if marshalErr != nil {
			return result, marshalErr
		}
		result.SnippetTOML = string(payload)
	}
	_ = recorder.Event(ctx, "info", "discover", fmt.Sprintf("suggested %d Patreon source(s)", len(discovered.Suggestions)), "auth_profile", auth.ID)
	summary := fmt.Sprintf("suggested=%d new=%d", len(discovered.Suggestions), len(snippet.Sources))
	if finishErr := recorder.Finish(ctx, domain.RunStatusSucceeded, summary); finishErr != nil {
		return result, finishErr
	}
	return result, nil
}

func (s *Service) Publish(ctx context.Context, sourceFilter, targetFilter string, dryRun bool, command string) (domain.PublishResult, error) {
	recorder, err := observe.Start(ctx, s.Repo, command, sourceFilter, dryRun, s.observeOptions())
	if err != nil {
		return domain.PublishResult{}, err
	}
	ctx = withRecorderProgress(ctx, recorder)
	result := domain.PublishResult{RunID: recorder.RunID(), DryRun: dryRun}
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
	candidates, err := s.Repo.ListPublishCandidates(ctx, sourceFilter)
	if err != nil {
		return result, err
	}
	for _, candidate := range candidates {
		for _, target := range targets {
			targetKind, targetRef, publishHashInput, refErr := publishTargetIdentity(target, candidate)
			if refErr != nil {
				return result, refErr
			}
			publishHash := publish.PublishHash(target.ID, candidate.Artifact.SHA256, publishHashInput)
			done, err := s.Repo.HasSuccessfulPublish(ctx, candidate.Artifact.ID, target.ID, publishHash)
			if err != nil {
				return result, err
			}
			if done {
				result.Skipped++
				result.Items = append(result.Items, domain.PublishItemResult{
					ArtifactID: candidate.Artifact.ID,
					TargetID:   target.ID,
					TargetKind: targetKind,
					TargetRef:  targetRef,
					Action:     "skipped",
				})
				_ = recorder.EventData(ctx, "info", "publish", "publish skipped: identical artifact already published", "artifact", candidate.Artifact.ID, map[string]any{
					"artifact_id":  candidate.Artifact.ID,
					"target_id":    target.ID,
					"target_kind":  targetKind,
					"target_ref":   targetRef,
					"publish_hash": publishHash,
					"action":       "skipped",
				})
				continue
			}
			if dryRun {
				result.Published++
				result.Artifacts = append(result.Artifacts, candidate.Artifact.ID)
				result.Items = append(result.Items, domain.PublishItemResult{
					ArtifactID: candidate.Artifact.ID,
					TargetID:   target.ID,
					TargetKind: targetKind,
					TargetRef:  targetRef,
					Action:     "planned",
				})
				_ = recorder.EventData(ctx, "info", "publish", "planned "+targetKind+" publish", "artifact", candidate.Artifact.ID, result.Items[len(result.Items)-1])
				continue
			}
			record, pubErr := s.publishTarget(ctx, recorder.RunID(), target, candidate)
			if pubErr != nil {
				result.Failed++
				result.Items = append(result.Items, domain.PublishItemResult{
					ArtifactID: candidate.Artifact.ID,
					TargetID:   target.ID,
					TargetKind: targetKind,
					TargetRef:  targetRef,
					Action:     "failed",
					Message:    pubErr.Error(),
				})
				_ = s.Repo.UpsertPublishRecord(ctx, domain.PublishRecord{
					ID:          "pub_" + uuid.NewString(),
					ArtifactID:  candidate.Artifact.ID,
					TargetID:    target.ID,
					TargetKind:  targetKind,
					TargetRef:   targetRef,
					PublishHash: publishHash,
					PublishedAt: time.Now().UTC(),
					Status:      domain.PublishStatusFailed,
					Message:     pubErr.Error(),
				})
				_ = recorder.EventData(ctx, "error", "publish", pubErr.Error(), "artifact", candidate.Artifact.ID, result.Items[len(result.Items)-1])
				continue
			}
			if err := s.Repo.UpsertPublishRecord(ctx, record); err != nil {
				return result, err
			}
			result.Published++
			result.Artifacts = append(result.Artifacts, candidate.Artifact.ID)
			result.Items = append(result.Items, domain.PublishItemResult{
				ArtifactID: candidate.Artifact.ID,
				TargetID:   target.ID,
				TargetKind: record.TargetKind,
				TargetRef:  record.TargetRef,
				Action:     "published",
				Message:    record.Message,
			})
			_ = recorder.EventData(ctx, "info", "publish", record.TargetKind+" publish completed", "artifact", candidate.Artifact.ID, record)
		}
	}
	summaryVerb := "published"
	if dryRun {
		summaryVerb = "planned"
	}
	summary := fmt.Sprintf("%s=%d skipped=%d failed=%d", summaryVerb, result.Published, result.Skipped, result.Failed)
	if finishErr := recorder.Finish(ctx, domain.RunStatusSucceeded, summary); finishErr != nil {
		return result, finishErr
	}
	return result, nil
}

func (s *Service) RunOnce(ctx context.Context, sourceFilter, targetFilter, command string) (RunOnceResult, error) {
	result := RunOnceResult{}
	syncResult, err := s.Sync(ctx, sourceFilter, false, command+" sync")
	if err != nil {
		return result, err
	}
	publishResult, err := s.Publish(ctx, sourceFilter, targetFilter, false, command+" publish")
	if err != nil {
		return result, err
	}
	result.Sync = syncResult
	result.Publish = publishResult
	return result, nil
}

func (s *Service) publishTarget(ctx context.Context, runID string, target config.PublisherConfig, candidate domain.PublishCandidate) (domain.PublishRecord, error) {
	switch normalizedPublisherKind(target.Kind) {
	case "filesystem":
		return publish.PublishFilesystem(ctx, publish.FilesystemTarget{ID: target.ID, Path: target.Path}, candidate)
	case "exec":
		return publish.PublishExec(ctx, publish.ExecTarget{
			ID:      target.ID,
			Command: target.Command,
			RunID:   runID,
		}, candidate)
	default:
		return domain.PublishRecord{}, fmt.Errorf("unsupported publisher kind %q", target.Kind)
	}
}

func (s *Service) handleRelease(ctx context.Context, recorder *observe.Recorder, sourceCfg config.SourceConfig, doc provider.ReleaseDocument, decision domain.TrackDecision, dryRun bool) (domain.SyncItemPlan, bool, bool, error) {
	source := domain.Source{
		ID:            sourceCfg.ID,
		Provider:      sourceCfg.Provider,
		SourceURL:     sourceCfg.URL,
		SourceType:    doc.Normalized.SourceType,
		CreatorID:     doc.Normalized.CreatorID,
		CreatorName:   doc.Normalized.CreatorName,
		AuthProfileID: sourceCfg.AuthProfile,
		Enabled:       sourceCfg.Enabled,
	}
	existingRelease, err := s.Repo.GetReleaseByProviderID(ctx, source.ID, doc.Normalized.ProviderReleaseID)
	if err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	normalizedJSON, err := json.Marshal(doc.Normalized)
	if err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	contentHashJSON, err := json.Marshal(hashableNormalizedRelease(doc.Normalized))
	if err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	contentHash := hashBytes(contentHashJSON)
	releaseID := "rel_" + uuid.NewString()
	if existingRelease != nil {
		releaseID = existingRelease.ID
	}
	track, err := s.resolveTrack(ctx, source, decision)
	if err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	release := domain.Release{
		ID:                releaseID,
		SourceID:          source.ID,
		ProviderReleaseID: doc.Normalized.ProviderReleaseID,
		URL:               doc.Normalized.URL,
		Title:             doc.Normalized.Title,
		PublishedAt:       doc.Normalized.PublishedAt,
		EditedAt:          doc.Normalized.EditedAt,
		PostType:          doc.Normalized.PostType,
		VisibilityState:   doc.Normalized.VisibilityState,
		ContentHash:       contentHash,
		DiscoveredAt:      time.Now().UTC(),
		Status:            "discovered",
	}
	var artifactPlan domain.ArtifactPlan
	var artifactErr error
	if classify.CanMaterialize(doc.Normalized, decision) {
		artifactPlan, artifactErr = s.Files.Plan(source, track, release, doc.Normalized, decision, doc.RawJSON)
		if artifactErr != nil {
			return domain.SyncItemPlan{}, false, false, artifactErr
		}
	}
	action := "create"
	changed := true
	if existingRelease != nil {
		action = "update"
		existingArtifact, err := s.Repo.GetCanonicalArtifactByReleaseID(ctx, existingRelease.ID)
		if err != nil {
			return domain.SyncItemPlan{}, false, false, err
		}
		if existingRelease.ContentHash == contentHash {
			switch {
			case existingArtifact == nil && artifactPlan.SHA256 == "":
				action = "noop"
				changed = false
			case existingArtifact != nil && existingArtifact.SHA256 == artifactPlan.SHA256:
				action = "noop"
				changed = false
			}
		}
	}
	itemPlan := domain.SyncItemPlan{
		SourceID:          source.ID,
		ProviderReleaseID: doc.Normalized.ProviderReleaseID,
		Title:             doc.Normalized.Title,
		TrackKey:          decision.TrackKey,
		ReleaseRole:       decision.ReleaseRole,
		Strategy:          decision.ContentStrategy,
		ArtifactKind:      artifactPlan.ArtifactKind,
		Filename:          artifactPlan.Filename,
		Action:            action,
	}
	if !changed {
		_ = recorder.EventData(ctx, "info", "sync", "release unchanged", "release", release.ID, itemPlan)
		return itemPlan, false, false, nil
	}
	if dryRun {
		_ = recorder.EventData(ctx, "info", "sync", "planned release sync", "release", release.ID, itemPlan)
		return itemPlan, true, artifactPlan.SHA256 != "", nil
	}
	payloadDir := filepath.Join(s.Config.Runtime.ArtifactRoot, source.ID, track.TrackKey, release.ProviderReleaseID)
	if err := os.MkdirAll(payloadDir, 0o755); err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	release.NormalizedPayloadRef = filepath.Join(payloadDir, "release.normalized.json")
	release.RawPayloadRef = filepath.Join(payloadDir, "release.raw.json")
	if err := os.WriteFile(release.NormalizedPayloadRef, prettyJSON(normalizedJSON), 0o644); err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	if err := os.WriteFile(release.RawPayloadRef, prettyJSON(doc.RawJSON), 0o644); err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	assignment := domain.ReleaseAssignment{
		ReleaseID:   release.ID,
		TrackID:     track.ID,
		RuleID:      decision.RuleID,
		ReleaseRole: decision.ReleaseRole,
		Confidence:  1.0,
	}
	var art domain.Artifact
	materialized := false
	if artifactPlan.SHA256 != "" {
		art, err = s.Files.Materialize(ctx, source, track, release, artifactPlan)
		if err != nil {
			return domain.SyncItemPlan{}, false, false, err
		}
		materialized = true
	}
	if err := s.Repo.SaveSyncSnapshot(ctx, store.SyncSnapshot{
		Source:     source,
		Track:      track,
		Release:    release,
		Assignment: assignment,
		Artifact:   art,
	}); err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	_ = recorder.EventData(ctx, "info", "sync", "release synced", "release", release.ID, map[string]any{
		"plan":         itemPlan,
		"materialized": materialized,
		"artifact_id":  art.ID,
	})
	return itemPlan, true, materialized, nil
}

func (s *Service) resolveTrack(ctx context.Context, source domain.Source, decision domain.TrackDecision) (domain.StoryTrack, error) {
	existing, err := s.Repo.GetTrackBySourceAndKey(ctx, source.ID, decision.TrackKey)
	if err != nil {
		return domain.StoryTrack{}, err
	}
	if existing != nil {
		existing.TrackName = decision.TrackName
		existing.CanonicalAuthor = source.CreatorName
		existing.OutputPolicy = string(decision.ContentStrategy)
		existing.UpdatedAt = time.Now().UTC()
		return *existing, nil
	}
	return domain.StoryTrack{
		ID:              "trk_" + uuid.NewString(),
		SourceID:        source.ID,
		TrackKey:        decision.TrackKey,
		TrackName:       decision.TrackName,
		CanonicalAuthor: source.CreatorName,
		OutputPolicy:    string(decision.ContentStrategy),
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}, nil
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

func (s *Service) InspectTrack(ctx context.Context, ref string) (*TrackInspect, error) {
	tracks, err := s.Repo.ListTracks(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, track := range tracks {
		if track.ID != ref && track.TrackKey != ref {
			continue
		}
		releases, err := s.Repo.ListReleases(ctx, track.SourceID)
		if err != nil {
			return nil, err
		}
		var filtered []domain.Release
		for _, release := range releases {
			bundle, err := s.Repo.GetReleaseBundle(ctx, release.ID)
			if err != nil {
				return nil, err
			}
			if bundle != nil && bundle.Track.ID == track.ID {
				filtered = append(filtered, release)
			}
		}
		return &TrackInspect{Track: track, Releases: filtered}, nil
	}
	return nil, fmt.Errorf("unknown track %q", ref)
}

func (s *Service) InspectRelease(ctx context.Context, ref string) (*domain.ReleaseBundle, error) {
	if bundle, err := s.Repo.GetReleaseBundle(ctx, ref); err != nil {
		return nil, err
	} else if bundle != nil {
		return bundle, nil
	}
	for _, sourceCfg := range s.Config.Sources {
		release, err := s.Repo.GetReleaseByProviderID(ctx, sourceCfg.ID, ref)
		if err != nil {
			return nil, err
		}
		if release != nil {
			return s.Repo.GetReleaseBundle(ctx, release.ID)
		}
	}
	return nil, fmt.Errorf("unknown release %q", ref)
}

func (s *Service) InspectArtifact(ctx context.Context, id string) (*domain.Artifact, error) {
	artifact, err := s.Repo.GetArtifact(ctx, id)
	if err != nil {
		return nil, err
	}
	if artifact == nil {
		return nil, fmt.Errorf("unknown artifact %q", id)
	}
	return artifact, nil
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
	result := &RunForensics{
		Run:             bundle.Run,
		LogText:         filepath.Join(s.Config.Runtime.LogRoot, runID+".log"),
		LogJSON:         filepath.Join(s.Config.Runtime.LogRoot, runID+".jsonl"),
		ComponentCounts: map[string]int{},
		EntityCounts:    map[string]int{},
		PhaseTimingsMS:  map[string]int64{},
	}
	for _, event := range bundle.Events {
		switch strings.ToLower(strings.TrimSpace(event.Level)) {
		case "warn", "warning":
			result.WarningEvents++
		case "error":
			result.ErrorEvents++
			result.RecentErrors = append(result.RecentErrors, event)
		default:
			result.InfoEvents++
		}
		if component := strings.TrimSpace(event.Component); component != "" {
			result.ComponentCounts[component]++
		}
		if entityKind := strings.TrimSpace(event.EntityKind); entityKind != "" {
			result.EntityCounts[entityKind]++
		}
		if strings.TrimSpace(event.PayloadRef) != "" {
			result.EventPayloadCount++
		}
		switch event.Component {
		case "classify":
			if strings.Contains(strings.ToLower(event.Message), "unmatched") {
				result.ClassifiedUnmatched++
			} else {
				result.ClassifiedMatched++
			}
		case "sync":
			message := strings.ToLower(event.Message)
			switch {
			case strings.Contains(message, "release synced"):
				result.ReleaseSynced++
			case strings.Contains(message, "release unchanged"):
				result.ReleaseUnchanged++
			}
		case "publish":
			message := strings.ToLower(event.Message)
			switch {
			case strings.Contains(message, "planned"):
				result.PublishPlanned++
			case strings.Contains(message, "skipped"):
				result.PublishSkipped++
			case strings.Contains(message, "completed"):
				result.PublishSucceeded++
			case strings.ToLower(strings.TrimSpace(event.Level)) == "error":
				result.PublishFailed++
			}
		}
		if strings.Contains(strings.ToLower(event.Message), "rate limited") {
			result.RetryEvents++
		}
		if payload, err := loadEventPayload(event.PayloadRef); err == nil {
			if durationMS, ok := payloadInt64(payload, "duration_ms"); ok {
				if phaseName := phaseNameForEvent(event); phaseName != "" {
					result.PhaseTimingsMS[phaseName] = durationMS
				}
			}
			if highlight := progressHighlightForEvent(event, payload); highlight != "" {
				result.ProgressHighlights = append(result.ProgressHighlights, highlight)
				if len(result.ProgressHighlights) > 8 {
					result.ProgressHighlights = result.ProgressHighlights[len(result.ProgressHighlights)-8:]
				}
			}
		}
		if strings.ToLower(strings.TrimSpace(event.Level)) == "error" && len(result.RecentErrors) > 5 {
			result.RecentErrors = result.RecentErrors[len(result.RecentErrors)-5:]
		}
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

func loadEventPayload(path string) (map[string]any, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func payloadInt64(payload map[string]any, key string) (int64, bool) {
	if payload == nil {
		return 0, false
	}
	value, ok := payload[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	}
	return 0, false
}

func phaseNameForEvent(event domain.EventRecord) string {
	switch strings.TrimSpace(event.Message) {
	case "resolved Patreon session":
		return "provider_session_resolution"
	case "bootstrapped Patreon session":
		return "provider_session_bootstrap"
	case "Patreon collection scan complete":
		return "provider_collection_scan"
	case "Patreon feed pagination complete":
		return "provider_feed_pagination"
	case "Patreon post detail fetch complete":
		return "provider_post_detail_fetch"
	case "Patreon live release listing complete":
		return "provider_list_releases"
	case "downloaded Patreon attachment":
		return "provider_attachment_download"
	}
	return ""
}

func progressHighlightForEvent(event domain.EventRecord, payload map[string]any) string {
	switch strings.TrimSpace(event.Message) {
	case "Patreon feed pagination complete":
		return fmt.Sprintf(
			"feed pagination discovered=%d pages=%d stop=%s duration=%dms",
			intOrZero(payload["discovered_ids"]),
			intOrZero(payload["pages"]),
			stringOrEmpty(payload["stop_reason"]),
			intOrZero(payload["duration_ms"]),
		)
	case "Patreon post detail fetch complete":
		return fmt.Sprintf(
			"post detail fetch completed=%d total=%d failed=%d duration=%dms",
			intOrZero(payload["completed"]),
			intOrZero(payload["total_posts"]),
			intOrZero(payload["failed"]),
			intOrZero(payload["duration_ms"]),
		)
	case "Patreon rate limited request; backing off":
		return fmt.Sprintf(
			"rate limited attempt=%d delay=%dms",
			intOrZero(payload["attempt"]),
			intOrZero(payload["delay_ms"]),
		)
	case "Patreon request budget reduced", "Patreon request budget increased":
		budget, _ := payload["budget"].(map[string]any)
		return fmt.Sprintf(
			"%s limit=%d inflight=%d",
			strings.ToLower(strings.TrimSpace(event.Message)),
			intOrZero(budget["limit"]),
			intOrZero(budget["in_flight"]),
		)
	case "Patreon live release listing complete":
		return fmt.Sprintf(
			"live listing documents=%d duration=%dms",
			intOrZero(payload["documents"]),
			intOrZero(payload["duration_ms"]),
		)
	}
	return ""
}

func intOrZero(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	}
	return 0
}

func stringOrEmpty(value any) string {
	if typed, ok := value.(string); ok {
		return typed
	}
	return ""
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

func NotImplemented(feature string) error {
	return errors.New(feature + " is not implemented in the current MVP")
}

func FormatSyncResult(result domain.SyncResult) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("run_id: %s", result.RunID))
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

func FormatSourceDiscoverResult(result SourceDiscoverResult, showPosts bool) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf(
		"run_id=%s provider=%s auth_profile=%s auth_state=%s suggestions=%d membership=%s scan=%s\n",
		result.RunID,
		result.Provider,
		result.AuthProfileID,
		result.AuthState,
		len(result.Suggestions),
		firstNonEmpty(result.Options.MembershipFilter, "all"),
		discoveryScanLabel(result.Options),
	))
	for _, suggestion := range result.Suggestions {
		status := "new"
		if suggestion.AlreadyConfigured {
			status = "configured as " + suggestion.ExistingSourceID
		}
		builder.WriteString(fmt.Sprintf(
			"%s\t%s\t%s\t%s\n",
			suggestion.Source.ID,
			suggestion.CreatorName,
			firstNonEmpty(suggestion.MembershipKind, "unknown"),
			status,
		))
		if suggestion.SampledPosts > 0 {
			builder.WriteString(fmt.Sprintf("  scanned posts: %d\n", suggestion.SampledPosts))
		}
		if len(suggestion.SampleTitles) > 0 {
			builder.WriteString("  titles: " + strings.Join(suggestion.SampleTitles, " | ") + "\n")
		}
		if len(suggestion.SampleTags) > 0 {
			builder.WriteString("  tags: " + strings.Join(suggestion.SampleTags, ", ") + "\n")
		}
		if len(suggestion.SampleCollections) > 0 {
			builder.WriteString("  collections: " + strings.Join(suggestion.SampleCollections, ", ") + "\n")
		}
		if len(suggestion.SuggestedRules) > 0 {
			ruleLabels := make([]string, 0, len(suggestion.SuggestedRules))
			for _, rule := range suggestion.SuggestedRules {
				ruleLabels = append(ruleLabels, rule.MatchType+":"+rule.TrackKey)
			}
			builder.WriteString("  rules: " + strings.Join(ruleLabels, ", ") + "\n")
		}
		if len(suggestion.Preview.Groups) > 0 {
			builder.WriteString(fmt.Sprintf(
				"  preview: groups=%d materializable=%d fallback=%d\n",
				len(suggestion.Preview.Groups),
				suggestion.Preview.Materializable,
				suggestion.Preview.FallbackPosts,
			))
			for _, group := range suggestion.Preview.Groups {
				label := group.MatchType
				if strings.TrimSpace(group.MatchValue) != "" {
					label += ":" + group.MatchValue
				}
				builder.WriteString(fmt.Sprintf(
					"    - %s [%s] posts=%d materializable=%d\n",
					group.TrackKey,
					label+" "+string(group.ContentStrategy),
					group.Total,
					group.Materializable,
				))
				if len(group.SampleTitles) > 0 {
					builder.WriteString("      titles: " + strings.Join(group.SampleTitles, " | ") + "\n")
				}
			}
		}
		if showPosts && len(suggestion.Preview.Posts) > 0 {
			builder.WriteString("  posts:\n")
			for _, post := range suggestion.Preview.Posts {
				label := post.MatchType
				if strings.TrimSpace(post.MatchValue) != "" {
					label += ":" + post.MatchValue
				}
				builder.WriteString(fmt.Sprintf(
					"    - %s [%s %s materializable=%t] %s\n",
					post.TrackKey,
					label,
					post.ContentStrategy,
					post.Materializable,
					post.Title,
				))
			}
		}
	}
	if strings.TrimSpace(result.SnippetTOML) != "" {
		builder.WriteString("\nSuggested TOML snippet:\n")
		builder.WriteString(result.SnippetTOML)
	}
	return strings.TrimSpace(builder.String())
}

func discoveryScanLabel(options provider.DiscoverOptions) string {
	if options.FullHistory {
		return "full"
	}
	if options.SampleLimit <= 0 {
		return "default"
	}
	return fmt.Sprintf("recent-%d", options.SampleLimit)
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

func publishTargetIdentity(target config.PublisherConfig, candidate domain.PublishCandidate) (targetKind, targetRef, publishHashInput string, err error) {
	switch normalizedPublisherKind(target.Kind) {
	case "filesystem":
		targetPath := filepath.Join(target.Path, candidate.Source.ID, candidate.Track.TrackKey, candidate.Artifact.Filename)
		return "filesystem", targetPath, targetPath, nil
	case "exec":
		return "exec", publish.ExecTargetRef(target.Command), publish.ExecTargetSignature(target.Command), nil
	default:
		return "", "", "", fmt.Errorf("unsupported publisher kind %q", target.Kind)
	}
}

func normalizedPublisherKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "command", "exec":
		return "exec"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
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
