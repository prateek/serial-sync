package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/store"
)

type releaseIntake struct {
	s *Service
}

type intakePlan struct {
	DryRun                bool
	Action                string
	BlockReason           string
	Track                 domain.StoryTrack
	Release               domain.Release
	ArtifactPlan          domain.ArtifactPlan
	StoredArtifact        *domain.Artifact
	StoredState           artifact.StoredState
	ContentHash           string
	NormalizedJSON        []byte
	RawJSON               []byte
	NeedsMaterialize      bool
	Decision              domain.TrackDecision
	NormalizedSourceType  string
	NormalizedCreatorID   string
	NormalizedCreatorName string
}

type StoredArtifactRef struct {
	Release    *domain.Release
	Artifact   *domain.Artifact
	Track      domain.StoryTrack
	Assignment domain.ReleaseAssignment
}

func (s *Service) newReleaseIntake() *releaseIntake {
	return &releaseIntake{s: s}
}

// intakeHandleRelease is the whole intake for one release: load stored state,
// plan, and apply unless the plan says nothing changes. Sync and the rebuild
// run share it; the recorder reaches only apply.
func (s *Service) intakeHandleRelease(ctx context.Context, recorder *observe.Recorder, sourceCfg config.SourceConfig, doc provider.ReleaseDocument, decision domain.TrackDecision, dryRun, rebuild bool) (domain.SyncItemPlan, bool, bool, error) {
	stored, err := s.loadStoredForIntake(ctx, sourceCfg.ID, doc.Normalized.ProviderReleaseID)
	if err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	ri := s.newReleaseIntake()
	plan, err := ri.plan(ctx, sourceCfg, doc, decision, stored, dryRun, rebuild)
	if err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	return ri.apply(ctx, recorder, sourceCfg, plan)
}

