package domain

type PublicationIdentitySource string

const (
	PublicationIdentityEmbedded PublicationIdentitySource = "embedded"
	PublicationIdentityRelease  PublicationIdentitySource = "release"
)

// PublicationMetadata is an edition input, separate from upstream content identity.
type PublicationMetadata struct {
	IdentitySource      PublicationIdentitySource `json:"identity_source,omitempty"`
	Description         string                    `json:"description,omitempty"`
	DescriptionOverride bool                      `json:"description_override,omitempty"`
	Language            string                    `json:"language,omitempty"`
	Cover               *MetadataAsset            `json:"cover,omitempty"`
	CoverOverride       bool                      `json:"cover_override,omitempty"`
	Authors             []AuthorProfile           `json:"authors,omitempty"`
	Links               []string                  `json:"links,omitempty"`
}

type MetadataAsset struct {
	Path      string `json:"path,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
}

type AuthorProfile struct {
	ID        string         `json:"id"`
	Name      string         `json:"name,omitempty"`
	Biography string         `json:"biography,omitempty"`
	URL       string         `json:"url,omitempty"`
	Portrait  *MetadataAsset `json:"portrait,omitempty"`
}

type CollectionMetadata struct {
	ID          string         `json:"id"`
	Description string         `json:"description,omitempty"`
	Cover       *MetadataAsset `json:"cover,omitempty"`
}
