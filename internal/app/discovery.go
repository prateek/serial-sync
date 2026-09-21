package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/discovery"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func (s *Service) discoverCaptured(ctx context.Context, source string, observed []provider.ReleaseDocument, observedAt time.Time, dryRun bool) ([]domain.DiscoveryCandidate, error) {
	releases, err := s.storedReleases(ctx, source)
	if err != nil {
		return nil, err
	}
	current := map[string]int{}
	fresh := map[string]bool{}
	for i, release := range releases {
		current[release.ProviderReleaseID] = i
	}
	for _, doc := range observed {
		if !dryRun && doc.Normalized.Enrichment != nil {
			if err := s.saveEnrichment(ctx, source, doc.Normalized); err != nil {
				return nil, err
			}
		}
		release := doc.Normalized
		fresh[release.ProviderReleaseID] = true
		if index, ok := current[release.ProviderReleaseID]; ok {
			captured := releases[index]
			if release.Title == captured.Title && release.TextHTML == captured.TextHTML && release.TextPlain == captured.TextPlain && release.EditedAt.Equal(captured.EditedAt) {
				release.Attachments = append([]domain.Attachment(nil), release.Attachments...)
				for i := range release.Attachments {
					for _, file := range captured.Attachments {
						if file.FileName == release.Attachments[i].FileName {
							release.Attachments[i].SHA256 = file.SHA256
							break
						}
					}
				}
			}
			releases[index] = release
		} else {
			current[release.ProviderReleaseID] = len(releases)
			releases = append(releases, release)
		}
	}

	previous, err := s.Repo.ListDiscoveryCandidates(ctx, source)
	if err != nil {
		return nil, err
	}
	candidates := discovery.Observe(source, releases, fresh, s.Config, previous, observedAt)
	if !dryRun {
		for i := range candidates {
			if candidates[i].Status != "resolved" {
				candidates[i].LastReportedFingerprint = candidates[i].EvidenceFingerprint
			}
		}
		if err := s.Repo.SaveDiscoveryCandidates(ctx, candidates); err != nil {
			return nil, err
		}
	}
	return candidates, nil
}

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

type observedBatch struct {
	Source    string
	Documents []provider.ReleaseDocument
	At        time.Time
}

func (s *Service) reportObservedCandidates(ctx context.Context, batches []observedBatch, dryRun bool, result *domain.SyncResult) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	for _, batch := range batches {
		candidates, err := s.discoverCaptured(ctx, batch.Source, batch.Documents, batch.At, dryRun)
		if err != nil {
			result.DiscoveryNotices = append(result.DiscoveryNotices, fmt.Sprintf("%s discovery unavailable: %v", batch.Source, err))
			continue
		}
		result.Candidates = append(result.Candidates, candidates...)
	}
	for _, candidate := range result.Candidates {
		if candidate.Status != "resolved" {
			result.UnresolvedCandidates++
		}
	}
}
