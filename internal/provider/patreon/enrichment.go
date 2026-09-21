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
	for _, ref := range envelope.Data.Relationships.Collections.Data {
		label := domain.LabelReference{Provider: "patreon", Campaign: envelope.Data.Relationships.Campaign.Data.ID, Type: "collection", ID: ref.ID}
		if name := names[ref.ID]; name != "" {
			label.Names = []string{name}
		}
		result.Collections = append(result.Collections, label)
	}
	return result
}
