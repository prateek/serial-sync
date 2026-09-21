package app

import (
	"encoding/json"
	"os"

	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/sequence"
)

func archivedSequence(candidate domain.PublishCandidate, series config.SeriesConfig) domain.Sequence {
	var metadata struct {
		Decision domain.TrackDecision `json:"decision"`
	}
	data, err := os.ReadFile(candidate.Artifact.MetadataRef)
	if err == nil && json.Unmarshal(data, &metadata) == nil && metadata.Decision.Sequence != nil {
		return *metadata.Decision.Sequence
	}
	return sequence.Resolve(series, artifact.DetectSequence(candidate.Release.Title, candidate.Artifact.Filename), "")
}

func (s *Service) numberedDecision(sourceID string, release domain.NormalizedRelease, decision domain.TrackDecision) domain.TrackDecision {
	return sequence.Apply(s.Config, sourceID, release, decision)
}
