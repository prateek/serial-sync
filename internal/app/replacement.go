package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/publish"
)

func resolveCollisionNames(candidates []domain.PublishCandidate) error {
	groups := map[string][]int{}
	for i, candidate := range candidates {
		key := strings.ToLower(filepath.Join(candidate.Source.ID, candidate.Track.TrackKey, candidate.Artifact.Filename))
		groups[key] = append(groups[key], i)
	}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		for _, i := range group {
			candidate := &candidates[i]
			identity := candidate.Source.Provider + "\x00" + candidate.Source.ID + "\x00" + candidate.Release.ProviderReleaseID
			suffix := sha256.Sum256([]byte(identity))
			ext := filepath.Ext(candidate.Artifact.Filename)
			candidate.Artifact.Filename = fmt.Sprintf("%s-r%x%s", strings.TrimSuffix(candidate.Artifact.Filename, ext), suffix[:8], ext)
		}
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		key := strings.ToLower(filepath.Join(candidate.Source.ID, candidate.Track.TrackKey, candidate.Artifact.Filename))
		if seen[key] {
			return fmt.Errorf("unresolved filename collision: %s", key)
		}
		seen[key] = true
	}
	return nil
}

func (s *Service) validatePublishTargets(cfg *config.Config, scope deliveryScope, targetFilter string) error {
	targets := selectPublishers(cfg.Publishers, targetFilter)
	if len(targets) == 0 {
		return fmt.Errorf("no enabled publishers match %q", targetFilter)
	}
	for _, target := range targets {
		if publish.NormalizedPublisherKind(target.Kind) != "exec" || target.ProtocolVersion == 2 {
			continue
		}
		for _, series := range cfg.Series {
			if scope.SeriesID != "" && series.ID != scope.SeriesID || cfg.SeriesOutput(series).Bundling != "volume" {
				continue
			}
			for _, input := range series.AuthoringInputs() {
				if s.sourceInScope(firstNonEmpty(input.Source, series.Source), scope.SourceID, scope.Rebuild, cfg) {
					return fmt.Errorf("exec publisher %q requires protocol_version = 2 for volume output and retirement", target.ID)
				}
			}
		}
	}
	return nil
}

func (s *Service) validateLegacyReplacements(ctx context.Context, targets []config.PublisherConfig, candidates []domain.PublishCandidate, recordsByTarget map[string][]domain.PublishRecordBundle) error {
	for _, target := range targets {
		if publish.NormalizedPublisherKind(target.Kind) != "exec" || target.ProtocolVersion == 2 {
			continue
		}
		for _, candidate := range candidates {
			if candidate.Volume != nil {
				return fmt.Errorf("exec publisher %q requires protocol_version = 2 for volumes", target.ID)
			}
			for _, old := range recordsByTarget[target.ID] {
				if old.Record.Status == domain.PublishStatusPublished && old.Release.ID == candidate.Release.ID && (old.Record.Filename != candidate.Artifact.Filename || old.Track.TrackKey != candidate.Track.TrackKey) {
					return fmt.Errorf("exec publisher %q requires protocol_version = 2 to retire renamed chapter %s", target.ID, old.Artifact.Filename)
				}
			}
		}
	}
	return nil
}

func (s *Service) validateFrozenNames(ctx context.Context, targets []config.PublisherConfig, candidates []domain.PublishCandidate, recordsByTarget map[string][]domain.PublishRecordBundle) error {
	for _, target := range targets {
		pending, pendingErr := s.pendingDelivery(ctx, s.Config, deliveryScope{}, target)
		for _, candidate := range candidates {
			if candidate.Volume == nil && !artifact.IsLegacy(candidate.Artifact) {
				continue
			}
			approved := false
			if pending != nil {
				for _, planned := range pending.Candidates {
					if planned.Artifact.ID == candidate.Artifact.ID && planned.Artifact.Filename == candidate.Artifact.Filename {
						approved = true
					}
				}
			}
			if approved {
				continue
			}
			for _, old := range recordsByTarget[target.ID] {
				if (old.Record.Status == domain.PublishStatusPublished || old.Record.Status == domain.PublishStatusPublishing) && old.Artifact.ID == candidate.Artifact.ID && old.Record.Filename != candidate.Artifact.Filename {
					if pendingErr != nil {
						return pendingErr
					}
					return fmt.Errorf("target %s: collision would rename frozen or legacy output %s; use run --rebuild", target.ID, old.Record.Filename)
				}
			}
		}
	}
	return nil
}

func retirementOwnedHashes(records []domain.PublishRecordBundle, old domain.PublishRecordBundle) []string {
	hashes := []string{old.Artifact.SHA256}
	if old.Release.ID == "" {
		return hashes
	}
	for _, record := range records {
		if record.Record.Status == domain.PublishStatusPublished && record.Release.ID == old.Release.ID && record.Record.TargetRef == old.Record.TargetRef {
			hashes = append(hashes, record.Artifact.SHA256)
		}
	}
	return hashes
}

func ownedHashesForPath(records []domain.PublishRecordBundle, path string) []string {
	var hashes []string
	for _, record := range records {
		if record.Record.TargetRef == path && (record.Record.Status == domain.PublishStatusPublished || record.Record.Status == domain.PublishStatusPublishing) {
			hashes = append(hashes, record.Artifact.SHA256)
		}
	}
	return hashes
}
