package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

// failRepo proxies the sqlite store and lets the deciding pass be observed:
// how often discovery candidates are read, and whether candidate-list and
// enrichment-write failures stay advisory instead of aborting the sync.
type failRepo struct {
	inner                *sqlite.Store
	failCandidateList    bool
	failEnrichment       bool
	candidateListCalls   int
	enrichmentWriteCalls int
}

func (r *failRepo) GetReleaseEnrichment(ctx context.Context, a, b, c string) (*domain.ReleaseEnrichment, error) {
	return r.inner.GetReleaseEnrichment(ctx, a, b, c)
}

func (r *failRepo) SaveReleaseEnrichment(ctx context.Context, a, b string, enrichment domain.ReleaseEnrichment) error {
	r.enrichmentWriteCalls++
	if r.failEnrichment {
		return errors.New("injected enrichment write failure")
	}
	return r.inner.SaveReleaseEnrichment(ctx, a, b, enrichment)
}

func (r *failRepo) ListLabelObservations(ctx context.Context) ([]domain.LabelReference, error) {
	return r.inner.ListLabelObservations(ctx)
}

func (r *failRepo) ListDiscoveryCandidates(ctx context.Context, sourceID string) ([]domain.DiscoveryCandidate, error) {
	r.candidateListCalls++
	if r.failCandidateList {
		return nil, errors.New("injected candidate read failure")
	}
	return r.inner.ListDiscoveryCandidates(ctx, sourceID)
}

func (r *failRepo) SaveDiscoveryCandidates(ctx context.Context, candidates []domain.DiscoveryCandidate) error {
	return r.inner.SaveDiscoveryCandidates(ctx, candidates)
}

func (r *failRepo) DismissDiscoveryCandidate(ctx context.Context, id, reason string) error {
	return r.inner.DismissDiscoveryCandidate(ctx, id, reason)
}

func (r *failRepo) EnsureSchema(ctx context.Context) error {
	return r.inner.EnsureSchema(ctx)
}

func (r *failRepo) Close() error {
	return r.inner.Close()
}

func (r *failRepo) UpsertSource(ctx context.Context, source domain.Source) error {
	return r.inner.UpsertSource(ctx, source)
}

func (r *failRepo) GetSource(ctx context.Context, id string) (*domain.Source, error) {
	return r.inner.GetSource(ctx, id)
}

func (r *failRepo) GetTrackBySourceAndKey(ctx context.Context, sourceID, trackKey string) (*domain.StoryTrack, error) {
	return r.inner.GetTrackBySourceAndKey(ctx, sourceID, trackKey)
}

func (r *failRepo) GetTrack(ctx context.Context, id string) (*domain.StoryTrack, error) {
	return r.inner.GetTrack(ctx, id)
}

func (r *failRepo) ListTracks(ctx context.Context, sourceID string) ([]domain.StoryTrack, error) {
	return r.inner.ListTracks(ctx, sourceID)
}

func (r *failRepo) GetReleaseByProviderID(ctx context.Context, sourceID, providerReleaseID string) (*domain.Release, error) {
	return r.inner.GetReleaseByProviderID(ctx, sourceID, providerReleaseID)
}

func (r *failRepo) GetReleaseBundle(ctx context.Context, id string) (*domain.ReleaseBundle, error) {
	return r.inner.GetReleaseBundle(ctx, id)
}

func (r *failRepo) ListReleases(ctx context.Context, sourceID string) ([]domain.Release, error) {
	return r.inner.ListReleases(ctx, sourceID)
}

func (r *failRepo) GetCanonicalArtifactByReleaseID(ctx context.Context, releaseID string) (*domain.Artifact, error) {
	return r.inner.GetCanonicalArtifactByReleaseID(ctx, releaseID)
}

func (r *failRepo) GetArtifact(ctx context.Context, id string) (*domain.Artifact, error) {
	return r.inner.GetArtifact(ctx, id)
}

func (r *failRepo) SaveSyncSnapshot(ctx context.Context, snapshot store.SyncSnapshot) error {
	return r.inner.SaveSyncSnapshot(ctx, snapshot)
}

func (r *failRepo) ListVolumeEditions(ctx context.Context) ([]domain.VolumeEdition, error) {
	return r.inner.ListVolumeEditions(ctx)
}

func (r *failRepo) ReplaceVolumes(ctx context.Context, seriesID string, volumes []domain.VolumeEdition, deactivatedGroups []string) error {
	return r.inner.ReplaceVolumes(ctx, seriesID, volumes, deactivatedGroups)
}

