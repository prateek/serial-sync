package artifact

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestFilenameMatchMaterializesThePinnedFileAndDoesNotFallBack(t *testing.T) {
	root := t.TempDir()
	content := []byte("%PDF-1.4\nFictional selected document\n%%EOF")
	selected := filepath.Join(root, "Harbor Chapter 2.pdf")
	if err := os.WriteFile(selected, content, 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "Other Chapter 99.epub")
	if err := os.WriteFile(other, []byte("unrelated attachment"), 0600); err != nil {
		t.Fatal(err)
	}
	normalized := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "New installment", TextPlain: "Unrelated body", Attachments: []domain.Attachment{
		{FileName: filepath.Base(other), LocalPath: other, MIMEType: "application/epub+zip"},
		{FileName: filepath.Base(selected), LocalPath: selected, MIMEType: "application/pdf"},
	}}
	authored := config.RuleConfig{Source: "fictional", MatchType: "attachment_filename_regex", MatchValue: "^Harbor", TrackKey: "harbor", ContentStrategy: "attachment_preferred", AttachmentPriority: []string{"epub", "pdf"}, OutputFormat: "preserve"}
	ruleSet := (&config.Config{Rules: []config.RuleConfig{authored}}).CompileRuleSet()
	decision := classify.Explain("fictional", normalized, ruleSet.ForSource("fictional")).Decision
	materializer := New(filepath.Join(root, "artifacts"))
	source := domain.Source{ID: "fictional"}
	track := domain.StoryTrack{ID: "harbor", TrackKey: "harbor", TrackName: "Harbor"}
	release := domain.Release{ID: "1", ProviderReleaseID: "1"}
	plan, err := materializer.Plan(context.Background(), source, track, release, normalized, decision, nil)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := materializer.Materialize(context.Background(), source, track, release, plan)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(artifact.StorageRef)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("wrong selected bytes: %s %v", got, err)
	}
	if err := os.Remove(selected); err != nil {
		t.Fatal(err)
	}
	if _, err := materializer.Plan(context.Background(), source, track, release, normalized, decision, nil); err == nil {
		t.Fatal("missing pinned attachment silently fell back")
	}
}
