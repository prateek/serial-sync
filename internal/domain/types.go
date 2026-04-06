package domain

import "time"

type AuthState string

const (
	AuthStateAuthenticated   AuthState = "authenticated"
	AuthStateExpired         AuthState = "expired"
	AuthStateReauthRequired  AuthState = "reauth_required"
	AuthStateChallengeNeeded AuthState = "challenge_required"
)

type ReleaseRole string

const (
	ReleaseRoleChapter           ReleaseRole = "chapter"
	ReleaseRoleReleaseAttachment ReleaseRole = "release_attachment"
	ReleaseRoleAnnouncement      ReleaseRole = "announcement"
	ReleaseRoleSchedule          ReleaseRole = "schedule"
	ReleaseRolePreviewBundle     ReleaseRole = "preview_bundle"
	ReleaseRoleUnknown           ReleaseRole = "unknown"
)

type ContentStrategy string

const (
	ContentStrategyTextPost            ContentStrategy = "text_post"
	ContentStrategyAttachmentPreferred ContentStrategy = "attachment_preferred"
	ContentStrategyAttachmentOnly      ContentStrategy = "attachment_only"
	ContentStrategyTextPlusAttachment  ContentStrategy = "text_plus_attachments"
	ContentStrategyManual              ContentStrategy = "manual"
)

type OutputFormat string

const (
	OutputFormatPreserve OutputFormat = "preserve"
	OutputFormatEPUB     OutputFormat = "epub"
	OutputFormatPDF      OutputFormat = "pdf"
)

type PrefaceMode string

const (
	PrefaceModeNone        PrefaceMode = "none"
	PrefaceModePrependPost PrefaceMode = "prepend_post"
)

type ArtifactState string

const (
	ArtifactStatePlanned      ArtifactState = "planned"
	ArtifactStateMaterialized ArtifactState = "materialized"
	ArtifactStatePublishing   ArtifactState = "publishing"
	ArtifactStatePublished    ArtifactState = "published"
	ArtifactStateFailed       ArtifactState = "failed"
)

type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
)

type PublishStatus string

const (
	PublishStatusPublishing PublishStatus = "publishing"
	PublishStatusPublished  PublishStatus = "published"
	PublishStatusFailed     PublishStatus = "failed"
)

type Source struct {
	ID            string    `json:"id"`
	Provider      string    `json:"provider"`
	SourceURL     string    `json:"source_url"`
	SourceType    string    `json:"source_type"`
	CreatorID     string    `json:"creator_id"`
	CreatorName   string    `json:"creator_name"`
	AuthProfileID string    `json:"auth_profile_id"`
	Enabled       bool      `json:"enabled"`
	SyncCursor    string    `json:"sync_cursor"`
	LastSyncedAt  time.Time `json:"last_synced_at"`
}

type TrackRule struct {
	ID                 string          `json:"id"`
	SourceID           string          `json:"source_id"`
	Priority           int             `json:"priority"`
	MatchType          string          `json:"match_type"`
	MatchValue         string          `json:"match_value"`
	TrackKey           string          `json:"track_key"`
	TrackName          string          `json:"track_name"`
	ReleaseRole        ReleaseRole     `json:"release_role"`
	ContentStrategy    ContentStrategy `json:"content_strategy"`
	AttachmentGlob     []string        `json:"attachment_glob"`
	AttachmentPriority []string        `json:"attachment_priority"`
	AnthologyMode      bool            `json:"anthology_mode"`
	Enabled            bool            `json:"enabled"`
}

