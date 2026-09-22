package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prateek/serial-sync/internal/domain"
)

func (s *Service) loadStoredNormalized(ctx context.Context, release domain.Release) (domain.NormalizedRelease, error) {
	var normalized domain.NormalizedRelease
	data, err := os.ReadFile(release.NormalizedPayloadRef)
	if err != nil {
		return normalized, err
	}
	if err := json.Unmarshal(data, &normalized); err != nil {
		return normalized, err
	}
	if normalized.ProviderReleaseID != release.ProviderReleaseID {
		return normalized, fmt.Errorf("stored payload identity does not match release %s", release.ProviderReleaseID)
	}
	metadata, err := s.Repo.GetReleaseEnrichment(ctx, release.SourceID, release.ProviderReleaseID, enrichmentFingerprint(normalized))
	if err != nil {
		return normalized, err
	}
	if metadata != nil && metadata.NormalizerVersion >= domain.NormalizerVersion && metadata.CaptureFingerprint == enrichmentFingerprint(normalized) {
		normalized.Enrichment = metadata
		return normalized, nil
	}
	if normalized.Enrichment != nil && normalized.Enrichment.NormalizerVersion >= domain.NormalizerVersion {
		return normalized, nil
	}
	raw, err := os.ReadFile(release.RawPayloadRef)
	if os.IsNotExist(err) || release.RawPayloadRef == "" {
		return normalized, nil
	}
	if err != nil {
		return normalized, err
	}
	return s.Providers.EnrichCaptured(normalized, raw)
}

func (s *Service) EnrichStored(ctx context.Context, source string) (int, error) {
	releases, err := s.Repo.ListReleases(ctx, source)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, release := range releases {
		normalized, err := s.loadStoredNormalized(ctx, release)
		if err != nil {
			return count, fmt.Errorf("enrich %s: %w", release.ProviderReleaseID, err)
		}
		if normalized.Enrichment == nil {
			return count, fmt.Errorf("release %s has no captured raw bytes or supported offline enricher", release.ProviderReleaseID)
		}
		if err := s.saveEnrichment(ctx, release.SourceID, normalized); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func enrichmentFingerprint(release domain.NormalizedRelease) string {
	capture := hashableNormalizedRelease(release)
	for i := range capture.Attachments {
		capture.Attachments[i].SHA256 = ""
	}
	raw, _ := json.Marshal(capture)
	return hashBytes(raw)
}

func (s *Service) saveEnrichment(ctx context.Context, source string, release domain.NormalizedRelease) error {
	data, err := json.Marshal(release.Enrichment)
	if err != nil {
		return err
	}
	var metadata domain.ReleaseEnrichment
	if err := json.Unmarshal(data, &metadata); err != nil {
		return err
	}
	for _, asset := range metadata.Assets() {
		if asset.Path == "" {
			continue
		}
		data, err := os.ReadFile(asset.Path)
		if err != nil {
			return err
		}
		if hashBytes(data) != asset.SHA256 {
			return fmt.Errorf("metadata image changed: %s", asset.Path)
		}
		target := filepath.Join(s.Config.Runtime.ArtifactRoot, "metadata-assets", asset.SHA256)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0644); err != nil {
			return err
		}
		asset.Path = target
	}
	metadata.CaptureFingerprint = enrichmentFingerprint(release)
	return s.Repo.SaveReleaseEnrichment(ctx, source, release.ProviderReleaseID, metadata)
}
