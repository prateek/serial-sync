package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/publish"
)

type deliveryScope struct {
	SourceID string
	SeriesID string
	Rebuild  bool
}

type librarySnapshot struct {
	Records []domain.PublishRecordBundle
	Volumes []domain.VolumeEdition
}

type planRetirement struct {
	RecordID    string
	ReleaseID   string
	ArtifactID  string
	Filename    string
	SHA256      string
	TargetRef   string
	PublishHash string
	Related     []string
}

type deliveryPlan struct {
	// Maintenance marks a rebuild-scoped delivery so lifecycle hooks can
	// distinguish it from steady-state publication.
	Maintenance bool                      `json:"maintenance,omitempty"`
	ID          string                    `json:"id"`
	EventScope  string                    `json:"event_scope,omitempty"`
	Target      config.PublisherConfig    `json:"target"`
	Candidates  []domain.PublishCandidate `json:"candidates"`
	// The saved plan carries only what resumption reads; a resumed plan is
	// re-planned from its candidates against the current snapshot.
	Items        []domain.PublishItemResult `json:"-"`
	Ordered      []string                   `json:"-"`
	Dependencies map[string][]string        `json:"-"`
	// Identities carries each candidate's computed destination identity from
	// the plan to the executor, so the publish hash formula runs once per
	// candidate. Blocked candidates are absent from it.
	Identities  map[string]publish.DeliveryIdentity `json:"-"`
	Retirements []planRetirement                    `json:"-"`
	Blocked     []string                            `json:"-"`
}

