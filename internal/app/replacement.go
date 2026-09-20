package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

func (s *Service) validatePublishTargets(sourceFilter, targetFilter, seriesFilter string, rebuild bool) error {
	targets := selectPublishers(s.Config.Publishers, targetFilter)
	if len(targets) == 0 {
		return fmt.Errorf("no enabled publishers match %q", targetFilter)
	}
	for _, target := range targets {
		if normalizedPublisherKind(target.Kind) != "exec" || target.ProtocolVersion == 2 {
			continue
		}
		for _, series := range s.Config.Series {
			if seriesFilter != "" && series.ID != seriesFilter || config.SeriesOutputDefaults(series.Output).Bundling != "volume" {
				continue
			}
			for _, input := range series.Inputs {
				if s.sourceInScope(input.Source, sourceFilter, rebuild) {
					return fmt.Errorf("exec publisher %q requires protocol_version = 2 for volume output and retirement", target.ID)
				}
			}
		}
	}
	return nil
}

func (s *Service) validateLegacyReplacements(ctx context.Context, targets []config.PublisherConfig, candidates []domain.PublishCandidate) error {
	for _, target := range targets {
		if normalizedPublisherKind(target.Kind) != "exec" || target.ProtocolVersion == 2 {
			continue
		}
		records, err := s.Repo.ListPublishRecords(ctx, "", target.ID)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			if candidate.Volume != nil {
				return fmt.Errorf("exec publisher %q requires protocol_version = 2 for volumes", target.ID)
			}
			for _, old := range records {
				if old.Record.Status == domain.PublishStatusPublished && old.Release.ID == candidate.Release.ID && (old.Record.Filename != candidate.Artifact.Filename || old.Track.TrackKey != candidate.Track.TrackKey) {
					return fmt.Errorf("exec publisher %q requires protocol_version = 2 to retire renamed chapter %s", target.ID, old.Artifact.Filename)
				}
			}
		}
	}
	return nil
}

