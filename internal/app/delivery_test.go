package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/observe"
	"github.com/prateek/serial-sync/internal/publish"
	"github.com/prateek/serial-sync/internal/publish/publishtest"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

func deliveryFixture(t *testing.T) (*Service, *sqlite.Store) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{Runtime: config.RuntimeConfig{
		LogRoot: filepath.Join(tmp, "logs"), StoreDriver: "sqlite", StoreDSN: filepath.Join(tmp, "state.db"),
		ArtifactRoot: filepath.Join(tmp, "artifacts"), SupportRoot: filepath.Join(tmp, "support"),
	}}
	roots := config.Roots{ConfigDir: filepath.Join(tmp, "config"), StateDir: tmp, CacheDir: filepath.Join(tmp, "cache"), RuntimeDir: filepath.Join(tmp, "runtime")}
	if err := config.EnsureDirs(roots, cfg); err != nil {
		t.Fatal(err)
	}
	repo, err := sqlite.Open(cfg.Runtime.StoreDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Config: cfg, Roots: roots, ConfigPath: filepath.Join(tmp, "config.toml"), Repo: repo}
	return svc, repo
}

func deliveryCandidate(name, sha string) domain.PublishCandidate {
	return domain.PublishCandidate{
		Source:     domain.Source{ID: "fictional"},
		Track:      domain.StoryTrack{ID: "fictional/tide", SourceID: "fictional", TrackKey: "tide", TrackName: "Tide"},
		Release:    domain.Release{ID: "rel_" + name, SourceID: "fictional", ProviderReleaseID: name, Title: name},
		Assignment: domain.ReleaseAssignment{ReleaseID: "rel_" + name, TrackID: "fictional/tide", ReleaseRole: domain.ReleaseRoleChapter},
		Artifact:   domain.Artifact{ID: "art_" + name, ReleaseID: "rel_" + name, Filename: name + ".epub", MIMEType: "application/epub+zip", SHA256: sha, StorageRef: "/unused", State: domain.ArtifactStateMaterialized},
	}
}

func startDeliveryRecorder(t *testing.T, svc *Service) *observe.Recorder {
	t.Helper()
	recorder, err := observe.Start(context.Background(), svc.Repo, "test delivery", "", false, svc.observeOptions())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Finish(context.Background(), domain.RunStatusSucceeded, "done") })
	return recorder
}

func memoryTarget(t *testing.T) *publishtest.Target {
	t.Helper()
	return &publishtest.Target{ID: "memory", Caps: publish.Capabilities{PendingRecord: true, Retires: true}}
}

func TestExecuteDeliveryOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		candidates                             []domain.PublishCandidate
		records                                []domain.PublishRecordBundle
		identityErr                            map[string]error
		deliverErr                             map[string]error
		retireErr                              map[string]error
		pending                                bool
		wantPublished, wantFailed, wantSkipped int
		wantItemActions                        []string
		wantDeliveries                         []string
		wantRetirements                        []string
		wantPendingAfter                       bool
	}{
		{
			name: "fresh deliveries succeed and clear the pending plan",
			candidates: []domain.PublishCandidate{
				deliveryCandidate("alpha", strings.Repeat("a", 64)),
				deliveryCandidate("beta", strings.Repeat("b", 64)),
			},
			wantPublished: 2, wantItemActions: []string{"published", "published"}, wantDeliveries: []string{"art_alpha", "art_beta"},
		},
		{
			name:          "a failed delivery keeps the pending plan for the retry",
			candidates:    []domain.PublishCandidate{deliveryCandidate("alpha", strings.Repeat("a", 64)), deliveryCandidate("beta", strings.Repeat("b", 64))},
			deliverErr:    map[string]error{"art_beta": os.ErrPermission},
			pending:       true,
			wantPublished: 1, wantFailed: 1, wantItemActions: []string{"published", "failed"}, wantDeliveries: []string{"art_alpha"}, wantPendingAfter: true,
		},
		{
			name:        "a plan-time block fails the attempt and retains the pending plan",
			candidates:  []domain.PublishCandidate{deliveryCandidate("alpha", strings.Repeat("a", 64))},
			identityErr: map[string]error{"art_alpha": os.ErrExist},
			pending:     true,
			wantFailed:  1, wantItemActions: []string{"blocked"}, wantPendingAfter: true,
		},
		{
			name:       "an unchanged candidate skips",
			candidates: []domain.PublishCandidate{deliveryCandidate("alpha", strings.Repeat("a", 64))},
			records: func() []domain.PublishRecordBundle {
				c := deliveryCandidate("alpha", strings.Repeat("a", 64))
				id := publish.DeliveryIdentity{Ref: "memory://" + c.Artifact.Filename, PublishHash: publish.PublishHash("memory", c.Artifact.SHA256, "memory://"+c.Artifact.Filename)}
				return []domain.PublishRecordBundle{{
					Record:   domain.PublishRecord{ID: "pub_live", ArtifactID: c.Artifact.ID, TargetID: "memory", TargetKind: "memory", TargetRef: id.Ref, Filename: c.Artifact.Filename, PublishHash: id.PublishHash, Status: domain.PublishStatusPublished},
					Artifact: c.Artifact, Release: c.Release,
				}}
			}(),
			wantSkipped: 1, wantItemActions: []string{"skipped"},
		},
		{
			name: "a failed retirement fails the attempt and keeps the pending plan",
			candidates: []domain.PublishCandidate{
				deliveryCandidate("renamed", strings.Repeat("c", 64)),
			},
			records: func() []domain.PublishRecordBundle {
				old := deliveryCandidate("renamed", strings.Repeat("c", 64))
				old.Artifact.Filename = "stale-name.epub"
				return []domain.PublishRecordBundle{{
					Record:   domain.PublishRecord{ID: "pub_old", ArtifactID: old.Artifact.ID, TargetID: "memory", TargetKind: "memory", TargetRef: "memory://stale-name.epub", Filename: old.Artifact.Filename, PublishHash: "hash_old", Status: domain.PublishStatusPublished},
					Artifact: old.Artifact, Release: old.Release,
				}}
			}(),
			retireErr:     map[string]error{"pub_old": os.ErrPermission},
			pending:       true,
			wantPublished: 1, wantFailed: 1, wantItemActions: []string{"published", "failed"}, wantDeliveries: []string{"art_renamed"}, wantPendingAfter: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := deliveryFixture(t)
			recorder := startDeliveryRecorder(t, svc)
			target := memoryTarget(t)
			target.IdentityErr, target.DeliverErr, target.RetireErr = tc.identityErr, tc.deliverErr, tc.retireErr
			if !tc.pending {
				target.Caps = publish.Capabilities{Retires: true}
			}
			cfg := config.PublisherConfig{ID: "memory", Kind: "memory", Enabled: true}
			outcome, err := svc.executeDelivery(context.Background(), recorder, deliveryScope{}, target, cfg, tc.candidates, tc.records, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Published != tc.wantPublished || outcome.Failed != tc.wantFailed || outcome.Skipped != tc.wantSkipped {
				t.Fatalf("counts = published %d failed %d skipped %d, want %d/%d/%d (items %+v)", outcome.Published, outcome.Failed, outcome.Skipped, tc.wantPublished, tc.wantFailed, tc.wantSkipped, outcome.Items)
			}
			if len(outcome.Items) != len(tc.wantItemActions) {
				t.Fatalf("items = %+v, want actions %v", outcome.Items, tc.wantItemActions)
			}
			for i, want := range tc.wantItemActions {
				if outcome.Items[i].Action != want {
					t.Fatalf("item %d action = %q, want %q (%+v)", i, outcome.Items[i].Action, want, outcome.Items[i])
				}
			}
			var delivered []string
			for _, candidate := range target.Deliveries {
				delivered = append(delivered, candidate.Artifact.ID)
			}
			if strings.Join(delivered, ",") != strings.Join(tc.wantDeliveries, ",") {
				t.Fatalf("deliveries = %v, want %v", delivered, tc.wantDeliveries)
			}
			if strings.Join(target.Retirements, ",") != strings.Join(tc.wantRetirements, ",") {
				t.Fatalf("retirements = %v, want %v", target.Retirements, tc.wantRetirements)
			}
			pending, err := repo.GetPendingPublish(context.Background(), cfg.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (pending != nil) != tc.wantPendingAfter {
				t.Fatalf("pending retained = %v, want %v", pending != nil, tc.wantPendingAfter)
			}
		})
	}
}