// planDelivery is the one delivery planner: it turns a target's library
// snapshot and the desired candidates into the full per-target action list,
// ordering and retirement set. The rebuild preview prints plan.Items and the
// publish executor applies them, so dry runs and real runs share one
// implementation of the add, replace, repair, unchanged and retire decisions.
func (s *Service) planDelivery(ctx context.Context, scope deliveryScope, pt publish.Target, target config.PublisherConfig, candidates []domain.PublishCandidate, snapshot librarySnapshot) (deliveryPlan, error) {
	plan := deliveryPlan{ID: "delivery_" + uuid.NewString(), Target: target, Candidates: candidates, Maintenance: scope.Rebuild}
	plan.EventScope = plan.ID
	plan.Identities = map[string]publish.DeliveryIdentity{}
	ordered, dependencies, orderErr := pt.Reorder(candidates, snapshot.Records, snapshot.Volumes)
	if orderErr != nil {
		return plan, orderErr
	}
	plan.Ordered = make([]string, 0, len(ordered))
	for _, candidate := range ordered {
		plan.Ordered = append(plan.Ordered, candidate.Artifact.ID)
	}
	plan.Dependencies = dependencies
	desired, covered := map[string]bool{}, map[string]bool{}
	for _, candidate := range candidates {
		identity, identityErr := pt.Identity(candidate, snapshot.Records)
		desired[identity.DesiredKey] = true
		if candidate.Volume != nil {
			for _, member := range candidate.Volume.Members {
				covered[member.ReleaseID] = true
			}
		} else {
			covered[candidate.Release.ID] = true
		}
		if identityErr == nil {
			plan.Identities[candidate.Artifact.ID] = identity
		}
		action := "add"
		message := ""
		if identityErr != nil {
			action = "blocked"
			message = fmt.Sprintf("target %s: %v", target.ID, identityErr)
			plan.Blocked = append(plan.Blocked, message)
		}
		for _, record := range snapshot.Records {
			if identityErr != nil || record.Record.Status != domain.PublishStatusPublished {
				continue
			}
			if pt.MatchesDestination(record, candidate) {
				action = "replace"
			}
			if !candidate.Planned && candidate.Artifact.ID == record.Artifact.ID && candidate.Artifact.SHA256 != "" && record.Record.PublishHash == identity.PublishHash {
				if identity.Intact {
					action = "unchanged"
				} else {
					action = "repair"
				}
				break
			}
		}
		plan.Items = append(plan.Items, domain.PublishItemResult{ArtifactID: candidate.Artifact.ID, TargetID: target.ID, TargetKind: pt.Kind(), TargetRef: identity.Ref, Action: action, Message: message})
	}
	if !pt.Capabilities().Retires {
		return plan, nil
	}
	for _, old := range snapshot.Records {
		if old.Record.Status != domain.PublishStatusPublished || desired[pt.DesiredKeyForRecord(old)] {
			continue
		}
		complete := covered[old.Release.ID]
		for _, volume := range snapshot.Volumes {
			if volume.Artifact.ID == old.Artifact.ID {
				complete = true
				for _, member := range volume.Members {
					if !covered[member.ReleaseID] {
						complete = false
					}
				}
			}
		}
		if !complete {
			continue
		}
		required := map[string]bool{old.Release.ID: true}
		for _, volume := range snapshot.Volumes {
			if volume.Artifact.ID != old.Artifact.ID {
				continue
			}
			for _, member := range volume.Members {
				required[member.ReleaseID] = true
			}
		}
		var related []string
		for _, replacement := range candidates {
			covers := required[replacement.Release.ID] && replacement.Release.ID != ""
			if replacement.Volume != nil {
				for _, member := range replacement.Volume.Members {
					if required[member.ReleaseID] {
						covers = true
					}
				}
			}
			if covers {
				related = append(related, replacement.Artifact.ID)
			}
		}
		sort.Strings(related)
		if _, checkErr := pt.CheckDestination(old.Record.TargetRef, retirementOwnedHashes(snapshot.Records, old)); checkErr != nil {
			item := domain.PublishItemResult{ArtifactID: old.Artifact.ID, TargetID: target.ID, TargetKind: old.Record.TargetKind, TargetRef: old.Record.TargetRef, Action: "blocked", Message: fmt.Sprintf("target %s retirement ownership conflict: %s", target.ID, old.Record.TargetRef)}
			plan.Items = append(plan.Items, item)
			plan.Blocked = append(plan.Blocked, item.Message)
			continue
		}
		plan.Items = append(plan.Items, domain.PublishItemResult{ArtifactID: old.Artifact.ID, TargetID: target.ID, TargetKind: old.Record.TargetKind, TargetRef: old.Record.TargetRef, Action: "retire"})
		plan.Retirements = append(plan.Retirements, planRetirement{
			RecordID:    old.Record.ID,
			ReleaseID:   old.Release.ID,
			ArtifactID:  old.Artifact.ID,
			Filename:    old.Record.Filename,
			SHA256:      old.Artifact.SHA256,
			TargetRef:   old.Record.TargetRef,
			PublishHash: old.Record.PublishHash,
			Related:     related,
		})
	}
	return plan, nil
}

func (s *Service) deliveryPlan(ctx context.Context, scope deliveryScope, pt publish.Target, target config.PublisherConfig, candidates []domain.PublishCandidate, snapshot librarySnapshot) (deliveryPlan, error) {
	pending, err := s.pendingDelivery(ctx, s.Config, scope, target)
	if err != nil {
		return deliveryPlan{}, err
	}
	if pending != nil {
		// A resumed plan keeps its saved ID and event scope but is re-planned
		// from its saved candidates against the current snapshot, exactly as a
		// fresh plan would. The persisted JSON omits the dependency map (and
		// plans saved before ordering existed carry no delivery order at all),
		// so resuming the deserialized plan straight would deliver without the
		// replacement gate.
		rebuilt, err := s.planDelivery(ctx, scope, pt, target, pending.Candidates, snapshot)
		if publish.IsCyclicReplacement(err) {
			// Older versions saved cyclic plans before rejecting them without delivery.
			if err := s.Repo.CompletePendingPublish(ctx, pending.ID); err != nil {
				return deliveryPlan{}, err
			}
		} else if err != nil {
			return rebuilt, err
		} else {
			rebuilt.ID = pending.ID
			rebuilt.EventScope = pending.EventScope
			rebuilt.Maintenance = pending.Maintenance
			return rebuilt, nil
		}
	}
	plan, err := s.planDelivery(ctx, scope, pt, target, candidates, snapshot)
	if err != nil {
		return plan, err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return plan, err
	}
	dir := filepath.Join(s.Config.Runtime.ArtifactRoot, "deliveries")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return plan, err
	}
	path := filepath.Join(dir, plan.ID+".json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return plan, err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return plan, err
	}
	return plan, s.Repo.SavePendingPublish(ctx, domain.PendingPublish{ID: plan.ID, TargetID: target.ID, PayloadRef: path})
}

