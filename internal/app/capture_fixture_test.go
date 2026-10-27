package app_test

import (
	"context"
	"testing"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
)

func canonicalReadingPaths(t *testing.T, s *app.Service) []string {
	t.Helper()
	candidates, err := s.Repo.ListPublishCandidates(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, candidate := range candidates {
		paths = append(paths, candidate.Artifact.StorageRef)
	}
	return paths
}

func canonicalReadingFilename(t *testing.T, s *app.Service) string {
	t.Helper()
	candidates, err := s.Repo.ListPublishCandidates(context.Background(), "")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("reading copies: %+v %v", candidates, err)
	}
	return candidates[0].Artifact.Filename
}

func installSidecarFixture(t *testing.T, s *app.Service, candidate domain.PublishCandidate, metadata []byte) {
	t.Helper()
	old := candidate.Artifact
	plan := domain.ArtifactPlan{
		Filename: old.Filename, ArtifactKind: old.ArtifactKind, MIMEType: old.MIMEType, SHA256: old.SHA256,
		SelectedContent: mustReadFile(t, old.StorageRef), MetadataJSON: metadata,
		NormalizedJSON: mustReadFile(t, old.NormalizedRef), RawJSON: mustReadFile(t, old.RawRef),
	}
	art, err := s.Files.Materialize(context.Background(), candidate.Source, candidate.Track, candidate.Release, plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Repo.SaveSyncSnapshot(context.Background(), store.SyncSnapshot{Source: candidate.Source, Track: candidate.Track, Release: candidate.Release, Assignment: candidate.Assignment, Artifact: art}); err != nil {
		t.Fatal(err)
	}
}