type StoryTrack struct {
	ID              string    `json:"id"`
	SourceID        string    `json:"source_id"`
	TrackKey        string    `json:"track_key"`
	TrackName       string    `json:"track_name"`
	CanonicalAuthor string    `json:"canonical_author"`
	SeriesMeta      string    `json:"series_meta"`
	OutputPolicy    string    `json:"output_policy"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Release struct {
	ID                   string    `json:"id"`
	SourceID             string    `json:"source_id"`
	ProviderReleaseID    string    `json:"provider_release_id"`
	URL                  string    `json:"url"`
	Title                string    `json:"title"`
	PublishedAt          time.Time `json:"published_at"`
	EditedAt             time.Time `json:"edited_at"`
	PostType             string    `json:"post_type"`
	VisibilityState      string    `json:"visibility_state"`
	NormalizedPayloadRef string    `json:"normalized_payload_ref"`
	RawPayloadRef        string    `json:"raw_payload_ref"`
	ContentHash          string    `json:"content_hash"`
	DiscoveredAt         time.Time `json:"discovered_at"`
	Status               string    `json:"status"`
}

type ReleaseAssignment struct {
	ReleaseID   string      `json:"release_id"`
	TrackID     string      `json:"track_id"`
	RuleID      string      `json:"rule_id"`
	ReleaseRole ReleaseRole `json:"release_role"`
	Confidence  float64     `json:"confidence"`
}

type Artifact struct {
	ID            string        `json:"id"`
	ReleaseID     string        `json:"release_id"`
	TrackID       string        `json:"track_id"`
	ArtifactKind  string        `json:"artifact_kind"`
	IsCanonical   bool          `json:"is_canonical"`
	Filename      string        `json:"filename"`
	MIMEType      string        `json:"mime_type"`
	SHA256        string        `json:"sha256"`
	StorageRef    string        `json:"storage_ref"`
	BuiltAt       time.Time     `json:"built_at"`
	State         ArtifactState `json:"state"`
	MetadataRef   string        `json:"metadata_ref"`
	NormalizedRef string        `json:"normalized_ref"`
	RawRef        string        `json:"raw_ref"`
}

type PublishRecord struct {
	ID          string        `json:"id"`
	ArtifactID  string        `json:"artifact_id"`
	TargetID    string        `json:"target_id"`
	TargetKind  string        `json:"target_kind"`
	TargetRef   string        `json:"target_ref"`
	PublishHash string        `json:"publish_hash"`
	PublishedAt time.Time     `json:"published_at"`
	Status      PublishStatus `json:"status"`
	Message     string        `json:"message"`
}

type RunRecord struct {
	ID          string     `json:"id"`
	Command     string     `json:"command"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Status      RunStatus  `json:"status"`
	Summary     string     `json:"summary"`
	SourceScope string     `json:"source_scope"`
	DryRun      bool       `json:"dry_run"`
}

type EventRecord struct {
	ID         string    `json:"id"`
	RunID      string    `json:"run_id"`
	Timestamp  time.Time `json:"timestamp"`
	Level      string    `json:"level"`
	Component  string    `json:"component"`
	Message    string    `json:"message"`
	EntityKind string    `json:"entity_kind"`
	EntityID   string    `json:"entity_id"`
	PayloadRef string    `json:"payload_ref"`
}

type Attachment struct {
	FileName    string `json:"file_name"`
	MIMEType    string `json:"mime_type"`
	DownloadURL string `json:"download_url"`
	LocalPath   string `json:"local_path"`
}

type NormalizedRelease struct {
	Provider          string       `json:"provider"`
	ProviderReleaseID string       `json:"provider_release_id"`
	URL               string       `json:"url"`
	Title             string       `json:"title"`
	PublishedAt       time.Time    `json:"published_at"`
	EditedAt          time.Time    `json:"edited_at"`
	PostType          string       `json:"post_type"`
	VisibilityState   string       `json:"visibility_state"`
	TextHTML          string       `json:"text_html"`
	TextPlain         string       `json:"text_plain"`
	Tags              []string     `json:"tags"`
	Collections       []string     `json:"collections"`
	Attachments       []Attachment `json:"attachments"`
	CreatorID         string       `json:"creator_id"`
	CreatorName       string       `json:"creator_name"`
	SourceType        string       `json:"source_type"`
}