func (r *failRepo) StartRun(ctx context.Context, run domain.RunRecord) error {
	return r.inner.StartRun(ctx, run)
}

func (r *failRepo) FinishRun(ctx context.Context, runID string, status domain.RunStatus, summary string) error {
	return r.inner.FinishRun(ctx, runID, status, summary)
}

func (r *failRepo) AddEvent(ctx context.Context, event domain.EventRecord) error {
	return r.inner.AddEvent(ctx, event)
}

func (r *failRepo) GetRunBundle(ctx context.Context, runID string) (*domain.RunBundle, error) {
	return r.inner.GetRunBundle(ctx, runID)
}

func (r *failRepo) ListRuns(ctx context.Context, limit int) ([]domain.RunRecord, error) {
	return r.inner.ListRuns(ctx, limit)
}

func (r *failRepo) ListPublishCandidates(ctx context.Context, sourceID string) ([]domain.PublishCandidate, error) {
	return r.inner.ListPublishCandidates(ctx, sourceID)
}

func (r *failRepo) HasSuccessfulPublish(ctx context.Context, artifactID, targetID, publishHash string) (bool, error) {
	return r.inner.HasSuccessfulPublish(ctx, artifactID, targetID, publishHash)
}

func (r *failRepo) UpsertPublishRecord(ctx context.Context, record domain.PublishRecord) error {
	return r.inner.UpsertPublishRecord(ctx, record)
}

func (r *failRepo) ListPublishRecords(ctx context.Context, sourceID, targetID string) ([]domain.PublishRecordBundle, error) {
	return r.inner.ListPublishRecords(ctx, sourceID, targetID)
}

func (r *failRepo) GetPublishRecord(ctx context.Context, id string) (*domain.PublishRecordBundle, error) {
	return r.inner.GetPublishRecord(ctx, id)
}

func (r *failRepo) GetPendingPublish(ctx context.Context, targetID string) (*domain.PendingPublish, error) {
	return r.inner.GetPendingPublish(ctx, targetID)
}

func (r *failRepo) SavePendingPublish(ctx context.Context, pending domain.PendingPublish) error {
	return r.inner.SavePendingPublish(ctx, pending)
}

func (r *failRepo) CompletePendingPublish(ctx context.Context, id string) error {
	return r.inner.CompletePendingPublish(ctx, id)
}

func (r *failRepo) AcquireLease(ctx context.Context, key, holder string, ttl time.Duration) (bool, error) {
	return r.inner.AcquireLease(ctx, key, holder, ttl)
}

func (r *failRepo) ReleaseLease(ctx context.Context, key, holder string) error {
	return r.inner.ReleaseLease(ctx, key, holder)
}

func newReaderServiceWithRepo(t *testing.T) (*app.Service, *stubRuleAuthoringProvider, *sqlite.Store) {
	t.Helper()
	upstream := newStubRuleAuthoringProvider()
	s, repo := newRuleAuthoringServiceWithRepo(t, upstream)
	s.Config.Sources = []config.SourceConfig{upstream.suggestions[0].Source}
	s.Config.Publishers = []config.PublisherConfig{{
		ID: "library", Kind: "filesystem", Path: filepath.Join(s.Roots.StateDir, "library"), Enabled: true,
	}}
	s.Config.Series = []config.SeriesConfig{{
		ID: "alpha-saga", Title: "Alpha Saga", Authors: []string{"Alpha Author"},
		Output: config.SeriesOutputConfig{Format: "preserve", PrefaceMode: "none"},
		Inputs: []config.SeriesInputConfig{{
			Source: "alpha", Priority: 10, MatchType: "title_regex", MatchValue: "^Alpha Saga",
			ReleaseRole: "chapter", ContentStrategy: "text_post",
		}},
	}}

	return s, upstream, repo
}

