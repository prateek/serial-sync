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
)

type deliveryPlan struct {
	ID         string                    `json:"id"`
	EventScope string                    `json:"event_scope,omitempty"`
	Target     config.PublisherConfig    `json:"target"`
	Candidates []domain.PublishCandidate `json:"candidates"`
}

func (s *Service) deliveryPlan(ctx context.Context, target config.PublisherConfig, candidates []domain.PublishCandidate, sourceFilter, seriesFilter string, rebuild bool) (deliveryPlan, error) {
	pending, err := s.pendingDelivery(ctx, target, sourceFilter, seriesFilter, rebuild)
	if err != nil {
		return deliveryPlan{}, err
	}
	if pending != nil {
		_, _, err := s.orderReplacements(ctx, target, pending.Candidates)
		if !errors.Is(err, errCyclicReplacement) {
			return *pending, err
		}
		// Older versions saved cyclic plans before rejecting them without delivery.
		if err := s.Repo.CompletePendingPublish(ctx, pending.ID); err != nil {
			return deliveryPlan{}, err
		}
	}
	if _, _, err := s.orderReplacements(ctx, target, candidates); err != nil {
		return deliveryPlan{}, err
	}
	plan := deliveryPlan{ID: "delivery_" + uuid.NewString(), Target: target, Candidates: candidates}
	plan.EventScope = plan.ID
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

func (s *Service) pendingDelivery(ctx context.Context, target config.PublisherConfig, sourceFilter, seriesFilter string, rebuild bool) (*deliveryPlan, error) {
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
			if !s.sourceInScope(candidate.Source.ID, sourceFilter, rebuild) || seriesFilter != "" && candidate.Track.TrackKey != seriesFilter {
				return nil, fmt.Errorf("target %s pending delivery requires excluded sources or series; retry its original scope first", target.ID)
			}
		}
		return &plan, nil
	}
	return nil, nil
}

func targetSignature(target config.PublisherConfig) string {
	data, _ := json.Marshal([]any{target.Kind, target.Path, target.Command, target.ProtocolVersion})
	return hashBytes(data)
}

func sameDelivery(left, right []domain.PublishCandidate) bool {
	fingerprint := func(candidates []domain.PublishCandidate) string {
		items := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			data, _ := json.Marshal([]string{candidate.Artifact.ID, candidate.Artifact.SHA256, candidate.Artifact.Filename, candidate.Source.ID, candidate.Track.TrackKey})
			items = append(items, string(data))
		}
		sort.Strings(items)
		data, _ := json.Marshal(items)
		return string(data)
	}
	return fingerprint(left) == fingerprint(right)
}
