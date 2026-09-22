package app

import (
	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/sequence"
)

func archivedSequence(candidate domain.PublishCandidate, series config.SeriesConfig) domain.Sequence {
	if recorded := artifact.ArchivedSequence(candidate.Artifact); recorded != nil {
		return *recorded
	}
	return sequence.Resolve(series, sequence.Detect(candidate.Release.Title, candidate.Artifact.Filename), "")
}

func (s *Service) numberedDecision(sourceID string, release domain.NormalizedRelease, decision domain.TrackDecision) domain.TrackDecision {
	return sequence.Apply(s.Config, sourceID, release, decision)
}
