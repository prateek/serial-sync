package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"slices"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func (s *Service) publicationMetadata(sourceID string, release domain.NormalizedRelease, decision domain.TrackDecision) (*domain.PublicationMetadata, error) {
	return publicationMetadataFor(s.Config, sourceID, release, decision)
}

// publicationMetadataFor resolves the publication metadata a reading copy
// carries from an explicit config, so replay and applied-library previews
// resolve against the candidate config rather than the live one.
func publicationMetadataFor(cfg *config.Config, sourceID string, release domain.NormalizedRelease, decision domain.TrackDecision) (*domain.PublicationMetadata, error) {
	if decision.OutputFormat != domain.OutputFormatEPUB {
		return nil, nil
	}
	result := &domain.PublicationMetadata{SourceURL: release.URL}
	var profiles []string
	if source, ok := cfg.SourceByID(sourceID); ok && source.AuthorProfile != "" {
		profiles = []string{source.AuthorProfile}
	}
	apply := func(metadata config.PublicationConfig) error {
		if metadata.IdentitySource != "" {
			result.IdentitySource = domain.PublicationIdentitySource(metadata.IdentitySource)
			if result.IdentitySource == domain.PublicationIdentityEmbedded {
				result.IdentitySource = ""
			}
		}
		if metadata.Description != "" {
			result.Description, result.DescriptionOverride = metadata.Description, true
		}
		if metadata.Language != "" {
			result.Language = metadata.Language
		}
		if metadata.Cover.Path != "" {
			asset, err := publicationAsset(metadata.Cover)
			if err != nil {
				return err
			}
			result.Cover, result.CoverOverride = asset, true
		}
		for _, link := range metadata.Links {
			if !slices.Contains(result.Links, link) {
				result.Links = append(result.Links, link)
			}
		}
		return nil
	}
	for _, series := range cfg.Series {
		if series.ID != decision.SeriesID {
			continue
		}
		if len(series.AuthorProfiles) > 0 {
			profiles = series.AuthorProfiles
		}
		if err := apply(series.Metadata); err != nil {
			return nil, err
		}
		for _, book := range series.Books {
			if book.ID != decision.BookID {
				continue
			}
			if release.Enrichment != nil && book.Collection != nil {
				for _, metadata := range release.Enrichment.Metadata {
					if metadata.ID != book.Collection.ID {
						continue
					}
					if result.Description == "" {
						result.Description = metadata.Description
					}
					if result.Cover == nil && metadata.Cover != nil && metadata.Cover.Path != "" {
						result.Cover = metadata.Cover
					}
				}
			}
			if err := apply(book.Metadata); err != nil {
				return nil, err
			}
		}
	}
	for _, id := range profiles {
		for _, profile := range cfg.AuthorProfiles {
			if profile.ID != id {
				continue
			}
			portrait, err := publicationAsset(profile.Portrait)
			if err != nil {
				return nil, err
			}
			result.Authors = append(result.Authors, domain.AuthorProfile{ID: id, Name: profile.Name, Biography: profile.Biography, URL: profile.URL, Portrait: portrait})
		}
	}
	if len(result.Authors) == 0 && release.Enrichment != nil && release.Enrichment.Author != nil {
		profile := *release.Enrichment.Author
		if profile.Portrait != nil && profile.Portrait.Path == "" {
			profile.Portrait = nil
		}
		result.Authors = []domain.AuthorProfile{profile}
	}
	if release.URL != "" && !slices.Contains(result.Links, release.URL) {
		result.Links = append(result.Links, release.URL)
	}
	return result, nil
}

func publicationAsset(asset config.AssetConfig) (*domain.MetadataAsset, error) {
	if asset.Path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(asset.Path)
	if err != nil {
		return nil, fmt.Errorf("metadata asset %s: %w", asset.Path, err)
	}
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("metadata asset %s exceeds 16 MiB", asset.Path)
	}
	mediaType := http.DetectContentType(data)
	if mediaType != "image/jpeg" && mediaType != "image/png" && mediaType != "image/gif" {
		return nil, fmt.Errorf("metadata asset %s must be JPEG, PNG or GIF", asset.Path)
	}
	hash := sha256.Sum256(data)
	return &domain.MetadataAsset{Path: asset.Path, SHA256: hex.EncodeToString(hash[:]), MediaType: mediaType, SourceURL: asset.SourceURL}, nil
}
