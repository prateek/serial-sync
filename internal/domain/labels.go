package domain

const NormalizerVersion = 2

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
	CaptureFingerprint string           `json:"capture_fingerprint,omitempty"`
	NormalizerVersion  int              `json:"normalizer_version"`
	Collections        []LabelReference `json:"collections,omitempty"`
}
