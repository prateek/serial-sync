package app

import (
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

// Hold-table tests at the decider's interface: history in, held-aware
// decisions out, with no store, provider or EPUB tooling.

func TestDecideHoldsFirstChaptersAndLeavesMatchedNeighbors(t *testing.T) {
	t.Helper()
	one := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Tide Lantern - Chapter 1", TextPlain: strings.Repeat("Filler fiction. ", 150), PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	two := domain.NormalizedRelease{ProviderReleaseID: "2", Title: "Tide Lantern - Chapter 2", TextPlain: strings.Repeat("Filler fiction. ", 150), PublishedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	hold := config.RuleConfig{Source: "fictional", MatchType: "fallback", TrackKey: "archive", TrackName: "Archive", ReleaseRole: "chapter", ContentStrategy: "text_post"}
	hold.Guards = config.Guards{HoldCandidates: true}
	cfg2 := config.Config{Sources: []config.SourceConfig{{ID: "fictional", Enabled: true}}, Rules: []config.RuleConfig{hold}}

	decisions, _ := decideReleases("fictional", []domain.NormalizedRelease{one, two}, map[string]bool{}, &cfg2, nil, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	held := decisions["1"]
	if len(held.Explanation.HeldReasons) == 0 || !strings.Contains(strings.Join(held.Explanation.HeldReasons, ","), "first-chapter") {
		t.Fatalf("first chapter should be held with the first-chapter reason: %+v", held)
	}
	if held.Decision.Sequence != nil {
		t.Fatalf("held decision must carry no sequence: %+v", held.Decision)
	}
	if held.Decision.TrackKey != "unmatched" || !held.Decision.Matched || held.Decision.SelectedContent.Kind != "none" {
		t.Fatalf("held decision should be review with no content selected: %+v", held.Decision)
	}
	neighbor := decisions["2"]
	if len(neighbor.Explanation.HeldReasons) > 0 {
		t.Fatalf("matched neighbor must not be held when the first chapter is: %+v", neighbor)
	}
	if neighbor.Decision.Sequence == nil || neighbor.Decision.Sequence.Chapter != 2 {
		t.Fatalf("matched neighbor must keep its sequence: %+v", neighbor.Decision)
	}
}

func TestDecideHoldReasonsResetCollectionsAndOverride(t *testing.T) {
	t.Helper()
	base := config.RuleConfig{Source: "fictional", MatchType: "fallback", TrackKey: "archive", TrackName: "Archive", ReleaseRole: "chapter", ContentStrategy: "text_post"}
	hold := base
	hold.Guards = config.Guards{HoldCandidates: true}
	cfg := config.Config{Sources: []config.SourceConfig{{ID: "fictional", Enabled: true}}, Rules: []config.RuleConfig{hold}}

	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	// A chapter after a higher one held dates back the stream: reset reason.
	one := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Tide Lantern - Chapter 1", TextPlain: strings.Repeat("Filler fiction. ", 150), PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	four := domain.NormalizedRelease{ProviderReleaseID: "4", Title: "Tide Lantern - Chapter 4", TextPlain: strings.Repeat("Filler fiction. ", 150), PublishedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	two := domain.NormalizedRelease{ProviderReleaseID: "2", Title: "Tide Lantern - Chapter 2", TextPlain: strings.Repeat("Filler fiction. ", 150), PublishedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)}
	decisions, _ := decideReleases("fictional", []domain.NormalizedRelease{one, four, two}, map[string]bool{}, &cfg, nil, now)
	for _, row := range []struct{ id, reason string }{{"1", "first-chapter"}, {"4", ""}, {"2", "numbering-reset"}} {
		held := decisions[row.id]
		joined := strings.Join(held.Explanation.HeldReasons, ",")
		if row.reason == "" {
			if len(held.Explanation.HeldReasons) > 0 {
				t.Fatalf("chapter %s should not be held: %+v", row.id, held)
			}
		} else if !strings.Contains(joined, row.reason) {
			t.Fatalf("chapter %s should be held with %s: %+v", row.id, row.reason, held)
		}
	}
	// An unknown collection reference holds the release with its own reason.
	mystery := domain.NormalizedRelease{ProviderReleaseID: "3", Title: "Tide Lantern - Chapter 3", TextPlain: strings.Repeat("Filler fiction. ", 150), PublishedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)}
	mystery.Enrichment = &domain.ReleaseEnrichment{NormalizerVersion: 1, Collections: []domain.LabelReference{{ID: "col-xyz"}}}
	decisions, _ = decideReleases("fictional", []domain.NormalizedRelease{mystery}, map[string]bool{}, &cfg, nil, now)
	if !strings.Contains(strings.Join(decisions["3"].Explanation.HeldReasons, ","), "unknown-collection") {
		t.Fatalf("unknown collection must hold the release: %+v", decisions["3"])
	}
	// An override rule is never held.
	over := base
	over.Guards = config.Guards{HoldCandidates: true}
	over.Override = true
	cfgOver := config.Config{Sources: []config.SourceConfig{{ID: "fictional", Enabled: true}}, Rules: []config.RuleConfig{over}}

	decisions, _ = decideReleases("fictional", []domain.NormalizedRelease{one}, map[string]bool{}, &cfgOver, nil, now)
	if len(decisions["1"].Explanation.HeldReasons) > 0 {
		t.Fatalf("override rules must not be held: %+v", decisions["1"])
	}
}

func TestDecideWithoutReasonsDoesNotHold(t *testing.T) {
	t.Helper()
	hold := config.RuleConfig{Source: "fictional", MatchType: "fallback", TrackKey: "archive", TrackName: "Archive", ReleaseRole: "chapter", ContentStrategy: "text_post"}
	hold.Guards = config.Guards{HoldCandidates: true}
	cfg := config.Config{Sources: []config.SourceConfig{{ID: "fictional", Enabled: true}}, Rules: []config.RuleConfig{hold}}

	mid := domain.NormalizedRelease{ProviderReleaseID: "2", Title: "Tide Lantern - Chapter 2", TextPlain: strings.Repeat("Filler fiction. ", 150), PublishedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
	decisions, _ := decideReleases("fictional", []domain.NormalizedRelease{mid}, map[string]bool{}, &cfg, nil, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if len(decisions["2"].Explanation.HeldReasons) > 0 || decisions["2"].Decision.Sequence == nil {
		t.Fatalf("a mid-series chapter with no trigger must pass through: %+v", decisions["2"])
	}
}
