package sqlite_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

func TestReadOnlyCatalogSortsLargeLibraryWithoutWriting(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	writable, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writable.Close() })
	if err := writable.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	const count = 1200
	for i := count - 1; i >= 0; i-- {
		id := fmt.Sprintf("release-%04d", i)
		if err := writable.SaveSyncSnapshot(ctx, store.SyncSnapshot{
			Source: domain.Source{ID: "fictional-source", Enabled: true},
			Track:  domain.StoryTrack{ID: "fictional-track", SourceID: "fictional-source", TrackKey: "story"},
			Release: domain.Release{ID: id, SourceID: "fictional-source", ProviderReleaseID: id,
				Title: strings.Repeat("Synthetic title ", 300), PublishedAt: time.Unix(int64(i), 0)},
			Assignment: domain.ReleaseAssignment{ReleaseID: id, TrackID: "fictional-track"},
			Artifact:   domain.Artifact{ID: "artifact-" + id, ReleaseID: id, IsCanonical: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	readonly, err := sqlite.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readonly.Close() })
	for _, source := range []string{"", "fictional-source"} {
		rows, err := readonly.ListPublishCandidates(ctx, source)
		if err != nil {
			t.Fatalf("list candidates for %q: %v", source, err)
		}
		if len(rows) != count {
			t.Fatalf("got %d candidates, want %d", len(rows), count)
		}
		for i, row := range rows {
			if want := fmt.Sprintf("release-%04d", i); row.Release.ID != want {
				t.Fatalf("candidate %d = %q, want %q", i, row.Release.ID, want)
			}
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("catalog changed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("preview created files: %v, %v", entries, err)
	}
}
