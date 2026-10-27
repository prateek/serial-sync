package app

import (
	"context"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/store"
)

type catalogReader struct {
	catalog  store.CatalogReader
	enricher provider.CapturedEnricher
}

func (s *Service) catalogReader() *catalogReader {
	return &catalogReader{catalog: s.Repo, enricher: s.Providers}
}

func (s *catalogReader) enrichCaptured(release domain.NormalizedRelease, raw []byte) (domain.NormalizedRelease, error) {
	if s.enricher == nil {
		return release, nil
	}
	return s.enricher.EnrichCaptured(release, raw)
}

func (s *catalogReader) resolveTrack(ctx context.Context, source domain.Source, decision domain.TrackDecision) (domain.StoryTrack, error) {
	existing, err := s.catalog.GetTrackBySourceAndKey(ctx, source.ID, decision.TrackKey)
	if err != nil {
		return domain.StoryTrack{}, err
	}
	canonicalAuthor := firstNonEmpty(decision.CanonicalAuthor, source.CreatorName)
	seriesMeta := buildSeriesMeta(decision)
	if existing != nil {
		copy := *existing
		existing = &copy
		existing.TrackName = decision.TrackName
		existing.CanonicalAuthor = canonicalAuthor
		existing.SeriesMeta = seriesMeta
		existing.OutputPolicy = string(decision.OutputFormat)
		existing.UpdatedAt = time.Now().UTC()
		return *existing, nil
	}
	return domain.StoryTrack{
		ID:              catalogIdentity("trk", source.ID, decision.TrackKey),
		SourceID:        source.ID,
		TrackKey:        decision.TrackKey,
		TrackName:       decision.TrackName,
		CanonicalAuthor: canonicalAuthor,
		SeriesMeta:      seriesMeta,
		OutputPolicy:    string(decision.OutputFormat),
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}, nil
}
