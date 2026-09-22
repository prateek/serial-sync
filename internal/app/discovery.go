package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func FormatCandidates(candidates []domain.DiscoveryCandidate) string {
	var changed, unresolved []domain.DiscoveryCandidate
	for _, candidate := range candidates {
		if candidate.Status != "resolved" {
			if candidate.Changed {
				changed = append(changed, candidate)
			} else {
				unresolved = append(unresolved, candidate)
			}
		}
	}
	lines := []string{fmt.Sprintf("candidates: %d new or changed, %d unresolved; scope is captured posts only", len(changed), len(changed)+len(unresolved))}
	shown := append([]domain.DiscoveryCandidate(nil), changed...)
	limit := len(unresolved)
	if limit > 5 {
		limit = 5
	}
	shown = append(shown, unresolved[:limit]...)
	for _, candidate := range shown {
		var evidence []string
		for _, item := range candidate.Evidence {
			label := item.Kind
			if item.Value != "" {
				label += "=" + item.Value
			}
			evidence = append(evidence, label)
		}
		lines = append(lines, fmt.Sprintf("  %s [%s, %s] %s: %d post(s); %s", candidate.ID, candidate.Status, candidate.Kind, candidate.Source, len(candidate.MemberReleaseIDs), strings.Join(evidence, "; ")))
	}
	return strings.Join(lines, "\n")
}

// observedBatch carries the decider output of one source so the candidate
// pass can recompute with the stored state for the run.
type observedBatch struct {
	Source    string
	Documents []provider.ReleaseDocument
	At        time.Time
	Decisions map[string]classify.ExplainedDecision
}

func (s *Service) reportObservedCandidates(ctx context.Context, batches []observedBatch, dryRun bool, result *domain.SyncResult) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	for i := range batches {
		batch := &batches[i]
		// Candidates are computed after releases are stored so the captured
		// attachment hashes sit in the evidence fingerprints, making the
		// candidate report stable across syncs. Discovery stays advisory: a
		// decision or store failure degrades to a notice, never an aborted
		// source.
		candidates, noPrevious, err := s.decideCandidates(ctx, batch.Source, batch.Documents, batch.At)
		if err != nil {
			result.DiscoveryNotices = append(result.DiscoveryNotices, fmt.Sprintf("%s discovery unavailable: %v", batch.Source, err))
			continue
		}
		if noPrevious {
			result.DiscoveryNotices = append(result.DiscoveryNotices, fmt.Sprintf("%s discovery unavailable: candidate history could not be read", batch.Source))
		}
		if !dryRun {
			for i := range candidates {
				if candidates[i].Status != "resolved" {
					candidates[i].LastReportedFingerprint = candidates[i].EvidenceFingerprint
				}
			}
			if err := s.Repo.SaveDiscoveryCandidates(ctx, candidates); err != nil {
				result.DiscoveryNotices = append(result.DiscoveryNotices, fmt.Sprintf("%s discovery persistence failed: %v", batch.Source, err))
				continue
			}
		}
		result.Candidates = append(result.Candidates, candidates...)
	}
	for _, candidate := range result.Candidates {
		if candidate.Status != "resolved" {
			result.UnresolvedCandidates++
		}
	}
}