func (ri *releaseIntake) plan(ctx context.Context, sourceCfg config.SourceConfig, doc provider.ReleaseDocument, decision domain.TrackDecision, stored *StoredArtifactRef, dryRun, rebuild bool) (intakePlan, error) {
	s := ri.s
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

	releaseID := "rel_" + uuid.NewString()
	if stored != nil && stored.Release != nil {
		releaseID = stored.Release.ID
	}
	// Capture attachments to get hashes (read-only on dry run)
	var captureErr error
	doc.Normalized, captureErr = s.captureAttachments(source.ID, doc.Normalized, dryRun)
	if captureErr != nil {
		return intakePlan{}, captureErr
	}
	if decision.SelectedContent != nil && decision.SelectedContent.Kind == "attachment" {
		attachment, ok := classify.SelectAttachment(doc.Normalized, decision)
		if !ok {
			return intakePlan{}, fmt.Errorf("selected attachment changed during capture")
		}
		selected := *decision.SelectedContent
		selected.SHA256 = attachment.SHA256
		decision.SelectedContent = &selected
	}

	contentHash := ri.computeContentHash(doc.Normalized)

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

	normalizedJSON, err := json.Marshal(doc.Normalized)
	if err != nil {
		return intakePlan{}, err
	}

	var track domain.StoryTrack
	var storedState artifact.StoredState
	var storedArtifact *domain.Artifact
	var artifactPlan domain.ArtifactPlan
	var artifactErr error
	pinnedPublication := false
	frozen := false

	if stored != nil && stored.Artifact != nil {
		storedArtifact = stored.Artifact
		storedState = artifact.Inspect(*storedArtifact)
		if !rebuild {
			if storedState.Blocked {
				// An unreadable sidecar is a plan entry with a reason,
				// not a failed run.
			} else if storedState.Legacy {
				// A legacy artifact is user data: sync keeps the stored
				// track and never re-plans or rewrites it.
				frozen = true
				track = stored.Track
				artifactPlan.Filename, artifactPlan.ArtifactKind = storedArtifact.Filename, storedArtifact.ArtifactKind
			} else {
				pinnedPublication = true
				decision.Publication = storedState.Publication
			}
		}
	}

	if !pinnedPublication && !frozen {
		decision.Publication, err = s.publicationMetadata(source.ID, doc.Normalized, decision)
		if err != nil {
			return intakePlan{}, err
		}
	}

	current := false
	noopEarly := false
	if !frozen {
		track, err = s.resolveTrack(ctx, source, decision)
		if err != nil {
			return intakePlan{}, err
		}
		if storedArtifact != nil && stored != nil && stored.Release != nil && stored.Release.ContentHash == contentHash {
			current = artifact.IsCurrent(*storedArtifact, release, track, decision)
			if current && storedState.Intact {
				// Current and intact on disk: never re-plan. Re-planning
				// a PDF would re-run Calibre for bytes that already match.
				noopEarly = true
			}
		}
		if !noopEarly && classify.CanMaterialize(doc.Normalized, decision) {
			artifactPlan, artifactErr = s.Files.Plan(ctx, source, track, release, doc.Normalized, decision, doc.RawJSON)
			if artifactErr != nil {
				return intakePlan{}, artifactErr
			}
		}
	}

	action := "create"
	needsMaterialize := artifactPlan.SHA256 != ""
	blockReason := ""

	switch {
	case noopEarly:
		action = "noop"
		needsMaterialize = false
		artifactPlan.Filename, artifactPlan.ArtifactKind = storedArtifact.Filename, storedArtifact.ArtifactKind
	case stored == nil || stored.Release == nil:
	case storedState.Blocked && !rebuild:
		action = "blocked"
		needsMaterialize = false
		blockReason = storedState.BlockReason
	case frozen:
		// Legacy output with changed content keeps its stored artifact
		// row and the stored track; only an explicit rebuild migrates it.
		if stored.Release.ContentHash == contentHash {
			action = "legacy_rebuild_available"
		} else {
			action = "pending_rebuild"
		}
		needsMaterialize = false
	case stored.Release.ContentHash == contentHash:
		action = "update"
		existingBytesValid := false
		if storedArtifact != nil {
			existingBytesValid = storedState.Intact
		}
		switch {
		case storedArtifact == nil && artifactPlan.SHA256 == "":
			action = "noop"
			needsMaterialize = false
		case storedArtifact != nil && existingBytesValid && storedArtifact.SHA256 == artifactPlan.SHA256 && storedArtifact.Filename == artifactPlan.Filename && (!rebuild || current):
			action = "noop"
			needsMaterialize = false
		}
		if action == "noop" && storedArtifact != nil {
			artifactPlan.Filename, artifactPlan.ArtifactKind = storedArtifact.Filename, storedArtifact.ArtifactKind
		}
	default:
		action = "update"
	}

	return intakePlan{
		DryRun:                dryRun,
		Action:                action,
		BlockReason:           blockReason,
		Track:                 track,
		Release:               release,
		ArtifactPlan:          artifactPlan,
		StoredArtifact:        storedArtifact,
		StoredState:           storedState,
		ContentHash:           contentHash,
		NormalizedJSON:        normalizedJSON,
		RawJSON:               doc.RawJSON,
		NeedsMaterialize:      needsMaterialize,
		Decision:              decision,
		NormalizedSourceType:  doc.Normalized.SourceType,
		NormalizedCreatorID:   doc.Normalized.CreatorID,
		NormalizedCreatorName: doc.Normalized.CreatorName,
	}, nil
}

