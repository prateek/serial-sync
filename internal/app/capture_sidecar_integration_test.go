//go:build linux || darwin

package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestInspectSidecarPreservesVerifiedCurrentnessWithoutReadingCopy(t *testing.T) {
	source := domain.Source{ID: "source"}
	track := domain.StoryTrack{ID: "track", TrackKey: "story", TrackName: "Story"}
	release := domain.Release{ID: "release", ProviderReleaseID: "1", Title: "Chapter 1", ContentHash: "content"}
	normalized := domain.NormalizedRelease{ProviderReleaseID: "1", Title: release.Title, TextHTML: "<p>Story.</p>"}
	decision := domain.TrackDecision{ContentStrategy: domain.ContentStrategyTextPost, OutputFormat: domain.OutputFormatPreserve}
	selected := classify.SelectContent(normalized, decision)
	decision.SelectedContent = &selected
	files := artifact.New(t.TempDir())
	plan, err := files.Plan(context.Background(), source, track, release, normalized, decision, nil)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := files.Materialize(context.Background(), source, track, release, plan)
	if err != nil {
		t.Fatal(err)
	}
	readingPath, readingOpens := volumeReadSentinel(t)
	captured.StorageRef = readingPath
	inspect := func() artifact.StoredState {
		t.Helper()
		done := make(chan artifact.StoredState, 1)
		go func() { done <- artifact.InspectSidecar(captured) }()
		select {
		case state := <-done:
			if readingOpens.Load() != 0 {
				t.Fatal("sidecar inspection opened the reading copy")
			}
			return state
		case <-time.After(5 * time.Second):
			t.Fatal("sidecar inspection blocked on reading-copy bytes")
			return artifact.StoredState{}
		}
	}
	state := inspect()
	if state.Blocked || !state.IsCurrent(release, track, decision) {
		t.Fatalf("verified sidecar lost currentness: %+v", state)
	}
	if err := os.WriteFile(captured.MetadataRef, []byte(`{"output_version":2}`), 0644); err != nil {
		t.Fatal(err)
	}
	state = inspect()
	if !state.Blocked || state.IsCurrent(release, track, decision) {
		t.Fatalf("corrupt sidecar accepted as verified current state: %+v", state)
	}
}
