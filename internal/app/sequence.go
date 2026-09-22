package app

import (
	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/sequence"
)

// archivedSequence answers where a stored chapter sits in its series. The
// sidecar is the record of the last build; the Detect fallback below serves
// ONLY sidecar-less legacy chapters (written before the sidecar existed) and
// can go away once those are rebuilt.
func archivedSequence(candidate domain.PublishCandidate, series config.SeriesConfig) domain.Sequence {
	if recorded := artifact.ArchivedSequence(candidate.Artifact); recorded != nil {
		return *recorded
	}
	return sequence.Resolve(series, sequence.Detect(candidate.Release.Title, candidate.Artifact.Filename), "")
}

func (s *Service) numberedDecision(sourceID string, release domain.NormalizedRelease, decision domain.TrackDecision) domain.TrackDecision {
	return sequence.Apply(s.Config, sourceID, release, decision)
}
