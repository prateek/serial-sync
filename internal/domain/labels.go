package domain

const NormalizerVersion = 4

type LabelReference struct {
	Provider string   `json:"provider"`
	Campaign string   `json:"campaign"`
	Type     string   `json:"type"`
	ID       string   `json:"id"`
	Names    []string `json:"names,omitempty"`
}

func (label LabelReference) Key() string {
	return label.Provider + "/" + label.Campaign + "/" + label.Type + "/" + label.ID
}

type ReleaseEnrichment struct {
	Author             *AuthorProfile       `json:"author,omitempty"`
	Metadata           []CollectionMetadata `json:"metadata,omitempty"`
	CaptureFingerprint string               `json:"capture_fingerprint,omitempty"`
	NormalizerVersion  int                  `json:"normalizer_version"`
	Collections        []LabelReference     `json:"collections,omitempty"`
}

func (metadata *ReleaseEnrichment) Assets() []*MetadataAsset {
	if metadata == nil {
		return nil
	}
	var assets []*MetadataAsset
	if metadata.Author != nil && metadata.Author.Portrait != nil {
		assets = append(assets, metadata.Author.Portrait)
	}
	for _, collection := range metadata.Metadata {
		if collection.Cover != nil {
			assets = append(assets, collection.Cover)
		}
	}
	return assets
}