// applyRetirements executes the plan's retirement instructions once its
// replacement deliveries all succeeded. Retirement is idempotent: a crash
// mid-cleanup leaves the pending plan, and the retry re-applies the same
// instructions against the same records.
func (s *Service) applyRetirements(ctx context.Context, pt publish.Target, target config.PublisherConfig, plan deliveryPlan, snapshot librarySnapshot) error {
	var failures []error
	for _, retirement := range plan.Retirements {
		var old *domain.PublishRecordBundle
		for _, record := range snapshot.Records {
			if record.Record.ID == retirement.RecordID {
				old = &record
				break
			}
		}
		if old == nil {
			continue
		}
		var related []domain.PublishCandidate
		for _, candidate := range plan.Candidates {
			for _, id := range retirement.Related {
				if candidate.Artifact.ID == id {
					related = append(related, candidate)
					break
				}
			}
		}
		owned := retirementOwnedHashes(snapshot.Records, *old)
		if err := pt.Retire(ctx, *old, plan.Candidates, related, owned, plan.EventScope); err != nil {
			failures = append(failures, err)
			continue
		}
		old.Record.Status = domain.PublishStatusSuperseded
		if err := s.Repo.UpsertPublishRecord(ctx, old.Record); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *Service) pendingDelivery(ctx context.Context, cfg *config.Config, scope deliveryScope, target config.PublisherConfig) (*deliveryPlan, error) {
	pending, err := s.Repo.GetPendingPublish(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	if pending != nil {
		var plan deliveryPlan
		data, err := os.ReadFile(pending.PayloadRef)
		if err == nil {
			err = json.Unmarshal(data, &plan)
		}
		if err != nil {
			return nil, fmt.Errorf("target %s pending delivery %s: %w", target.ID, pending.PayloadRef, err)
		}
		if plan.ID != pending.ID || targetSignature(plan.Target) != targetSignature(target) {
			return nil, fmt.Errorf("target %s has a pending delivery with different settings; restore its settings and retry first", target.ID)
		}
		for _, candidate := range plan.Candidates {
			if !s.sourceInScope(candidate.Source.ID, scope.SourceID, scope.Rebuild, cfg) || scope.SeriesID != "" && candidate.Track.TrackKey != scope.SeriesID {
				return nil, fmt.Errorf("target %s pending delivery requires excluded sources or series; retry its original scope first", target.ID)
			}
		}
		return &plan, nil
	}
	return nil, nil
}

func targetSignature(target config.PublisherConfig) string {
	data, _ := json.Marshal([]any{target.Kind, target.Path, target.Command, target.ProtocolVersion, target.LifecycleCommand})
	return hashBytes(data)
}

func sameDelivery(left, right []domain.PublishCandidate) bool {
	fingerprint := func(candidates []domain.PublishCandidate) string {
		items := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			data, _ := json.Marshal([]any{candidate.Artifact.ID, candidate.Artifact.SHA256, candidate.Artifact.Filename, candidate.Source.ID, candidate.Track.TrackKey, candidate.Planned})
			items = append(items, string(data))
		}
		sort.Strings(items)
		data, _ := json.Marshal(items)
		return string(data)
	}
	return fingerprint(left) == fingerprint(right)
}
