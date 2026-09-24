package publish_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/publish"
)

func dropTarget(t *testing.T, root string) publish.Target {
	t.Helper()
	target, err := publish.TargetFor(config.PublisherConfig{ID: "bookorbit", Kind: "drop", Path: root, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// Records written by an earlier publisher of the same ID carry that
// publisher's ref and publish hash; hand-off identity must not depend on them.
func TestDropTargetKeysHandoffBySourceRelease(t *testing.T) {
	root := t.TempDir()
	target := dropTarget(t, filepath.Join(root, "library"))
	candidate, _ := writeTestCandidate(t, filepath.Join(root, "storage"), "Tide Chapter 1")
	migrated := domain.PublishRecordBundle{
		Record:   domain.PublishRecord{ID: "pub_exec", TargetID: "bookorbit", TargetKind: "exec", TargetRef: `"python3" "hook.py"`, PublishHash: "exec-hash", Status: domain.PublishStatusPublished},
		Artifact: candidate.Artifact, Release: candidate.Release, Source: candidate.Source, Track: candidate.Track,
	}

	revised := candidate
	revised.Artifact.ID, revised.Artifact.SHA256 = "art_2", strings.Repeat("f", 64)
	other := candidate
	other.Release.ProviderReleaseID = "p2"
	otherSource := candidate
	otherSource.Source.ID = "elsewhere"
	superseded := migrated
	superseded.Record.Status = domain.PublishStatusSuperseded

	for _, tc := range []struct {
		name      string
		candidate domain.PublishCandidate
		records   []domain.PublishRecordBundle
		want      publish.Handoff
	}{
		{"same bytes", candidate, []domain.PublishRecordBundle{migrated}, publish.HandoffSame},
		{"revised bytes", revised, []domain.PublishRecordBundle{migrated}, publish.HandoffRevised},
		{"revised then restored", candidate, []domain.PublishRecordBundle{{Record: migrated.Record, Artifact: revised.Artifact, Release: candidate.Release, Source: candidate.Source}, migrated}, publish.HandoffSame},
		{"another release", other, []domain.PublishRecordBundle{migrated}, publish.HandoffNone},
		{"same post ID from another source", otherSource, []domain.PublishRecordBundle{migrated}, publish.HandoffNone},
		{"superseded record", candidate, []domain.PublishRecordBundle{superseded}, publish.HandoffNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity, err := target.Identity(tc.candidate, tc.records)
			if err != nil || identity.Handoff != tc.want {
				t.Fatalf("Identity() handoff = %v %v, want %v", identity.Handoff, err, tc.want)
			}
		})
	}
}

func TestDropTargetDeliversAtomicallyWithoutOwningTheFile(t *testing.T) {
	root := t.TempDir()
	library := filepath.Join(root, "library")
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatal(err)
	}
	target := dropTarget(t, library)
	if caps := target.Capabilities(); caps.Retires || !caps.HandsOff || !caps.PendingRecord {
		t.Fatalf("Capabilities() = %+v", caps)
	}
	candidate, content := writeTestCandidate(t, filepath.Join(root, "storage"), "Tide Chapter 1")
	identity, err := target.Identity(candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(library, "fictional", "tide", "tide-ch0001.epub"); identity.Ref != want {
		t.Fatalf("Ref = %q, want %q", identity.Ref, want)
	}

	corrupt := candidate
	corrupt.Artifact.SHA256 = strings.Repeat("0", 64)
	if _, err := target.Deliver(context.Background(), "run_1", "", corrupt, identity, nil); err == nil {
		t.Fatal("Deliver() accepted bytes that do not match the artifact hash")
	}
	entries, _ := os.ReadDir(filepath.Dir(identity.Ref))
	if len(entries) != 0 {
		t.Fatalf("failed delivery left files behind: %v", entries)
	}

	record, err := target.Deliver(context.Background(), "run_1", "", candidate, identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != domain.PublishStatusPublished || record.TargetKind != "drop" || record.TargetRef != identity.Ref {
		t.Fatalf("record = %+v", record)
	}
	if got := string(mustReadTestFile(t, identity.Ref)); got != content {
		t.Fatalf("dropped bytes = %q, want %q", got, content)
	}
	entries, _ = os.ReadDir(filepath.Dir(identity.Ref))
	if len(entries) != 1 {
		t.Fatalf("staging file left beside the drop: %v", entries)
	}

	if err := os.WriteFile(identity.Ref, []byte("someone else's book"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Deliver(context.Background(), "run_2", "", candidate, identity, []string{candidate.Artifact.SHA256}); err == nil || !strings.Contains(err.Error(), "ownership conflict") {
		t.Fatalf("Deliver() over unowned bytes = %v, want ownership conflict", err)
	}
	if err := target.Retire(context.Background(), domain.PublishRecordBundle{}, nil, nil, nil, ""); !errors.Is(err, publish.ErrRetirementUnsupported) {
		t.Fatalf("Retire() = %v, want unsupported", err)
	}
}

func TestDropTargetRejectsVolumes(t *testing.T) {
	target := dropTarget(t, t.TempDir())
	candidate, _ := writeTestCandidate(t, t.TempDir(), "Tide Volume")
	candidate.Volume = &domain.VolumeEdition{ID: "vol_1"}
	if _, err := target.Identity(candidate, nil); err == nil {
		t.Fatal("Identity() accepted a volume")
	}
}
