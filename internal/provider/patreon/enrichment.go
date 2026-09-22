package patreon

import (
	"encoding/json"
	"fmt"

	"github.com/prateek/serial-sync/internal/domain"
)

// EnrichCaptured reads only raw bytes already captured with this release.
func (c *Client) EnrichCaptured(release domain.NormalizedRelease, raw []byte) (domain.NormalizedRelease, error) {
	var envelope postEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return release, err
	}
	if envelope.Data.ID != release.ProviderReleaseID {
		return release, fmt.Errorf("raw payload identity does not match release %s", release.ProviderReleaseID)
	}
	release.Enrichment = collectionEnrichment(envelope)
	return release, nil
}

func collectionEnrichment(envelope postEnvelope) *domain.ReleaseEnrichment {
	names := map[string]string{}
	for _, item := range envelope.Included {
		if item.Type != "collection" {
			continue
		}
		var attrs struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(item.Attributes, &attrs) == nil {
			names[item.ID] = attrs.Title
		}
	}
	result := &domain.ReleaseEnrichment{NormalizerVersion: domain.NormalizerVersion}
	creatorID := envelope.Data.Relationships.User.Data.ID
	campaignBiography := ""
	nonCoverImages := map[string]bool{}
	for _, item := range envelope.Included {
		if item.Type != "campaign" || item.ID != envelope.Data.Relationships.Campaign.Data.ID {
			continue
		}
		if item.Relationships.Creator.Data.ID != "" {
			creatorID = item.Relationships.Creator.Data.ID
		}
		var attrs struct {
			Summary        string `json:"summary"`
			AvatarPhotoURL string `json:"avatar_photo_url"`
			CoverPhotoURL  string `json:"cover_photo_url"`
		}
		if json.Unmarshal(item.Attributes, &attrs) == nil {
			campaignBiography = stripTags(attrs.Summary)
			nonCoverImages[metadataImageIdentity(attrs.AvatarPhotoURL)] = true
			nonCoverImages[metadataImageIdentity(attrs.CoverPhotoURL)] = true
		}
	}
	for _, item := range envelope.Included {
		if item.Type == "user" && item.ID == creatorID {
			var attrs struct {
				FullName string `json:"full_name"`
				About    string `json:"about"`
				URL      string `json:"url"`
				ImageURL string `json:"image_url"`
			}
			if json.Unmarshal(item.Attributes, &attrs) == nil {
				result.Author = &domain.AuthorProfile{ID: "patreon:user:" + item.ID, Name: attrs.FullName, Biography: firstNonEmpty(stripTags(attrs.About), campaignBiography), URL: attrs.URL}
				if attrs.ImageURL != "" {
					result.Author.Portrait = &domain.MetadataAsset{SourceURL: attrs.ImageURL}
					nonCoverImages[metadataImageIdentity(attrs.ImageURL)] = true
				}
			}
		}
	}
	for _, ref := range envelope.Data.Relationships.Collections.Data {
		label := domain.LabelReference{Provider: "patreon", Campaign: envelope.Data.Relationships.Campaign.Data.ID, Type: "collection", ID: ref.ID}
		if name := names[ref.ID]; name != "" {
			label.Names = []string{name}
		}
		result.Collections = append(result.Collections, label)
		for _, item := range envelope.Included {
			if item.Type != "collection" || item.ID != ref.ID {
				continue
			}
			var attrs struct {
				Description   string `json:"description"`
				CoverImageURL string `json:"cover_image_url"`
				Thumbnail     struct {
					Default string `json:"default"`
				} `json:"thumbnail"`
			}
			if json.Unmarshal(item.Attributes, &attrs) == nil {
				metadata := domain.CollectionMetadata{ID: item.ID, Description: stripTags(attrs.Description)}
				coverURL := firstNonEmpty(attrs.CoverImageURL, attrs.Thumbnail.Default)
				if coverURL != "" && !nonCoverImages[metadataImageIdentity(coverURL)] {
					metadata.Cover = &domain.MetadataAsset{SourceURL: coverURL}
				}
				result.Metadata = append(result.Metadata, metadata)
			}
		}
	}
	return result
}
