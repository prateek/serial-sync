package provider

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

type ReleaseDocument struct {
	Normalized domain.NormalizedRelease
	RawJSON    json.RawMessage
}

type ProgressEvent struct {
	Level      string
	Component  string
	Message    string
	EntityKind string
	EntityID   string
	Payload    any
}

type ProgressReporter interface {
	ReportProgress(context.Context, ProgressEvent)
}

type ProgressReporterFunc func(context.Context, ProgressEvent)

func (fn ProgressReporterFunc) ReportProgress(ctx context.Context, event ProgressEvent) {
	if fn != nil {
		fn(ctx, event)
	}
}

type progressReporterKey struct{}

func WithProgress(ctx context.Context, reporter ProgressReporter) context.Context {
	if reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, progressReporterKey{}, reporter)
}

func ReportProgress(ctx context.Context, event ProgressEvent) {
	reporter, _ := ctx.Value(progressReporterKey{}).(ProgressReporter)
	if reporter == nil {
		return
	}
	reporter.ReportProgress(ctx, event)
}

type ListResult struct {
	Documents  []ReleaseDocument
	AuthState  domain.AuthState
	SyncCursor string
}

type AuthBootstrapResult struct {
	State  domain.AuthState
	Action string
}

type DiscoverOptions struct {
	SampleLimit       int      `json:"sample_limit,omitempty"`
	FullHistory       bool     `json:"full_history,omitempty"`
	MembershipFilter  string   `json:"membership_filter,omitempty"`
	CreatorFilters    []string `json:"creator_filters,omitempty"`
	IncludeConfigured bool     `json:"include_configured,omitempty"`
	ShowPosts         bool     `json:"show_posts,omitempty"`
	MetadataOnly      bool     `json:"metadata_only,omitempty"`
}

type DiscoveryPreviewGroup struct {
	TrackKey        string                 `json:"track_key"`
	TrackName       string                 `json:"track_name"`
	MatchType       string                 `json:"match_type"`
	MatchValue      string                 `json:"match_value,omitempty"`
	ContentStrategy domain.ContentStrategy `json:"content_strategy"`
	Total           int                    `json:"total"`
	Materializable  int                    `json:"materializable"`
	SampleTitles    []string               `json:"sample_titles,omitempty"`
}

type DiscoveryPreviewPost struct {
	ProviderReleaseID string                 `json:"provider_release_id"`
	Title             string                 `json:"title"`
	PublishedAt       time.Time              `json:"published_at"`
	Tags              []string               `json:"tags,omitempty"`
	Collections       []string               `json:"collections,omitempty"`
	Attachments       []string               `json:"attachments,omitempty"`
	TrackKey          string                 `json:"track_key"`
	TrackName         string                 `json:"track_name"`
	MatchType         string                 `json:"match_type"`
	MatchValue        string                 `json:"match_value,omitempty"`
	ContentStrategy   domain.ContentStrategy `json:"content_strategy"`
	Materializable    bool                   `json:"materializable"`
}

type DiscoveryPreview struct {
	SampledPosts   int                     `json:"sampled_posts"`
	Materializable int                     `json:"materializable"`
	FallbackPosts  int                     `json:"fallback_posts"`
	Groups         []DiscoveryPreviewGroup `json:"groups,omitempty"`
	Posts          []DiscoveryPreviewPost  `json:"posts,omitempty"`
}

type SourceSuggestion struct {
	Source            config.SourceConfig `json:"source"`
	CreatorName       string              `json:"creator_name"`
	CreatorHandle     string              `json:"creator_handle"`
	MembershipKind    string              `json:"membership_kind"`
	AlreadyConfigured bool                `json:"already_configured"`
	ExistingSourceID  string              `json:"existing_source_id,omitempty"`
	SampledPosts      int                 `json:"sampled_posts,omitempty"`
	SampleTitles      []string            `json:"sample_titles,omitempty"`
	SampleTags        []string            `json:"sample_tags,omitempty"`
	SampleCollections []string            `json:"sample_collections,omitempty"`
	SuggestedRules    []config.RuleConfig `json:"suggested_rules,omitempty"`
	Preview           DiscoveryPreview    `json:"preview,omitempty"`
}

type DiscoverResult struct {
	Provider    string             `json:"provider"`
	AuthState   domain.AuthState   `json:"auth_state"`
	Suggestions []SourceSuggestion `json:"suggestions"`
}

type Client interface {
	Name() string
	ValidateSource(source config.SourceConfig) error
	ValidateSession(ctx context.Context, auth config.AuthProfile, source config.SourceConfig) (domain.AuthState, error)
	BootstrapAuth(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, force bool) (AuthBootstrapResult, error)
	DiscoverSources(ctx context.Context, auth config.AuthProfile, existingSources []config.SourceConfig, options DiscoverOptions) (DiscoverResult, error)
	ListReleases(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, storedSource *domain.Source) (ListResult, error)
	PrepareRelease(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, doc ReleaseDocument, decision domain.TrackDecision) (ReleaseDocument, domain.AuthState, error)
}

type Registry struct {
	clients map[string]Client
}

func NewRegistry(clients ...Client) *Registry {
	registry := &Registry{clients: map[string]Client{}}
	for _, client := range clients {
		registry.clients[client.Name()] = client
	}
	return registry
}

func (r *Registry) Get(name string) (Client, bool) {
	client, ok := r.clients[name]
	return client, ok
}

func SortReleaseDocuments(items []ReleaseDocument) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Normalized.PublishedAt.After(items[j].Normalized.PublishedAt)
	})
}
