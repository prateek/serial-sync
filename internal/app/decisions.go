package app

import (
	"context"
	"time"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/discovery"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func (s *Service) authoringDecisions(ctx context.Context, source string, documents []provider.ReleaseDocument) (map[string]classify.ExplainedDecision, error) {
	hold := false
	for _, rule := range s.Config.RulesForSource(source) {
		hold = hold || rule.HoldCandidates
	}
	if !hold {
		return nil, nil
	}
	releases, err := s.storedReleases(ctx, source)
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.NormalizedRelease{}
	for _, release := range releases {
		byID[release.ProviderReleaseID] = release
	}
	for _, doc := range documents {
		byID[doc.Normalized.ProviderReleaseID] = doc.Normalized
	}
	releases = releases[:0]
	for _, release := range byID {
		releases = append(releases, release)
	}
	return discovery.Analyze(source, releases, nil, s.Config, nil, time.Time{}).Decisions, nil
}

func (s *Service) authoringDecision(source string, release domain.NormalizedRelease, history map[string]classify.ExplainedDecision) domain.TrackDecision {
	if explained, ok := history[release.ProviderReleaseID]; ok {
		return explained.Decision
	}
	return s.replayDecision(source, release).Decision
}
