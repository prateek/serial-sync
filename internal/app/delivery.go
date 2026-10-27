package app

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/classify"
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
			plan, err = planDelivery(ctx, scope, pt, target, selected, snapshot)
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
		if plan.Persisted {
			if err := s.Repo.CompletePendingPublish(ctx, plan.ID); err != nil {
				return outcome, err
			}

			if err := s.deliveryFiles().compact(plan, recorder.RunID()); err != nil {
				if logErr := recorder.EventDataK(ctx, "warn", observe.KindDeliveryCompactionFailed, "completed delivery compaction incomplete; saved plan or receipt available for cleanup", "delivery", plan.ID, map[string]any{"error": err.Error(), "target_id": target.ID}); logErr != nil {
					fmt.Fprintf(os.Stderr, "completed delivery %s compaction failed: %v; recording warning failed: %v\n", plan.ID, err, logErr)
				}
			}
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

func (s *Service) deliverLibrary(ctx context.Context, recorder *observe.Recorder, scope deliveryScope, targetFilter string, dryRun bool, blockedSeries map[string]bool, decisions *RunDecisions, result *domain.PublishResult) error {
	targets := selectPublishers(s.Config.Publishers, targetFilter)
	if len(targets) == 0 {
		return fmt.Errorf("no enabled publishers match %q", targetFilter)
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
		return err
	}
	existing, err := s.Repo.ListVolumeEditions(ctx)
	if err != nil {
		return err
	}
	prepared, err := s.catalogReader().prepareDelivery(ctx, s.Config, scope, targets, candidates, blockedSeries, existing, existing)
	if err != nil {
		return err
	}
	for _, target := range prepared.targets {
		if target.err != nil {
			result.Failed++
			result.Items = append(result.Items, domain.PublishItemResult{TargetID: target.config.ID, Action: "blocked", Message: target.err.Error()})
			continue
		}
		outcome, execErr := s.executeDelivery(ctx, recorder, scope, target.publisher, target.config, prepared.selected, target.snapshot.Records, target.snapshot.Volumes, dryRun)
		if execErr != nil {
			return execErr
		}
		outcome.fold(result)
	}
	return nil
}

func (s *catalogReader) deliverySelection(ctx context.Context, cfg *config.Config, scope deliveryScope, targets []config.PublisherConfig, chapters []domain.PublishCandidate, blockedSeries map[string]bool, existing []domain.VolumeEdition) (selected []domain.PublishCandidate, recordsByTarget map[string][]domain.PublishRecordBundle, err error) {
	candidates, err := s.volumeCandidates(ctx, chapters, existing)
	if err != nil {
		return nil, nil, err
	}
	if err := resolveCollisionNames(candidates); err != nil {
		return nil, nil, err
	}
	selected = make([]domain.PublishCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if blockedSeries[candidate.Track.TrackKey] {
			continue
		}
		if !sourceInScope(candidate.Source.ID, scope.SourceID, scope.Rebuild, cfg) || scope.SeriesID != "" && candidate.Track.TrackKey != scope.SeriesID {
			continue
		}
		selected = append(selected, candidate)
	}
	recordsByTarget = map[string][]domain.PublishRecordBundle{}
	for _, target := range targets {
		records, err := s.catalog.ListPublishRecords(ctx, "", target.ID)
		if err != nil {
			return nil, nil, err
		}
		recordsByTarget[target.ID] = records
	}
	if err := validateLegacyReplacements(ctx, targets, selected, recordsByTarget); err != nil {
		return nil, nil, err
	}
	if !scope.Rebuild {
		if err := s.validateFrozenNames(ctx, cfg, targets, selected, recordsByTarget); err != nil {
			return nil, nil, err
		}
	}
	return selected, recordsByTarget, nil
}

func (s *catalogReader) previewDelivery(ctx context.Context, cfg *config.Config, options RebuildOptions, inputs deliveryInputs, result *RebuildResult) error {
	request := volumeEvaluation{sourceFilter: options.SourceID, seriesFilter: options.SeriesID, rebuild: true, blockedSeries: inputs.blocked, candidates: inputs.chapters, existing: inputs.editions, inputs: inputs.normalized, histories: inputs.histories, series: cfg.Series, config: cfg}
	plans, volumes, err := s.evaluateVolumes(ctx, request, nil)
	result.Publish.Volumes = plans
	if err != nil {
		result.Blocked = append(result.Blocked, err.Error())
	}
	candidates, blocked, existing := request.candidates, request.blockedSeries, request.existing
	targets := selectPublishers(cfg.Publishers, options.TargetID)
	scope := deliveryScope{SourceID: options.SourceID, SeriesID: options.SeriesID, Rebuild: true}
	prepared, err := s.prepareDelivery(ctx, cfg, scope, targets, candidates, blocked, volumes, existing)
	if err != nil {
		return err
	}
	for _, target := range prepared.targets {
		if target.err != nil {
			result.Blocked = append(result.Blocked, target.err.Error())
			continue
		}
		pending, err := s.pendingDelivery(ctx, cfg, scope, target.config)
		if err != nil {
			result.Blocked = append(result.Blocked, err.Error())
			continue
		}
		if pending != nil {
			result.Pending = append(result.Pending, *pending)
		}
		plan, planErr := planDelivery(ctx, scope, target.publisher, target.config, prepared.selected, target.snapshot)
		if planErr != nil {
			result.Blocked = append(result.Blocked, planErr.Error())
			continue
		}
		result.Publish.Items = append(result.Publish.Items, plan.Items...)
		result.Blocked = append(result.Blocked, plan.Blocked...)

	}
	if notice := readingPositionNotice(result.Publish.Items); notice != "" {
		result.Notices = append(result.Notices, notice)
	}
	return nil
}

type deliveryInputs struct {
	chapters   []domain.PublishCandidate
	normalized map[string]domain.NormalizedRelease
	histories  map[string]map[string]classify.ExplainedDecision
	blocked    map[string]bool
	editions   []domain.VolumeEdition
}

type preparedDelivery struct {
	selected []domain.PublishCandidate
	targets  []preparedDeliveryTarget
}

type preparedDeliveryTarget struct {
	config    config.PublisherConfig
	publisher publish.Target
	snapshot  librarySnapshot
	err       error
}

func (s *catalogReader) prepareDelivery(ctx context.Context, cfg *config.Config, scope deliveryScope, targets []config.PublisherConfig, chapters []domain.PublishCandidate, blocked map[string]bool, selectedVolumes, priorVolumes []domain.VolumeEdition) (preparedDelivery, error) {
	selected, records, err := s.deliverySelection(ctx, cfg, scope, targets, chapters, blocked, selectedVolumes)
	if err != nil {
		return preparedDelivery{}, err
	}
	prepared := preparedDelivery{selected: selected}
	for _, target := range targets {
		publisher, err := publish.TargetFor(target)
		prepared.targets = append(prepared.targets, preparedDeliveryTarget{config: target, publisher: publisher, snapshot: librarySnapshot{Records: records[target.ID], Volumes: priorVolumes}, err: err})
	}
	return prepared, nil
}

func readingPositionNotice(items []domain.PublishItemResult) string {
	conditional := false
	for _, item := range items {
		if item.Action != "replace" && item.Action != "retire" {
			continue
		}
		if !item.Conditional {
			return "Replacing delivered books may reset saved reading positions; check a small reader sample before a larger migration."
		}
		conditional = true
	}
	if conditional {
		return "Volume delivery is conditional on regenerated member bytes. If replacement occurs, saved reading positions may reset; check a small reader sample before a larger migration."
	}
	return ""
}
