package discovery_test

import (
	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/discovery"
	"github.com/prateek/serial-sync/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAttachmentSuggestionMatchesItsEvidence(t *testing.T) {
	post := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "New installment", Attachments: []domain.Attachment{{FileName: "Tide Lantern Chapter 1.epub"}}}
	candidates := discovery.Detect("fictional", []domain.NormalizedRelease{post}, &config.Config{}, nil, time.Now())
	draft := discovery.Suggestions(candidates, map[string][]domain.NormalizedRelease{"fictional": {post}})
	if !strings.Contains(draft, "attachment_patterns") {
		t.Fatalf("missing attachment selector: %s", draft)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	data := "[[sources]]\nid=\"fictional\"\nprovider=\"patreon\"\nurl=\"https://example.invalid\"\n" + draft
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	decision := classify.Decide("fictional", post, cfg.RulesForSource("fictional"))
	if !decision.Matched || decision.ContentStrategy != domain.ContentStrategyAttachmentOnly {
		t.Fatalf("draft cannot match its evidence: %+v", decision)
	}
}

func TestPrologueSuggestionUsesTitleEvidence(t *testing.T) {
	post := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Tide Lantern Prologue", TextPlain: strings.Repeat("Filler fiction. ", 150)}
	candidates := discovery.Detect("fictional", []domain.NormalizedRelease{post}, &config.Config{}, nil, time.Now())
	draft := discovery.Suggestions(candidates, map[string][]domain.NormalizedRelease{"fictional": {post}})
	if !strings.Contains(draft, "title_patterns") || strings.Contains(draft, "attachment_patterns") {
		t.Fatalf("prologue lost title evidence: %s", draft)
	}
}