func (ri *releaseIntake) apply(ctx context.Context, recorder *observe.Recorder, sourceCfg config.SourceConfig, plan intakePlan) (domain.SyncItemPlan, bool, bool, error) {
	s := ri.s

	source := domain.Source{
		ID:            sourceCfg.ID,
		Provider:      sourceCfg.Provider,
		SourceURL:     sourceCfg.URL,
		SourceType:    plan.NormalizedSourceType,
		CreatorID:     plan.NormalizedCreatorID,
		CreatorName:   plan.NormalizedCreatorName,
		AuthProfileID: sourceCfg.AuthProfile,
		Enabled:       sourceCfg.Enabled,
	}

	itemPlan := domain.SyncItemPlan{
		SourceID:          sourceCfg.ID,
		ProviderReleaseID: plan.Release.ProviderReleaseID,
		Title:             plan.Release.Title,
		TrackKey:          plan.Track.TrackKey,
		ReleaseRole:       plan.Decision.ReleaseRole,
		Strategy:          plan.Decision.ContentStrategy,
		OutputFormat:      plan.Decision.OutputFormat,
		ArtifactKind:      plan.ArtifactPlan.ArtifactKind,
		Filename:          plan.ArtifactPlan.Filename,
		Action:            plan.Action,
		BlockReason:       plan.BlockReason,
	}

	if plan.Action == "noop" || plan.Action == "blocked" || plan.Action == "legacy_rebuild_available" {
		_ = recorder.EventData(ctx, "info", "sync", "release unchanged", "release", plan.Release.ID, itemPlan)
		return itemPlan, false, false, nil
	}

	if plan.DryRun {
		_ = recorder.EventData(ctx, "info", "sync", "planned release sync", "release", plan.Release.ID, itemPlan)
		return itemPlan, true, plan.NeedsMaterialize, nil
	}

	payloadDir := filepath.Join(s.Config.Runtime.ArtifactRoot, sourceCfg.ID, plan.Track.TrackKey, plan.Release.ProviderReleaseID, plan.ContentHash)
	if err := os.MkdirAll(payloadDir, 0o755); err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	plan.Release.NormalizedPayloadRef = filepath.Join(payloadDir, "release.normalized.json")
	plan.Release.RawPayloadRef = filepath.Join(payloadDir, "release.raw.json")
	if err := os.WriteFile(plan.Release.NormalizedPayloadRef, prettyJSON(plan.NormalizedJSON), 0o644); err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}
	if err := os.WriteFile(plan.Release.RawPayloadRef, prettyJSON(plan.RawJSON), 0o644); err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}

	assignment := domain.ReleaseAssignment{
		ReleaseID:   plan.Release.ID,
		TrackID:     plan.Track.ID,
		RuleID:      plan.Decision.RuleID,
		ReleaseRole: plan.Decision.ReleaseRole,
		Confidence:  1.0,
	}

	art := domain.Artifact{}
	materialized := false
	if plan.NeedsMaterialize {
		var err error
		art, err = s.Files.Materialize(ctx, source, plan.Track, plan.Release, plan.ArtifactPlan)
		if err != nil {
			return domain.SyncItemPlan{}, false, false, err
		}
		materialized = true
	} else if plan.Action == "pending_rebuild" && plan.StoredArtifact != nil {
		// A legacy artifact with changed content keeps its stored row until
		// an explicit rebuild replaces it.
		art = *plan.StoredArtifact
	}
	if err := s.Repo.SaveSyncSnapshot(ctx, store.SyncSnapshot{
		Source:     source,
		Track:      plan.Track,
		Release:    plan.Release,
		Assignment: assignment,
		Artifact:   art,
	}); err != nil {
		return domain.SyncItemPlan{}, false, false, err
	}

	_ = recorder.EventData(ctx, "info", "sync", "release synced", "release", plan.Release.ID, map[string]any{
		"plan":         itemPlan,
		"materialized": materialized,
		"artifact_id":  art.ID,
	})

	return itemPlan, true, materialized, nil
}

func (ri *releaseIntake) computeContentHash(normalized domain.NormalizedRelease) string {
	hashable := hashableNormalizedRelease(normalized)
	contentHashJSON, _ := json.Marshal(hashable)
	sum := sha256.Sum256(contentHashJSON)
	return hex.EncodeToString(sum[:])
}

func (s *Service) loadStoredForIntake(ctx context.Context, sourceID, providerReleaseID string) (*StoredArtifactRef, error) {
	existingRelease, err := s.Repo.GetReleaseByProviderID(ctx, sourceID, providerReleaseID)
	if err != nil {
		return nil, err
	}
	if existingRelease == nil {
		return nil, nil
	}

	current, err := s.Repo.GetCanonicalArtifactByReleaseID(ctx, existingRelease.ID)
	if err != nil {
		return nil, err
	}

	var track domain.StoryTrack
	var assignment domain.ReleaseAssignment
	if current != nil {
		bundle, err := s.Repo.GetReleaseBundle(ctx, existingRelease.ID)
		if err != nil {
			return nil, err
		}
		if bundle != nil {
			track = bundle.Track
			assignment = bundle.Assignment
		}
	}

	return &StoredArtifactRef{
		Release:    existingRelease,
		Artifact:   current,
		Track:      track,
		Assignment: assignment,
	}, nil
}