type TrackDecision struct {
	TrackKey           string          `json:"track_key"`
	TrackName          string          `json:"track_name"`
	SeriesID           string          `json:"series_id,omitempty"`
	RuleID             string          `json:"rule_id"`
	ReleaseRole        ReleaseRole     `json:"release_role"`
	ContentStrategy    ContentStrategy `json:"content_strategy"`
	OutputFormat       OutputFormat    `json:"output_format"`
	PrefaceMode        PrefaceMode     `json:"preface_mode"`
	CanonicalAuthor    string          `json:"canonical_author,omitempty"`
	AttachmentGlob     []string        `json:"attachment_glob"`
	AttachmentPriority []string        `json:"attachment_priority"`
	AnthologyMode      bool            `json:"anthology_mode"`
	Matched            bool            `json:"matched"`
}

type ArtifactPlan struct {
	ArtifactKind    string `json:"artifact_kind"`
	Filename        string `json:"filename"`
	MIMEType        string `json:"mime_type"`
	SHA256          string `json:"sha256"`
	SelectedPath    string `json:"selected_path"`
	SelectedContent []byte `json:"-"`
	MetadataJSON    []byte `json:"-"`
	NormalizedJSON  []byte `json:"-"`
	RawJSON         []byte `json:"-"`
}

type SyncItemPlan struct {
	SourceID          string          `json:"source_id"`
	ProviderReleaseID string          `json:"provider_release_id"`
	Title             string          `json:"title"`
	TrackKey          string          `json:"track_key"`
	ReleaseRole       ReleaseRole     `json:"release_role"`
	Strategy          ContentStrategy `json:"strategy"`
	OutputFormat      OutputFormat    `json:"output_format"`
	ArtifactKind      string          `json:"artifact_kind"`
	Filename          string          `json:"filename"`
	Action            string          `json:"action"`
}

type SyncResult struct {
	RunID                 string         `json:"run_id"`
	Discovered            int            `json:"discovered"`
	Changed               int            `json:"changed"`
	Unchanged             int            `json:"unchanged"`
	MaterializedArtifacts int            `json:"materialized_artifacts"`
	Plans                 []SyncItemPlan `json:"plans"`
}

type PublishCandidate struct {
	Source     Source            `json:"source"`
	Track      StoryTrack        `json:"track"`
	Release    Release           `json:"release"`
	Assignment ReleaseAssignment `json:"assignment"`
	Artifact   Artifact          `json:"artifact"`
}

type PublishItemResult struct {
	ArtifactID string `json:"artifact_id"`
	TargetID   string `json:"target_id"`
	TargetKind string `json:"target_kind"`
	TargetRef  string `json:"target_ref"`
	Action     string `json:"action"`
	Message    string `json:"message,omitempty"`
}

type PublishResult struct {
	RunID     string              `json:"run_id"`
	Published int                 `json:"published"`
	Skipped   int                 `json:"skipped"`
	Failed    int                 `json:"failed"`
	DryRun    bool                `json:"dry_run"`
	Artifacts []string            `json:"artifacts"`
	Items     []PublishItemResult `json:"items"`
}

type PublishRecordBundle struct {
	Record   PublishRecord `json:"record"`
	Artifact Artifact      `json:"artifact"`
	Release  Release       `json:"release"`
	Source   Source        `json:"source"`
	Track    StoryTrack    `json:"track"`
}

type Lease struct {
	Key       string    `json:"key"`
	Holder    string    `json:"holder"`
	ExpiresAt time.Time `json:"expires_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ReleaseBundle struct {
	Source     Source            `json:"source"`
	Release    Release           `json:"release"`
	Assignment ReleaseAssignment `json:"assignment"`
	Track      StoryTrack        `json:"track"`
	Artifacts  []Artifact        `json:"artifacts"`
}

type RunBundle struct {
	Run    RunRecord     `json:"run"`
	Events []EventRecord `json:"events"`
}