func TestCandidateFingerprintStableAfterAttachmentCapture(t *testing.T) {
	s, upstream, _ := newReaderServiceWithRepo(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	path := filepath.Join(t.TempDir(), "chapter.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\nFictional captured attachment\n%%EOF"), 0o600); err != nil {
		t.Fatal(err)
	}
	upstream.docs["alpha"][0].Normalized.Attachments = []domain.Attachment{{FileName: filepath.Base(path), LocalPath: path, MIMEType: "application/pdf"}}
	s.Config.Series[0].Inputs[0].MatchType = "fallback"
	s.Config.Series[0].Inputs[0].HoldCandidates = true
	s.Config.Series[0].Inputs[0].ContentStrategy = "attachment_only"

	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	first, err := s.Repo.ListDiscoveryCandidates(context.Background(), "alpha")
	if err != nil || len(first) != 1 {
		t.Fatalf("held attachment release should raise one candidate: %+v %v", first, err)
	}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	second, err := s.Repo.ListDiscoveryCandidates(context.Background(), "alpha")
	if err != nil || len(second) != 1 {
		t.Fatalf("second sync should keep one candidate: %+v %v", second, err)
	}
	if second[0].ID != first[0].ID {
		t.Fatalf("candidate identity changed: %s -> %s", first[0].ID, second[0].ID)
	}
	if second[0].Changed {
		t.Fatalf("candidate reported as changed once capture filled the attachment hash: %+v", second[0])
	}
	if second[0].EvidenceFingerprint != first[0].EvidenceFingerprint {
		t.Fatalf("evidence fingerprint changed after capture: %s -> %s", first[0].EvidenceFingerprint, second[0].EvidenceFingerprint)
	}
	// A dismissal recorded against the stable fingerprint must still hold on
	// the next sync.
	if err := s.Repo.DismissDiscoveryCandidate(context.Background(), first[0].ID, "intentional repost"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	third, err := s.Repo.ListDiscoveryCandidates(context.Background(), "alpha")
	if err != nil || len(third) != 1 || third[0].Status != "resolved" {
		t.Fatalf("dismissal must survive the stable fingerprint: %+v %v", third, err)
	}
}

func TestDiscoveryCandidateReadFailureKeepsTheSourceSynced(t *testing.T) {
	s, _, repo := newReaderServiceWithRepo(t)
	wrapped := &failRepo{inner: repo, failCandidateList: true}
	s.Repo = wrapped
	result, err := s.RunOnce(context.Background(), "", "", "run")
	if err != nil {
		t.Fatalf("candidate read failure must not abort the sync: %v", err)
	}
	if len(result.Sync.DiscoveryNotices) == 0 {
		t.Fatalf("candidate read failure should surface as a discovery notice: %+v", result.Sync)
	}
	releases, err := s.Repo.ListReleases(context.Background(), "alpha")
	if err != nil || len(releases) != 3 {
		t.Fatalf("releases must be synced despite the candidate read failure: %+v %v", releases, err)
	}
}

func TestEnrichmentWriteFailureKeepsTheSourceSynced(t *testing.T) {
	s, upstream, repo := newReaderServiceWithRepo(t)
	wrapped := &failRepo{inner: repo, failEnrichment: true}
	s.Repo = wrapped
	upstream.docs["alpha"][0].Normalized.Enrichment = &domain.ReleaseEnrichment{NormalizerVersion: 1}
	result, err := s.RunOnce(context.Background(), "", "", "run")
	if err != nil {
		t.Fatalf("enrichment write failure must not abort the sync: %v", err)
	}
	if len(result.Sync.DiscoveryNotices) == 0 {
		t.Fatalf("enrichment write failure should surface as a discovery notice: %+v", result.Sync)
	}
	releases, err := s.Repo.ListReleases(context.Background(), "alpha")
	if err != nil || len(releases) != 3 {
		t.Fatalf("releases must be synced despite the enrichment write failure: %+v %v", releases, err)
	}
}

func TestSyncDecidesEachSourceOnce(t *testing.T) {
	s, upstream, repo := newReaderServiceWithRepo(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	wrapped := &failRepo{inner: repo}
	s.Repo = wrapped
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	if wrapped.candidateListCalls != 1 {
		t.Fatalf("expected exactly one candidate read for a synced source, saw %d", wrapped.candidateListCalls)
	}
}

func TestPublishReusesSyncDecisionsAndSkipsUnbundledSources(t *testing.T) {
	s, upstream, repo := newReaderServiceWithRepo(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	// A second source with no volume-bundled series must be decided only by
	// its sync pass; the publish must not re-decide either source.
	s.Config.Sources = append(s.Config.Sources, upstream.suggestions[1].Source)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 50}

	wrapped := &failRepo{inner: repo}
	s.Repo = wrapped
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	if wrapped.candidateListCalls != 2 {
		t.Fatalf("one candidate read per synced source and none at publish time, saw %d", wrapped.candidateListCalls)
	}
}
