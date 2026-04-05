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
	recorder, err := observe.Start(ctx, s.Repo, command, sourceFilter, dryRun)
	if err != nil {
		return domain.SyncResult{}, err
	}
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
			docs, authState, listErr := client.ListReleases(ctx, auth, sourceCfg)
			_ = recorder.Event(ctx, "info", "provider", "auth state "+string(authState), "source", sourceCfg.ID)
			if listErr != nil {
				err = listErr
				_ = recorder.Event(ctx, "error", "provider", listErr.Error(), "source", sourceCfg.ID)
				return result, err
			}
			for _, doc := range docs {
				result.Discovered++
				decision := classify.Decide(sourceCfg.ID, doc.Normalized, s.Config.RulesForSource(sourceCfg.ID))
				action, changed, materialized, handleErr := s.handleRelease(ctx, recorder, sourceCfg, doc, decision, dryRun)
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
	recorder, err := observe.Start(ctx, s.Repo, command, scope, false)
	if err != nil {
		return AuthBootstrapResult{}, err
	}
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

func (s *Service) Publish(ctx context.Context, sourceFilter, targetFilter string, dryRun bool, command string) (domain.PublishResult, error) {
	recorder, err := observe.Start(ctx, s.Repo, command, sourceFilter, dryRun)
	if err != nil {
		return domain.PublishResult{}, err
	}
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
				_ = recorder.Event(ctx, "info", "publish", "planned "+targetKind+" publish", "artifact", candidate.Artifact.ID)
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
				_ = recorder.Event(ctx, "error", "publish", pubErr.Error(), "artifact", candidate.Artifact.ID)
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
			_ = recorder.Event(ctx, "info", "publish", record.TargetKind+" publish completed", "artifact", candidate.Artifact.ID)
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
		_ = recorder.Event(ctx, "info", "sync", "release unchanged", "release", release.ID)
		return itemPlan, false, false, nil
	}
	if dryRun {
		_ = recorder.Event(ctx, "info", "sync", "planned release sync", "release", release.ID)
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
	_ = recorder.Event(ctx, "info", "sync", "release synced", "release", release.ID)
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
	return run, nil
}

func (s *Service) SupportBundle(ctx context.Context, runID string) (string, error) {
	bundle, err := s.InspectRun(ctx, runID)
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
	configJSON, err := json.MarshalIndent(s.Config, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), configJSON, 0o644); err != nil {
		return "", err
	}
	readme := "serial-sync support bundle\nrun_id=" + runID + "\nconfig_path=" + s.ConfigPath + "\n"
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte(readme), 0o644); err != nil {
		return "", err
	}
	return dir, nil
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

func FormatRunOnceResult(result RunOnceResult) string {
	return strings.Join([]string{
		"sync:",
		indentBlock(FormatSyncResult(result.Sync), "  "),
		"",
		"publish:",
		indentBlock(FormatPublishResult(result.Publish), "  "),
	}, "\n")
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