func (s *Service) validateFrozenNames(ctx context.Context, targets []config.PublisherConfig, candidates []domain.PublishCandidate) error {
	for _, target := range targets {
		pending, pendingErr := s.pendingDelivery(ctx, target, "", "", false)
		records, err := s.Repo.ListPublishRecords(ctx, "", target.ID)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			if candidate.Volume == nil && !legacyArtifact(candidate.Artifact) {
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
			for _, old := range records {
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

var errCyclicReplacement = errors.New("cyclic same-path replacement; choose distinct output names for this regroup")

func (s *Service) orderReplacements(ctx context.Context, target config.PublisherConfig, candidates []domain.PublishCandidate) ([]domain.PublishCandidate, map[string][]string, error) {
	dependencies := map[string][]string{}
	if normalizedPublisherKind(target.Kind) != "filesystem" {
		return candidates, dependencies, nil
	}
	editions, err := s.Repo.ListVolumeEditions(ctx)
	if err != nil {
		return nil, nil, err
	}
	members := map[string][]string{}
	for _, edition := range editions {
		for _, member := range edition.Members {
			members[edition.Artifact.ID] = append(members[edition.Artifact.ID], member.ReleaseID)
		}
	}
	byRelease := map[string]string{}
	byID := map[string]domain.PublishCandidate{}
	for _, candidate := range candidates {
		byID[candidate.Artifact.ID] = candidate
		if candidate.Volume == nil {
			byRelease[candidate.Release.ID] = candidate.Artifact.ID
		} else {
			for _, member := range candidate.Volume.Members {
				byRelease[member.ReleaseID] = candidate.Artifact.ID
			}
		}
	}
	records, err := s.Repo.ListPublishRecords(ctx, "", target.ID)
	if err != nil {
		return nil, nil, err
	}
	for _, candidate := range candidates {
		path := filepath.Join(target.Path, candidate.Source.ID, candidate.Track.TrackKey, candidate.Artifact.Filename)
		for _, old := range records {
			if old.Record.Status != domain.PublishStatusPublished || old.Record.TargetRef != path || old.Artifact.ID == candidate.Artifact.ID {
				continue
			}
			for _, releaseID := range members[old.Artifact.ID] {
				replacementID := byRelease[releaseID]
				if replacementID == "" {
					return nil, nil, fmt.Errorf("target %s: replacement for %s does not cover chapter %s", target.ID, path, releaseID)
				}
				if replacementID != candidate.Artifact.ID {
					dependencies[candidate.Artifact.ID] = append(dependencies[candidate.Artifact.ID], replacementID)
				}
			}
		}
	}
	var ordered []domain.PublishCandidate
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("target %s: %w", target.ID, errCyclicReplacement)
		}
		visiting[id] = true
		sort.Strings(dependencies[id])
		for _, dependency := range dependencies[id] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visited[id], visiting[id] = true, false
		ordered = append(ordered, byID[id])
		return nil
	}
	for _, candidate := range candidates {
		if err := visit(candidate.Artifact.ID); err != nil {
			return nil, nil, err
		}
	}
	return ordered, dependencies, nil
}

func (s *Service) retirePreviousPaths(ctx context.Context, candidates []domain.PublishCandidate, targets []config.PublisherConfig, results []domain.PublishItemResult, eventScope string) error {
	editions, err := s.Repo.ListVolumeEditions(ctx)
	if err != nil {
		return err
	}
	byArtifact := map[string][]domain.VolumeMember{}
	for _, edition := range editions {
		byArtifact[edition.Artifact.ID] = edition.Members
	}
	var failures []error
	for _, target := range targets {
		kind := normalizedPublisherKind(target.Kind)
		if kind != "filesystem" && kind != "exec" {
			continue
		}
		desired, delivered := map[string]bool{}, map[string]bool{}
		desiredArtifacts := map[string]bool{}
		var replacements []domain.PublishCandidate
		for _, item := range results {
			if item.TargetID == target.ID && (item.Action == "published" || item.Action == "skipped") {
				delivered[item.ArtifactID] = true
			}
		}
		covered := map[string]bool{}
		for _, candidate := range candidates {
			desiredArtifacts[candidate.Artifact.ID+"\x00"+candidate.Artifact.Filename] = true
			desired[filepath.Join(target.Path, candidate.Source.ID, candidate.Track.TrackKey, candidate.Artifact.Filename)] = true
			if !delivered[candidate.Artifact.ID] {
				continue
			}
			replacements = append(replacements, candidate)
			if candidate.Volume != nil {
				for _, member := range candidate.Volume.Members {
					covered[member.ReleaseID] = true
				}
			} else {
				covered[candidate.Release.ID] = true
			}
		}
		records, err := s.Repo.ListPublishRecords(ctx, "", target.ID)
		if err != nil {
			return err
		}
		for _, old := range records {
			samePath := kind == "filesystem" && desired[old.Record.TargetRef]
			if old.Record.Status != domain.PublishStatusPublished || (samePath || kind == "exec") && desiredArtifacts[old.Artifact.ID+"\x00"+old.Record.Filename] {
				continue
			}
			complete := covered[old.Release.ID]
			if members := byArtifact[old.Artifact.ID]; len(members) > 0 {
				complete = true
				for _, member := range members {
					if !covered[member.ReleaseID] {
						complete = false
						break
					}
				}
			}
			if !complete {
				continue
			}
			if kind == "exec" && target.ProtocolVersion != 2 {
				continue
			}
			if kind == "exec" {
				old.Artifact.Filename = old.Record.Filename
				required := map[string]bool{old.Release.ID: true}
				for _, member := range byArtifact[old.Artifact.ID] {
					required[member.ReleaseID] = true
				}
				var related []domain.PublishCandidate
				for _, replacement := range replacements {
					covers := required[replacement.Release.ID] && replacement.Release.ID != ""
					if replacement.Volume != nil {
						for _, member := range replacement.Volume.Members {
							if required[member.ReleaseID] {
								covers = true
							}
						}
					}
					if covers {
						related = append(related, replacement)
					}
				}
				sort.Slice(related, func(i, j int) bool { return related[i].Artifact.ID < related[j].Artifact.ID })
				if err := publish.SupersedeExec(ctx, publish.ExecTarget{ID: target.ID, Command: target.Command, ProtocolVersion: target.ProtocolVersion, EventScope: eventScope}, old, related); err != nil {
					failures = append(failures, err)
					continue
				}
			} else if !samePath {
				current, err := publish.FileHash(old.Record.TargetRef)
				if err == nil && current != "" && current != old.Artifact.SHA256 {
					err = fmt.Errorf("retirement ownership conflict: %s", old.Record.TargetRef)
				}
				if err == nil && current != "" {
					err = os.Remove(old.Record.TargetRef)
				}
				if err != nil {
					failures = append(failures, err)
					continue
				}
			}
			old.Record.Status = domain.PublishStatusSuperseded
			if err := s.Repo.UpsertPublishRecord(ctx, old.Record); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
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
