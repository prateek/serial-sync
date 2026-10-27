package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
)

type captureSaveFailureRepo struct {
	store.Repository
	fail      bool
	attempted []domain.Artifact
}

func (r *captureSaveFailureRepo) SaveSyncSnapshot(ctx context.Context, snapshot store.SyncSnapshot) error {
	r.attempted = append(r.attempted, snapshot.Artifact)
	if r.fail {
		return errors.New("injected failure after capture installation")
	}
	return r.Repository.SaveSyncSnapshot(ctx, snapshot)
}

func TestIntakeRetriesReuseCaptureAfterCatalogFailure(t *testing.T) {
	for _, correction := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial capture", true: "correction"}[correction], func(t *testing.T) {
			ctx := context.Background()
			s, upstream := newReaderService(t)
			upstream.docs["alpha"] = upstream.docs["alpha"][:1]
			initial := 0
			if correction {
				if _, err := s.RunOnce(ctx, "", "", "initial"); err != nil {
					t.Fatal(err)
				}
				initial = 1
				upstream.docs["alpha"][0].Normalized.TextHTML += "<p>Correction.</p>"
				upstream.docs["alpha"][0].Normalized.TextPlain += " Correction."
			}
			if !correction {
				releases, err := s.Repo.ListReleases(ctx, "alpha")
				if err != nil || len(releases) != 0 {
					t.Fatalf("initial catalog already has releases: %+v %v", releases, err)
				}
				tracks, err := s.Repo.ListTracks(ctx, "alpha")
				if err != nil || len(tracks) != 0 {
					t.Fatalf("initial catalog already has tracks: %+v %v", tracks, err)
				}
			}
			repo := &captureSaveFailureRepo{Repository: s.Repo, fail: true}
			s.Repo = repo
			for attempt := 0; attempt < 2; attempt++ {
				if _, err := s.RunOnce(ctx, "", "", "failed save"); err == nil {
					t.Fatal("expected injected catalog failure")
				}
				entries, err := os.ReadDir(filepath.Join(s.Config.Runtime.ArtifactRoot, "captures"))
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != initial+1 {
					t.Fatalf("retry accumulated capture directories: got %d want %d", len(entries), initial+1)
				}
			}
			if !correction {
				releases, err := repo.ListReleases(ctx, "alpha")
				if err != nil || len(releases) != 0 {
					t.Fatalf("failed saves left a stored release: %+v %v", releases, err)
				}
				tracks, err := repo.ListTracks(ctx, "alpha")
				if err != nil || len(tracks) != 0 {
					t.Fatalf("failed saves left a stored track: %+v %v", tracks, err)
				}
			}
			if len(repo.attempted) != 2 || repo.attempted[0].ID != repo.attempted[1].ID {
				t.Fatalf("retry changed installed capture identity: %+v", repo.attempted)
			}
			repo.fail = false
			if _, err := s.RunOnce(ctx, "", "", "successful retry"); err != nil {
				t.Fatal(err)
			}
			got, err := repo.GetArtifact(ctx, repo.attempted[0].ID)
			if err != nil || got == nil || !got.IsCanonical {
				t.Fatalf("installed capture not adopted by catalog: %+v %v", got, err)
			}
		})
	}
}

func TestIntakeMaterializesChangedShapeWhenRepairingCurrentLegacyCapture(t *testing.T) {
	ctx := context.Background()
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	if _, err := s.RunOnce(ctx, "", "", "initial"); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.Repo.ListPublishCandidates(ctx, "alpha")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates: %+v %v", candidates, err)
	}
	c := candidates[0]
	legacy := c.Artifact
	legacy.ID = "legacy-current-shape"
	legacy.MIMEType = "application/octet-stream"
	legacy.ArtifactKind = "file"
	if err := s.Repo.SaveSyncSnapshot(ctx, store.SyncSnapshot{Source: c.Source, Track: c.Track, Release: c.Release, Assignment: c.Assignment, Artifact: legacy}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(legacy.StorageRef); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunOnce(ctx, "", "", "repair with current output shape"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Repo.GetCanonicalArtifactByReleaseID(ctx, c.Release.ID)
	if err != nil || got == nil || got.ID == legacy.ID || got.MIMEType != "text/html" || got.ArtifactKind != "html" {
		t.Fatalf("repair failed to materialize changed shape: %+v %v", got, err)
	}
}
