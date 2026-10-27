package store

import (
	"context"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

type SyncSnapshot struct {
	Source     domain.Source
	Track      domain.StoryTrack
	Release    domain.Release
	Assignment domain.ReleaseAssignment
	Artifact   domain.Artifact
}

// CatalogReader reads captured catalog and library facts without mutation.
type CatalogReader interface {
	GetReleaseEnrichment(context.Context, string, string, string) (*domain.ReleaseEnrichment, error)
	ListLabelObservations(context.Context) ([]domain.LabelReference, error)
	ListDiscoveryCandidates(context.Context, string) ([]domain.DiscoveryCandidate, error)
	GetSource(context.Context, string) (*domain.Source, error)
	GetTrack(context.Context, string) (*domain.StoryTrack, error)
	GetTrackBySourceAndKey(context.Context, string, string) (*domain.StoryTrack, error)
	GetReleaseBundle(context.Context, string) (*domain.ReleaseBundle, error)
	ListReleases(context.Context, string) ([]domain.Release, error)
	ListPublishCandidates(context.Context, string) ([]domain.PublishCandidate, error)
	ListVolumeEditions(context.Context) ([]domain.VolumeEdition, error)
	ListPublishRecords(context.Context, string, string) ([]domain.PublishRecordBundle, error)
	GetPendingPublish(context.Context, string) (*domain.PendingPublish, error)
}

type Repository interface {
	CatalogReader
	SaveReleaseEnrichment(context.Context, string, string, domain.ReleaseEnrichment) error
	SaveDiscoveryCandidates(ctx context.Context, candidates []domain.DiscoveryCandidate) error
	DismissDiscoveryCandidate(ctx context.Context, id, reason string) error
	EnsureSchema(ctx context.Context) error
	Close() error

	UpsertSource(ctx context.Context, source domain.Source) error

	ListTracks(ctx context.Context, sourceID string) ([]domain.StoryTrack, error)

	GetReleaseByProviderID(ctx context.Context, sourceID, providerReleaseID string) (*domain.Release, error)

	GetCanonicalArtifactByReleaseID(ctx context.Context, releaseID string) (*domain.Artifact, error)
	GetArtifact(ctx context.Context, id string) (*domain.Artifact, error)
	SaveSyncSnapshot(ctx context.Context, snapshot SyncSnapshot) error
	ReplaceVolumes(ctx context.Context, seriesID string, volumes []domain.VolumeEdition, deactivatedGroups []string) error

	StartRun(ctx context.Context, run domain.RunRecord) error
	FinishRun(ctx context.Context, runID string, status domain.RunStatus, summary string) error
	AddEvent(ctx context.Context, event domain.EventRecord) error
	GetRunBundle(ctx context.Context, runID string) (*domain.RunBundle, error)
	ListRuns(ctx context.Context, limit int) ([]domain.RunRecord, error)

	HasSuccessfulPublish(ctx context.Context, artifactID, targetID, publishHash string) (bool, error)
	UpsertPublishRecord(ctx context.Context, record domain.PublishRecord) error
	GetPublishRecord(ctx context.Context, id string) (*domain.PublishRecordBundle, error)
	SavePendingPublish(ctx context.Context, pending domain.PendingPublish) error
	CompletePendingPublish(ctx context.Context, id string) error

	AcquireLease(ctx context.Context, key, holder string, ttl time.Duration) (bool, error)
	ReleaseLease(ctx context.Context, key, holder string) error
}
