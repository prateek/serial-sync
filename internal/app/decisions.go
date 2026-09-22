package app

import (
	"context"
	"time"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/discovery"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

// decideReleases is the one release decider: history of normalized releases
// in, explained, numbered, hold-aware decisions and discovery candidates out.
// It is store-free so sync, rebuild, preview and replay all obtain decisions
// from the same function and hold behaviour is testable at this interface.
func decideReleases(source string, releases []domain.NormalizedRelease, fresh map[string]bool, cfg *config.Config, previous []domain.DiscoveryCandidate, now time.Time) (map[string]classify.ExplainedDecision, []domain.DiscoveryCandidate) {
	analysis := discovery.Analyze(source, releases, fresh, cfg, previous, now)
	return analysis.Decisions, analysis.Candidates
}

// observingReleases reads the stored history for a source and merges the
// observed documents into it, backfilling captured attachment hashes for
// unchanged releases so the deciding inputs carry the same identity.
func (s *Service) observingReleases(ctx context.Context, source string, documents []provider.ReleaseDocument) ([]domain.NormalizedRelease, map[string]bool, error) {
	releases, err := s.storedReleases(ctx, source)
	if err != nil {
		return nil, nil, err
	}
	current := map[string]int{}
	for i, release := range releases {
		current[release.ProviderReleaseID] = i
	}
	fresh := map[string]bool{}
	for _, doc := range documents {
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
	return releases, fresh, nil
}

// decideSource is the sync-facing release decider: it returns the decisions
// for every observed release. Only a hold rule makes a decision depend on the
// source's history, so the stored releases are read only then; otherwise the
// observed documents decide on their own with no store read. It writes
// nothing; discovery candidate computation happens after releases are stored
// so captured hashes sit in the evidence fingerprints.
func (s *Service) decideSource(ctx context.Context, source string, documents []provider.ReleaseDocument, observedAt time.Time, cfg *config.Config) (map[string]classify.ExplainedDecision, error) {
	if cfg == nil {
		cfg = s.Config
	}
	var releases []domain.NormalizedRelease
	if cfg.Compiled().HoldsCandidates(source) {
		var err error
		releases, _, err = s.observingReleases(ctx, source, documents)
		if err != nil {
			return nil, err
		}
	} else {
		releases = make([]domain.NormalizedRelease, 0, len(documents))
		for _, doc := range documents {
			releases = append(releases, doc.Normalized)
		}
	}
	decisions, _ := decideReleases(source, releases, map[string]bool{}, cfg, nil, observedAt)
	return decisions, nil
}

// decideCandidates is the discovery pass: it re-derives candidates from the
// stored releases (with captured attachment hashes) merged with the observed
// documents for the run, reconciled against the stored candidates. Discovery
// is advisory, so a candidate read failure reports no candidates for this run
// rather than reconciling against an empty history, which would resurrect
// dismissed candidates; the boolean reports that the history was unreadable.
func (s *Service) decideCandidates(ctx context.Context, source string, documents []provider.ReleaseDocument, observedAt time.Time) ([]domain.DiscoveryCandidate, bool, error) {
	releases, fresh, err := s.observingReleases(ctx, source, documents)
	if err != nil {
		return nil, false, err
	}
	previous, err := s.Repo.ListDiscoveryCandidates(ctx, source)
	if err != nil {
		return nil, true, nil
	}
	_, candidates := decideReleases(source, releases, fresh, s.Config, previous, observedAt)
	return candidates, false, nil
}

func (s *Service) authoringDecisions(ctx context.Context, cfg *config.Config, source string, documents []provider.ReleaseDocument) (map[string]classify.ExplainedDecision, error) {
	return s.decideSource(ctx, source, documents, time.Time{}, cfg)
}
