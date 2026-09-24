package app

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
	"github.com/prateek/serial-sync/internal/publish"
)

// deliveryOutcome is one target's delivery result: the items produced and the
// counts to fold into the run.
type deliveryOutcome struct {
	Items     []domain.PublishItemResult
	Artifacts []string
	Published int
	Skipped   int
	Failed    int
}

func (o *deliveryOutcome) fold(result *domain.PublishResult) {
	result.Items = append(result.Items, o.Items...)
	result.Artifacts = append(result.Artifacts, o.Artifacts...)
	result.Published += o.Published
	result.Skipped += o.Skipped
	result.Failed += o.Failed
}

// executeDelivery runs one target's delivery: the two-attempt loop with
// snapshot refresh, lifecycle hooks, dependency gating, delivery, retirement
// and pending-record bookkeeping. A dry run takes the same path through the
// planner and renders the plan instead of executing it. The returned error is
// a run-aborting failure; per-candidate and per-target failures land in the
// outcome as failed/blocked items.
func (s *Service) executeDelivery(ctx context.Context, recorder *observe.Recorder, scope deliveryScope, pt publish.Target, target config.PublisherConfig, selected []domain.PublishCandidate, records []domain.PublishRecordBundle, editions []domain.VolumeEdition, dryRun bool) (deliveryOutcome, error) {
	var outcome deliveryOutcome
	failed := func(candidateID, action, message string, ref ...string) {
		item := domain.PublishItemResult{ArtifactID: candidateID, TargetID: target.ID, TargetKind: pt.Kind(), Action: action, Message: message}
		if len(ref) > 0 {
			item.TargetRef = ref[0]
		}
		outcome.Items = append(outcome.Items, item)
		outcome.Failed++
	}
	targetStartFailed := outcome.Failed
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			// Refresh the snapshot on retry: the first attempt's own writes
			// (published and superseded records) must be visible so
			// retirement is not re-planned against an already-retired
			// record. The first attempt reuses the selection's snapshot.
			var err error
			if records, err = s.Repo.ListPublishRecords(ctx, "", target.ID); err != nil {
				return outcome, err
			}
			if editions, err = s.Repo.ListVolumeEditions(ctx); err != nil {
				return outcome, err
			}
		}
		snapshot := librarySnapshot{Records: records, Volumes: editions}
		var plan deliveryPlan
		var err error
		if dryRun {
			plan, err = s.planDelivery(ctx, scope, pt, target, selected, snapshot)
		} else {
			plan, err = s.deliveryPlan(ctx, scope, pt, target, selected, snapshot)
		}
		if err != nil {
			failed("", "blocked", err.Error())
			break
		}
		startFailed := outcome.Failed
		byID := map[string]domain.PublishCandidate{}
		itemByID := map[string]domain.PublishItemResult{}
		for _, candidate := range plan.Candidates {
			byID[candidate.Artifact.ID] = candidate
		}
		for _, item := range plan.Items {
			if item.Action != "retire" {
				itemByID[item.ArtifactID] = item
			}
		}
		// Plan-time blocks (destination and retirement ownership conflicts)
		// gate the delivery on every attempt, including the fresh plan
		// after a resumed one: each fails the run so the pending plan is
		// retained for the fix-and-retry loop.
		for _, item := range plan.Items {
			if item.Action != "blocked" {
				continue
			}
			outcome.Items = append(outcome.Items, item)
			if !dryRun {
				outcome.Failed++
			}
		}
		delivered := map[string]bool{}
		if !dryRun {
			ordered := make([]domain.PublishCandidate, 0, len(plan.Ordered))
			for _, id := range plan.Ordered {
				ordered = append(ordered, byID[id])
			}
			event := publish.LifecycleEvent{Action: "prepare", RunID: recorder.RunID(), TargetID: target.ID, DeliveryID: plan.ID, Maintenance: plan.Maintenance, Candidates: ordered}
			if plan.Maintenance && len(target.LifecycleCommand) > 0 {
				previous := make([]domain.PublishRecordBundle, 0, len(snapshot.Records))
				for _, record := range snapshot.Records {
					if record.Record.Status == domain.PublishStatusPublished {
						previous = append(previous, record)
					}
				}
				event.Previous = &previous
			}
			if err := publish.RunLifecycle(ctx, target.LifecycleCommand, event); err != nil {
				failed("", "failed", err.Error())
				break
			}
		}
		for _, id := range plan.Ordered {
			candidate := byID[id]
			ready := true
			if !dryRun {
				for _, dependency := range plan.Dependencies[id] {
					if !delivered[dependency] {
						ready = false
					}
				}
			}
			if !ready {
				failed(candidate.Artifact.ID, "blocked", "waiting for other replacement files before overwriting the old volume")
				continue
			}
			if itemByID[id].Action == "blocked" {
				// Already reported once for this plan; never delivered.
				continue
			}
			identity, identityErr := pt.Identity(candidate, snapshot.Records)
			if identityErr != nil && !dryRun {
				failed(candidate.Artifact.ID, "failed", identityErr.Error(), identity.Ref)
				continue
			}
			if itemByID[id].Action == "unchanged" {
				// The plan already verified this destination against the
				// candidate's published record; skip instead of re-deriving
				// the same decision at execute time.
				delivered[candidate.Artifact.ID] = true
				outcome.Skipped++
				outcome.Items = append(outcome.Items, domain.PublishItemResult{
					ArtifactID: candidate.Artifact.ID,
					TargetID:   target.ID,
					TargetKind: pt.Kind(),
					TargetRef:  identity.Ref,
					Action:     "skipped",
				})
				_ = recorder.EventDataK(ctx, "info", observe.KindPublishSkipped, "publish skipped: identical artifact already published", "artifact", candidate.Artifact.ID, map[string]any{
					"artifact_id":  candidate.Artifact.ID,
					"target_id":    target.ID,
					"target_kind":  pt.Kind(),
					"target_ref":   identity.Ref,
					"publish_hash": identity.PublishHash,
					"action":       "skipped",
				})
				continue
			}
			if itemByID[id].Action == "held" {
				outcome.Skipped++
				item := itemByID[id]
				item.TargetRef = identity.Ref
				outcome.Items = append(outcome.Items, item)
				_ = recorder.EventDataK(ctx, "info", observe.KindPublishHeld, "publish held: revised release already handed off", "artifact", candidate.Artifact.ID, item)
				continue
			}
			if dryRun {
				delivered[candidate.Artifact.ID] = true
				outcome.Published++
				outcome.Artifacts = append(outcome.Artifacts, candidate.Artifact.ID)
				item := domain.PublishItemResult{
					ArtifactID: candidate.Artifact.ID,
					TargetID:   target.ID,
					TargetKind: pt.Kind(),
					TargetRef:  identity.Ref,
					Action:     "planned",
				}
				outcome.Items = append(outcome.Items, item)
				_ = recorder.EventDataK(ctx, "info", observe.KindPublishPlanned, "planned "+pt.Kind()+" publish", "artifact", candidate.Artifact.ID, item)
				continue
			}
			record, pubErr := s.deliverCandidate(ctx, recorder.RunID(), pt, target, plan, candidate, identity, snapshot.Records)
			if pubErr != nil {
				outcome.Items = append(outcome.Items, domain.PublishItemResult{
					ArtifactID: candidate.Artifact.ID,
					TargetID:   target.ID,
					TargetKind: pt.Kind(),
					TargetRef:  identity.Ref,
					Action:     "failed",
					Message:    pubErr.Error(),
				})
				outcome.Failed++
				if !pt.Capabilities().PendingRecord {
					_ = s.Repo.UpsertPublishRecord(ctx, domain.PublishRecord{
						ID:          "pub_" + uuid.NewString(),
						ArtifactID:  candidate.Artifact.ID,
						TargetID:    target.ID,
						TargetKind:  pt.Kind(),
						TargetRef:   identity.Ref,
						PublishHash: identity.PublishHash,
						PublishedAt: time.Now().UTC(),
						Status:      domain.PublishStatusFailed,
						Message:     pubErr.Error(),
					})
				}
				_ = recorder.EventDataK(ctx, "error", observe.KindPublishFailed, pubErr.Error(), "artifact", candidate.Artifact.ID, outcome.Items[len(outcome.Items)-1])
				continue
			}
			if err := s.Repo.UpsertPublishRecord(ctx, record); err != nil {
				return outcome, err
			}
			delivered[candidate.Artifact.ID] = true
			outcome.Published++
			outcome.Artifacts = append(outcome.Artifacts, candidate.Artifact.ID)
			outcome.Items = append(outcome.Items, domain.PublishItemResult{
				ArtifactID: candidate.Artifact.ID,
				TargetID:   target.ID,
				TargetKind: record.TargetKind,
				TargetRef:  record.TargetRef,
				Action:     "published",
				Message:    record.Message,
			})
			_ = recorder.EventDataK(ctx, "info", observe.KindPublishCompleted, record.TargetKind+" publish completed", "artifact", candidate.Artifact.ID, record)
		}
		if dryRun {
			break
		}
		if outcome.Failed > startFailed {
			break
		}
		if cleanupErr := s.applyRetirements(ctx, pt, target, plan, snapshot); cleanupErr != nil {
			failed("", "failed", cleanupErr.Error())
		}
		if outcome.Failed > startFailed {
			break
		}
		if err := s.Repo.CompletePendingPublish(ctx, plan.ID); err != nil {
			return outcome, err
		}
		if sameDelivery(plan.Candidates, selected) {
			break
		}
	}
	if !dryRun {
		if err := publish.RunLifecycle(ctx, target.LifecycleCommand, publish.LifecycleEvent{Action: "complete", RunID: recorder.RunID(), TargetID: target.ID, Maintenance: scope.Rebuild, Succeeded: outcome.Failed == targetStartFailed}); err != nil {
			failed("", "notification_failed", err.Error())
		}
	}
	return outcome, nil
}

// deliverCandidate prepares one candidate's delivery: the final destination
// check against the current records, the provisional publishing ledger entry
// when the target keeps one, and the delivery itself.
func (s *Service) deliverCandidate(ctx context.Context, runID string, pt publish.Target, target config.PublisherConfig, plan deliveryPlan, candidate domain.PublishCandidate, identity publish.DeliveryIdentity, records []domain.PublishRecordBundle) (domain.PublishRecord, error) {
	// The just-written publishing record (or a retry's) owns the same bytes
	// this candidate will deliver, so count them as owned from the start.
	owned := append(ownedHashesForPath(records, identity.Ref), candidate.Artifact.SHA256)
	if _, err := pt.CheckDestination(identity.Ref, owned); err != nil {
		return domain.PublishRecord{}, err
	}
	pending := pt.PublishingRecord(candidate, identity)
	if pending != nil {
		if err := s.Repo.UpsertPublishRecord(ctx, *pending); err != nil {
			return domain.PublishRecord{}, err
		}
	}
	return pt.Deliver(ctx, runID, plan.EventScope, candidate, identity, owned)
}
