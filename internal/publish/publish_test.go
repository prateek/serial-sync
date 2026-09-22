package publish_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/publish"
)

func TestTargetForRejectsUnknownKind(t *testing.T) {
	pt, err := publish.TargetFor(config.PublisherConfig{ID: "x", Kind: "carrier-pigeon"})
	if pt != nil || err == nil || !strings.Contains(err.Error(), "unsupported publisher kind") {
		t.Fatalf("TargetFor() = %#v %v, want kind rejection", pt, err)
	}
}

func TestFilesystemTargetPublishesVerifiesAndRetires(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	storageDir := filepath.Join(root, "storage")
	target, err := publish.TargetFor(config.PublisherConfig{ID: "lib", Kind: "filesystem", Enabled: true, Path: filepath.Join(root, "pub")})
	if err != nil {
		t.Fatal(err)
	}
	candidate, content := writeTestCandidate(t, storageDir, "Tide Chapter 1")
	ref, err := target.Ref(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ref, filepath.Join(root, "pub", "fictional", "tide", "tide-ch0001.epub"); got != want {
		t.Fatalf("Ref() = %q, want %q", got, want)
	}
	if got, want := target.Kind(), "filesystem"; got != want {
		t.Fatalf("Kind() = %q, want %q", got, want)
	}
	if pending := target.PublishingRecord("library", "filesystem", candidate, ref, "hash"); pending == nil || pending.Status != domain.PublishStatusPublishing {
		t.Fatalf("filesystem target must declare the pending ledger record: %+v", pending)
	}
	ordered, dependencies, err := target.Reorder([]domain.PublishCandidate{candidate}, nil, nil)
	if err != nil || len(ordered) != 1 || len(dependencies) != 0 {
		t.Fatalf("single candidate must order as itself: %+v %+v %v", ordered, dependencies, err)
	}

	record, err := target.Publish(context.Background(), "run_1", "", candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != domain.PublishStatusPublished || record.TargetRef != ref || record.TargetID != "lib" {
		t.Fatalf("publish record = %+v", record)
	}
	if got := string(mustReadTestFile(t, ref)); got != content {
		t.Fatalf("published bytes = %q, want %q", got, content)
	}
	if already, err := target.AlreadyDelivered(ref, candidate.Artifact.SHA256); err != nil || !already {
		t.Fatalf("AlreadyDelivered() = %t %v, want true", already, err)
	}
	if already, _ := target.AlreadyDelivered(filepath.Join(root, "missing.epub"), candidate.Artifact.SHA256); already {
		t.Fatalf("absent destination reported delivered")
	}

	// Re-publishing the same bytes over an owned destination is idempotent.
	if _, err := target.Publish(context.Background(), "run_2", "", candidate, []string{candidate.Artifact.SHA256}); err != nil {
		t.Fatal(err)
	}
	if got := string(mustReadTestFile(t, ref)); got != content {
		t.Fatalf("re-publish changed bytes: %q", got)
	}

	// Unrelated bytes at the destination are an ownership conflict.
	os.WriteFile(ref, []byte("someone else's bytes"), 0o644)
	if _, err := target.Publish(context.Background(), "run_3", "", candidate, []string{candidate.Artifact.SHA256}); err == nil || !strings.Contains(err.Error(), "ownership conflict") {
		t.Fatalf("expected ownership conflict, got %v", err)
	}
	if already, _ := target.AlreadyDelivered(ref, candidate.Artifact.SHA256); already {
		t.Fatalf("tampered destination reported delivered")
	}

	// Retirement removes only bytes owned by the old delivery.
	os.WriteFile(ref, []byte(content), 0o644)
	old := oldRecordBundle(candidate, ref)
	if err := target.Retire(context.Background(), old, nil, nil, nil, ""); err == nil || !strings.Contains(err.Error(), "ownership conflict") {
		t.Fatalf("expected retirement ownership conflict, got %v", err)
	}
	if err := target.Retire(context.Background(), old, nil, nil, []string{old.Artifact.SHA256}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ref); !os.IsNotExist(err) {
		t.Fatalf("owned file not retired: %v", err)
	}
	// An already-absent file retires cleanly.
	if err := target.Retire(context.Background(), old, nil, nil, []string{old.Artifact.SHA256}, ""); err != nil {
		t.Fatal(err)
	}
}

func TestFilesystemTargetLeavesInPlaceReplacementUntouched(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target, err := publish.TargetFor(config.PublisherConfig{ID: "lib", Kind: "filesystem", Enabled: true, Path: filepath.Join(root, "pub")})
	if err != nil {
		t.Fatal(err)
	}
	candidate, content := writeTestCandidate(t, filepath.Join(root, "storage"), "Tide Chapter 1")
	replacement, replacementContent := writeTestCandidate(t, filepath.Join(root, "storage2"), "Tide Chapter 1 revised")
	replacement.Artifact.ID = "art_2"
	sum := sha256.Sum256([]byte(replacementContent))
	replacement.Artifact.SHA256 = hex.EncodeToString(sum[:])
	ref, _ := target.Ref(candidate)
	if err := os.MkdirAll(filepath.Dir(ref), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ref, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := oldRecordBundle(candidate, ref)
	// A planned candidate occupying the same path supersedes the old delivery
	// in place; its bytes must survive retirement untouched.
	if err := target.Retire(context.Background(), old, []domain.PublishCandidate{replacement}, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got := string(mustReadTestFile(t, ref)); got != content {
		t.Fatalf("in-place retirement altered the new bytes: %q", got)
	}
}

func TestExecTargetPublishesPayloadAndSupersedes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	script := filepath.Join(root, "hook.py")
	hook := `import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
payload = json.load(sys.stdin)
if isinstance(payload, dict) and "action" in payload:
    (root / ("event-" + payload["action"] + ".json")).write_text(json.dumps(payload))
else:
    (root / "publish.json").write_text(json.dumps(payload))
`
	if err := os.WriteFile(script, []byte(hook), 0o600); err != nil {
		t.Fatal(err)
	}
	command := []string{"python3", script, root}
	target, err := publish.TargetFor(config.PublisherConfig{ID: "hook", Kind: "exec", Enabled: true, ProtocolVersion: 2, Command: command})
	if err != nil {
		t.Fatal(err)
	}
	candidate, _ := writeTestCandidate(t, filepath.Join(root, "storage"), "Tide Chapter 1")
	if pending := target.PublishingRecord("hook", "exec", candidate, "cmd", "hash"); pending != nil {
		t.Fatalf("exec target must not declare a pending ledger record: %+v", pending)
	}
	ordered, dependencies, err := target.Reorder([]domain.PublishCandidate{candidate, candidate}, nil, nil)
	if err != nil || len(ordered) != 2 || len(dependencies) != 0 {
		t.Fatalf("exec target must keep the input order with no dependencies: %+v %+v %v", ordered, dependencies, err)
	}

	record, err := target.Publish(context.Background(), "run_1", "scope_1", candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != domain.PublishStatusPublished || record.TargetKind != "exec" || record.ArtifactID != candidate.Artifact.ID {
		t.Fatalf("publish record = %+v", record)
	}
	eventData := string(mustReadTestFile(t, filepath.Join(root, "event-publish.json")))
	if !strings.Contains(eventData, `"action": "publish"`) || !strings.Contains(eventData, candidate.Artifact.ID) || !strings.Contains(eventData, `"event_id"`) {
		t.Fatalf("publish event = %s", eventData)
	}

	old := oldRecordBundle(candidate, record.TargetRef)
	if err := target.Retire(context.Background(), old, nil, []domain.PublishCandidate{candidate}, nil, "scope_1"); err != nil {
		t.Fatal(err)
	}
	eventData = string(mustReadTestFile(t, filepath.Join(root, "event-supersede.json")))
	if !strings.Contains(eventData, `"action": "supersede"`) || !strings.Contains(eventData, old.Record.PublishHash) || !strings.Contains(eventData, candidate.Artifact.ID) {
		t.Fatalf("supersede event = %s", eventData)
	}

	// A legacy single-file hook receives no retirement event.
	legacyRoot := t.TempDir()
	legacyTarget, err := publish.TargetFor(config.PublisherConfig{ID: "hook-legacy", Kind: "exec", Enabled: true, ProtocolVersion: 1, Command: []string{"python3", script, legacyRoot}})
	if err != nil {
		t.Fatal(err)
	}
	if legacyTarget.Capabilities().Retires {
		t.Fatal("legacy hook must not claim retirement support")
	}
	if err := legacyTarget.Retire(context.Background(), old, nil, []domain.PublishCandidate{candidate}, nil, "scope_2"); !errors.Is(err, publish.ErrRetirementUnsupported) {
		t.Fatalf("legacy hook retirement = %v, want ErrRetirementUnsupported", err)
	}
	if _, err := os.Stat(filepath.Join(legacyRoot, "event-supersede.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy hook received a supersede event: %v", err)
	}
}

func TestPublishHashBindsTargetArtifactAndDestination(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target, _ := publish.TargetFor(config.PublisherConfig{ID: "lib", Kind: "filesystem", Enabled: true, Path: filepath.Join(root, "pub")})
	candidate, _ := writeTestCandidate(t, filepath.Join(root, "storage"), "Tide Chapter 1")
	ref, _ := target.Ref(candidate)
	hashInput, err := target.PublishHashInput(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if hashInput != ref {
		t.Fatalf("PublishHashInput() = %q, want %q", hashInput, ref)
	}
	hash := publish.PublishHash("lib", candidate.Artifact.SHA256, hashInput)
	if hash == publish.PublishHash("other", candidate.Artifact.SHA256, hashInput) {
		t.Fatalf("publish hash ignores the target id")
	}
	if hash == publish.PublishHash("lib", "different-sha", hashInput) {
		t.Fatalf("publish hash ignores the artifact bytes")
	}
}

func writeTestCandidate(t *testing.T, storageDir, title string) (domain.PublishCandidate, string) {
	content := "fixture chapter bytes " + title
	sum := sha256.Sum256([]byte(content))
	sha := hex.EncodeToString(sum[:])
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	storageRef := filepath.Join(storageDir, "artifact.epub")
	if err := os.WriteFile(storageRef, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	source := domain.Source{ID: "fictional", Provider: "fixture", SourceURL: "https://example.test/fictional", CreatorID: "", CreatorName: "Test Author", AuthProfileID: "fixture", Enabled: true}
	track := domain.StoryTrack{ID: "fictional/tide", SourceID: "fictional", TrackKey: "tide", TrackName: "Tide", CanonicalAuthor: "Test Author"}
	release := domain.Release{ID: "rel_1", SourceID: "fictional", ProviderReleaseID: "p1", URL: "https://example.test/p1", Title: title}
	artifact := domain.Artifact{
		ID:           "art_1",
		ReleaseID:    "rel_1",
		TrackID:      "fictional/tide",
		ArtifactKind: "epub",
		IsCanonical:  true,
		Filename:     "tide-ch0001.epub",
		MIMEType:     "application/epub+zip",
		SHA256:       sha,
		StorageRef:   storageRef,
		BuiltAt:      time.Now().UTC(),
		State:        domain.ArtifactStateMaterialized,
	}
	candidate := domain.PublishCandidate{
		Source:     source,
		Track:      track,
		Release:    release,
		Assignment: domain.ReleaseAssignment{ReleaseID: "rel_1", TrackID: "fictional/tide", RuleID: "rule_1", ReleaseRole: domain.ReleaseRoleChapter, Confidence: 1},
		Artifact:   artifact,
	}
	return candidate, content
}

func oldRecordBundle(candidate domain.PublishCandidate, ref string) domain.PublishRecordBundle {
	return domain.PublishRecordBundle{
		Record: domain.PublishRecord{
			ID:          "pub_old",
			ArtifactID:  candidate.Artifact.ID,
			TargetID:    "irrelevant",
			TargetKind:  "irrelevant",
			TargetRef:   ref,
			PublishHash: "hash_old",
			PublishedAt: time.Now().UTC(),
			Status:      domain.PublishStatusPublished,
		},
		Artifact: candidate.Artifact,
		Release:  candidate.Release,
	}
}

func mustReadTestFile(t *testing.T, path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
