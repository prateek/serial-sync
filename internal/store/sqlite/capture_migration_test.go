package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
)

func TestCaptureMigrationPreservesLegacyReferences(t *testing.T) {
	for _, unique := range []string{"release_id, sha256, artifact_kind", "release_id, sha256, artifact_kind, filename, track_id"} {
		t.Run(unique, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "catalog.db")
			repo, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			legacySchema := strings.Replace(schemaSQL, "raw_ref TEXT NOT NULL\n);", "raw_ref TEXT NOT NULL,\n UNIQUE("+unique+")\n);", 1)
			if legacySchema == schemaSQL {
				t.Fatal("legacy fixture did not add constraint")
			}
			if _, err := repo.db.ExecContext(ctx, legacySchema); err != nil {
				t.Fatal(err)
			}
			first := domain.Artifact{ID: "legacy-A", ReleaseID: "release", TrackID: "track", Filename: "story.html", SHA256: "same-reading-bytes", MetadataRef: "legacy.metadata.json", NormalizedRef: "legacy.normalized.json", RawRef: "legacy.raw.json", StorageRef: "legacy.html", IsCanonical: true}
			snapshot := store.SyncSnapshot{Source: domain.Source{ID: "source"}, Track: domain.StoryTrack{ID: "track", SourceID: "source", TrackKey: "story"}, Release: domain.Release{ID: "release", SourceID: "source", ProviderReleaseID: "1"}, Assignment: domain.ReleaseAssignment{ReleaseID: "release"}, Artifact: first}
			if err := repo.SaveSyncSnapshot(ctx, snapshot); err != nil {
				t.Fatal(err)
			}
			if err := repo.UpsertPublishRecord(ctx, domain.PublishRecord{ID: "receipt", ArtifactID: first.ID, TargetID: "library", Status: domain.PublishStatusPublished}); err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			repo, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			for i := 0; i < 2; i++ {
				if err := repo.EnsureSchema(ctx); err != nil {
					t.Fatal(err)
				}
			}
			got, err := repo.GetArtifact(ctx, first.ID)
			if err != nil || got == nil || !reflect.DeepEqual(*got, first) {
				t.Fatalf("migration changed legacy capture: %+v %v", got, err)
			}
			snapshot.Artifact.ID = "capture-B"
			snapshot.Artifact.MetadataRef = "new.metadata.json"
			if err := repo.SaveSyncSnapshot(ctx, snapshot); err != nil {
				t.Fatal(err)
			}
			bundle, err := repo.GetReleaseBundle(ctx, "release")
			if err != nil || len(bundle.Artifacts) != 2 {
				t.Fatalf("new capture was collapsed: %+v %v", bundle, err)
			}
			records, err := repo.ListPublishRecords(ctx, "source", "library")
			if err != nil || len(records) != 1 || records[0].Artifact.MetadataRef != first.MetadataRef || records[0].Artifact.ID != first.ID {
				t.Fatalf("legacy receipt changed: %+v %v", records, err)
			}
		})
	}
}

func TestVolumeArtifactReferencesFollowReplacedEdition(t *testing.T) {
	for _, rename := range []bool{false, true} {
		t.Run(map[bool]string{false: "same filename", true: "renamed"}[rename], func(t *testing.T) {
			ctx := context.Background()
			repo, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if err := repo.EnsureSchema(ctx); err != nil {
				t.Fatal(err)
			}
			first := domain.VolumeEdition{ID: "edition-A", SeriesID: "series", SourceID: "source", TrackID: "track", GroupID: "group", Active: true,
				Artifact: domain.Artifact{ID: "volart_same-bytes", TrackID: "track", ArtifactKind: "epub", Filename: "volume.epub", SHA256: "same-bytes", StorageRef: "edition-A/volume.epub", MetadataRef: "edition-A/volume.json"}}
			if err := repo.ReplaceVolumes(ctx, "series", []domain.VolumeEdition{first}, nil); err != nil {
				t.Fatal(err)
			}
			second := first
			second.ID = "edition-B"
			second.Artifact.StorageRef = "edition-B/volume.epub"
			second.Artifact.MetadataRef = "edition-B/volume.json"
			if rename {
				second.Artifact.Filename = "renamed.epub"
			}
			if err := repo.ReplaceVolumes(ctx, "series", []domain.VolumeEdition{second}, nil); err != nil {
				t.Fatal(err)
			}
			volumes, err := repo.ListVolumeEditions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var current *domain.VolumeEdition
			for i := range volumes {
				if volumes[i].Active {
					current = &volumes[i]
				}
			}
			if current == nil || current.ID != second.ID || current.Artifact.StorageRef != second.Artifact.StorageRef || current.Artifact.MetadataRef != second.Artifact.MetadataRef || current.Artifact.Filename != second.Artifact.Filename {
				t.Fatalf("active volume retains stale artifact references: %+v", current)
			}
		})
	}
}

func TestSnapshotRejectsArtifactTrackRebinding(t *testing.T) {
	ctx := context.Background()
	repo, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := store.SyncSnapshot{Source: domain.Source{ID: "source"}, Track: domain.StoryTrack{ID: "stored-track", SourceID: "source", TrackKey: "story"}, Release: domain.Release{ID: "release", SourceID: "source", ProviderReleaseID: "1"}, Assignment: domain.ReleaseAssignment{ReleaseID: "release"}, Artifact: domain.Artifact{ID: "original", ReleaseID: "release", TrackID: "stored-track", IsCanonical: true}}
	if err := repo.SaveSyncSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Track.ID = "provisional-track"
	snapshot.Artifact.ID = "new-capture"
	snapshot.Artifact.TrackID = "provisional-track"
	if err := repo.SaveSyncSnapshot(ctx, snapshot); err == nil || !strings.Contains(err.Error(), "does not match stored track") {
		t.Fatalf("capture silently rebound to another track: %v", err)
	}
	current, err := repo.GetCanonicalArtifactByReleaseID(ctx, "release")
	if err != nil || current == nil || current.ID != "original" {
		t.Fatalf("rejected save changed canonical capture: %+v %v", current, err)
	}
}
