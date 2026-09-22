package discovery_test

import (
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/discovery"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestDiscoveryGroupsFirstEvidenceAndPersistsDismissal(t *testing.T) {
	cfg := config.Config{Sources: []config.SourceConfig{{ID: "fictional", Enabled: true}}, Rules: []config.RuleConfig{{Source: "fictional", MatchType: "fallback", TrackKey: "archive", TrackName: "Archive", ReleaseRole: "chapter", ContentStrategy: "text_post"}}}
	first := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Tide Lantern - Chapter 1", Collections: []string{"Shelved Stories"}, Tags: []string{"fantasy"}, TextPlain: strings.Repeat("Filler fiction. ", 150), PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	candidates := discovery.Detect("fictional", []domain.NormalizedRelease{first}, &cfg, nil, now)
	if len(candidates) != 1 || len(candidates[0].MemberReleaseIDs) != 1 || candidates[0].Status == "resolved" {
		t.Fatalf("first claimed chapter must raise one candidate: %+v", candidates)
	}
	id := candidates[0].ID
	candidates[0].DismissalReason = "An intentional repost"
	candidates[0].DismissedFingerprint = candidates[0].EvidenceFingerprint
	candidates[0].Status = "resolved"

	repeat := discovery.Detect("fictional", []domain.NormalizedRelease{first}, &cfg, candidates, now.Add(time.Hour))
	if len(repeat) != 1 || repeat[0].ID != id || repeat[0].Status != "resolved" || !repeat[0].FirstObserved.Equal(now) {
		t.Fatalf("lookback reopened dismissal: %+v", repeat)
	}
	second := first
	second.ProviderReleaseID = "2"
	second.Title = "Tide Lantern - Chapter 2"
	second.PublishedAt = first.PublishedAt.Add(90 * 24 * time.Hour)

	changed := discovery.Detect("fictional", []domain.NormalizedRelease{second, first}, &cfg, repeat, now.Add(100*24*time.Hour))
	if len(changed) != 1 || changed[0].ID != id || len(changed[0].MemberReleaseIDs) != 2 || changed[0].Status == "resolved" {
		t.Fatalf("new evidence did not reopen the same candidate: %+v", changed)
	}
	mapped := cfg
	mapped.Rules = []config.RuleConfig{{Source: "fictional", MatchType: "collection", MatchValue: "Shelved Stories", TrackKey: "tide-lantern", TrackName: "Tide Lantern", ReleaseRole: "chapter", ContentStrategy: "text_post"}}

	resolved := discovery.Detect("fictional", []domain.NormalizedRelease{first, second}, &mapped, changed, now.Add(101*24*time.Hour))
	if len(resolved) != 1 || resolved[0].Status != "resolved" || len(resolved[0].ResolvedMembers) != 2 {
		t.Fatalf("mapping did not resolve members: %+v", resolved)
	}
}

func TestDiscoveryCannotBeHiddenByCatchAllAndDoesNotGuessGenericContinuation(t *testing.T) {
	cfg := config.Config{Rules: []config.RuleConfig{{Source: "fictional", MatchType: "collection", MatchValue: "All Stories", TrackKey: "harbor", TrackName: "Harbor", ReleaseRole: "chapter", ContentStrategy: "attachment_only"}}}
	release := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "A new installment", Collections: []string{"All Stories"}, Attachments: []domain.Attachment{{FileName: "Tide Lantern Chapter 1.epub"}}}

	candidates := discovery.Detect("fictional", []domain.NormalizedRelease{release}, &cfg, nil, time.Now())
	if len(candidates) != 1 {
		t.Fatalf("catch-all hid attachment story: %+v", candidates)
	}
	release.Attachments[0].FileName = "chapter-24.epub"

	candidates = discovery.Detect("fictional", []domain.NormalizedRelease{release}, &cfg, nil, time.Now())
	if len(candidates) != 0 {
		t.Fatalf("generic continuation guessed a second series: %+v", candidates)
	}
}

func TestAnonymousNumberingContinuesAndNamedStoriesSeparate(t *testing.T) {
	cfg := config.Config{Rules: []config.RuleConfig{{Source: "fictional", MatchType: "fallback", TrackKey: "archive", TrackName: "Archive", ReleaseRole: "chapter", ContentStrategy: "text_post"}}}
	now := time.Now()
	posts := []domain.NormalizedRelease{{ProviderReleaseID: "1", Title: "Chapter 1", PublishedAt: now}, {ProviderReleaseID: "2", Title: "Chapter 2", PublishedAt: now.Add(time.Hour)}}

	got := discovery.Detect("fictional", posts, &cfg, nil, now)
	if len(got) != 1 || len(got[0].MemberReleaseIDs) != 2 {
		t.Fatalf("anonymous numbering did not join: %+v", got)
	}
	posts = []domain.NormalizedRelease{{ProviderReleaseID: "1", Title: "Story archive", Collections: []string{"All Stories"}, PublishedAt: now}, {ProviderReleaseID: "2", Title: "Alpha Chapter 1", Collections: []string{"All Stories"}, PublishedAt: now.Add(time.Hour)}, {ProviderReleaseID: "3", Title: "Beta Chapter 1", Collections: []string{"All Stories"}, PublishedAt: now.Add(2 * time.Hour)}}

	got = discovery.Detect("fictional", posts, &cfg, nil, now)
	if len(got) != 2 {
		t.Fatalf("catch-all merged named stories: %+v", got)
	}
}

func TestDeclaredBookExplainsNumberingReset(t *testing.T) {
	cfg := config.Config{Series: []config.SeriesConfig{{ID: "harbor", Title: "Harbor", Books: []config.BookConfig{{ID: "one", Number: 1}, {ID: "two", Number: 2}}}}, Rules: []config.RuleConfig{{Source: "fictional", MatchType: "title_regex", MatchValue: "Harbor", TrackKey: "harbor", TrackName: "Harbor", ReleaseRole: "chapter", ContentStrategy: "text_post"}}}
	now := time.Now()
	posts := []domain.NormalizedRelease{{ProviderReleaseID: "1", Title: "Harbor Book 1 Chapter 10", PublishedAt: now}, {ProviderReleaseID: "2", Title: "Harbor Book 2 Chapter 1", PublishedAt: now.Add(time.Hour)}}

	got := discovery.Detect("fictional", posts, &cfg, nil, now)
	if len(got) != 0 {
		t.Fatalf("declared book transition was unexplained: %+v", got)
	}
}

func TestSplitCandidatesRetainUniqueIdentities(t *testing.T) {
	cfg := config.Config{}
	now := time.Now()
	first := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Alpha Chapter 1", PublishedAt: now}
	second := first
	second.ProviderReleaseID = "2"
	second.Title = "Alpha Chapter 2"
	second.PublishedAt = now.Add(time.Hour)

	before := discovery.Detect("fictional", []domain.NormalizedRelease{first, second}, &cfg, nil, now)
	earlier := first
	earlier.ProviderReleaseID = "0"
	earlier.Title = "Beta Chapter 1"
	earlier.PublishedAt = now.Add(-time.Hour)
	second.Title = "Beta Chapter 2"

	split := discovery.Detect("fictional", []domain.NormalizedRelease{earlier, first, second}, &cfg, before, now.Add(2*time.Hour))
	if len(split) != 2 || split[0].ID == split[1].ID {
		t.Fatalf("split lost unique identities: %+v", split)
	}

	restarted := discovery.Detect("fictional", []domain.NormalizedRelease{earlier, first, second}, &cfg, split, now.Add(3*time.Hour))
	if len(restarted) != 2 || restarted[0].ID != split[0].ID || restarted[1].ID != split[1].ID {
		t.Fatalf("split identities did not survive restart: %+v", restarted)
	}
}

func TestTagReferenceDoesNotHideCollectionWithSameName(t *testing.T) {
	cfg := config.Config{Rules: []config.RuleConfig{{Source: "fictional", MatchType: "tag", MatchValue: "Harbor", TrackKey: "harbor", TrackName: "Harbor", ReleaseRole: "chapter", ContentStrategy: "text_post"}}}
	post := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Harbor Chapter 2", Tags: []string{"Harbor"}, Collections: []string{"Harbor"}}

	got := discovery.Detect("fictional", []domain.NormalizedRelease{post}, &cfg, nil, time.Now())
	if len(got) != 1 {
		t.Fatalf("unreferenced collection was hidden by tag rule: %+v", got)
	}
}

func TestCounterfactualFindsFirstReleaseWithoutFutureEvidence(t *testing.T) {
	first := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Harbor Chapter 1"}
	later := first
	later.ProviderReleaseID = "2"
	later.Title = "Harbor Chapter 2"
	later.PublishedAt = time.Now()
	cfg := config.Config{Rules: []config.RuleConfig{{Source: "fictional", MatchType: "title_regex", MatchValue: "Harbor", TrackKey: "harbor", TrackName: "Harbor", ReleaseRole: "chapter", ContentStrategy: "text_post"}}}

	if got := discovery.Detect("fictional", []domain.NormalizedRelease{first}, &cfg, nil, time.Now()); len(got) != 0 {
		t.Fatalf("mapped title raised an unexplained start: %+v", got)
	}
	cfg.Rules = nil

	one := discovery.Detect("fictional", []domain.NormalizedRelease{first, first}, &cfg, nil, time.Now())

	all := discovery.Detect("fictional", []domain.NormalizedRelease{first, later}, &cfg, nil, time.Now())
	if len(one) != 1 || len(one[0].MemberReleaseIDs) != 1 || len(all) != 1 || one[0].ID != all[0].ID || len(all[0].MemberReleaseIDs) != 2 {
		t.Fatalf("first evidence depended on future posts or duplicated lookback: %+v / %+v", one, all)
	}
}

func TestDiscoveryDistinguishesExternalChaptersTeasersAndIgnoredLabels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		post   domain.NormalizedRelease
		signal string
	}{
		{"external", domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Harbor Chapter 1", TextHTML: `<a href="https://example.invalid/chapter">Read elsewhere</a>`}, "content-external"},
		{"teaser", domain.NormalizedRelease{ProviderReleaseID: "1", Title: "A new story teaser", TextPlain: "A small sample."}, "teaser"},
		{"bonus", domain.NormalizedRelease{ProviderReleaseID: "1", Title: "A bonus story", TextPlain: strings.Repeat("Filler. ", 250)}, "substantial-review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := discovery.Detect("fictional", []domain.NormalizedRelease{tc.post}, &config.Config{}, nil, time.Now())
			if len(got) != 1 {
				t.Fatalf("missing candidate: %+v", got)
			}
			found := false
			for _, evidence := range got[0].Evidence {
				found = found || evidence.Kind == tc.signal
			}
			if !found {
				t.Fatalf("missing evidence %s: %+v", tc.signal, got)
			}
		})
	}
	cfg := config.Config{Sources: []config.SourceConfig{{ID: "fictional", IgnoreLabels: []string{"Genre"}}}, Rules: []config.RuleConfig{{Source: "fictional", MatchType: "fallback", TrackKey: "archive", ReleaseRole: "extra", ContentStrategy: "text_post"}}}
	post := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "An unnumbered scene", Collections: []string{"Genre"}, TextPlain: strings.Repeat("Filler. ", 250)}

	if got := discovery.Detect("fictional", []domain.NormalizedRelease{post}, &cfg, nil, time.Now()); len(got) != 0 {
		t.Fatalf("ignored label raised a candidate: %+v", got)
	}
}

func TestSequenceOverrideExplainsBookReset(t *testing.T) {
	cfg := config.Config{Series: []config.SeriesConfig{{ID: "harbor", Books: []config.BookConfig{{ID: "two", Number: 2}}, SequenceOverrides: []config.SequenceOverride{{Source: "fictional", ReleaseID: "2", BookID: "two", Chapter: 1}}}}, Rules: []config.RuleConfig{{Source: "fictional", MatchType: "title_regex", MatchValue: "Harbor", TrackKey: "harbor", TrackName: "Harbor", ReleaseRole: "chapter", ContentStrategy: "text_post"}}}
	now := time.Now()
	posts := []domain.NormalizedRelease{{ProviderReleaseID: "1", Title: "Harbor Chapter 10", PublishedAt: now}, {ProviderReleaseID: "2", Title: "Harbor Chapter 1", PublishedAt: now.Add(time.Hour)}}

	if got := discovery.Detect("fictional", posts, &cfg, nil, now); len(got) != 0 {
		t.Fatalf("sequence override did not explain reset: %+v", got)
	}
}

func TestStoredReplayCannotResolveEvidenceFromANewerObservedVersion(t *testing.T) {
	cfg := config.Config{Rules: []config.RuleConfig{{Source: "fictional", MatchType: "title_regex", MatchValue: "Harbor", TrackKey: "harbor", TrackName: "Harbor", ReleaseRole: "chapter", ContentStrategy: "text_post"}}}
	stale := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Harbor Chapter 2"}
	newer := stale
	newer.Title = "New Story Chapter 1"

	observed := discovery.Detect("fictional", []domain.NormalizedRelease{newer}, &cfg, nil, time.Now())
	if len(observed) != 1 {
		t.Fatal(observed)
	}

	replayed := discovery.Replay("fictional", []domain.NormalizedRelease{stale}, &cfg, observed, time.Now())
	if len(replayed) != 1 || replayed[0].Status == "resolved" || len(replayed[0].UncapturedMembers) != 1 {
		t.Fatalf("stale captured version falsely resolved new evidence: %+v", replayed)
	}

	observedAgain := discovery.Detect("fictional", []domain.NormalizedRelease{stale}, &cfg, observed, time.Now())
	if len(observedAgain) != 1 || observedAgain[0].Status != "resolved" {
		t.Fatalf("fresh authoritative observation could not resolve it: %+v", observedAgain)
	}
}

func TestAttachmentNumberingResetBehindBodyRule(t *testing.T) {
	cfg := config.Config{Rules: []config.RuleConfig{{Source: "fictional", MatchType: "fallback", TrackKey: "archive", TrackName: "Archive", ReleaseRole: "chapter", ContentStrategy: "text_post"}}}
	now := time.Now()
	posts := []domain.NormalizedRelease{{ProviderReleaseID: "1", Title: "Installment", Attachments: []domain.Attachment{{FileName: "chapter-50.epub"}}, PublishedAt: now}, {ProviderReleaseID: "2", Title: "Installment", Attachments: []domain.Attachment{{FileName: "chapter-2.epub"}}, PublishedAt: now.Add(time.Hour)}}

	got := discovery.Detect("fictional", posts, &cfg, nil, now)
	for _, c := range got {
		for _, evidence := range c.Evidence {
			if evidence.Kind == "numbering-reset" {
				return
			}
		}
	}
	t.Fatalf("attachment numbering hidden by body rule: %+v", got)
}

func TestIncrementalObservationCannotResolveUnsavedVersion(t *testing.T) {
	cfg := config.Config{Rules: []config.RuleConfig{{Source: "fictional", MatchType: "title_regex", MatchValue: "Harbor", TrackKey: "harbor", TrackName: "Harbor", ReleaseRole: "chapter", ContentStrategy: "text_post"}}}
	old := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Harbor Chapter 8"}
	edited := old
	edited.Title = "Star Garden Chapter 1"
	now := time.Now()

	raised := discovery.Detect("fictional", []domain.NormalizedRelease{edited}, &cfg, nil, now)
	later := domain.NormalizedRelease{ProviderReleaseID: "2", Title: "Harbor Chapter 9"}
	got := discovery.Observe("fictional", []domain.NormalizedRelease{old, later}, map[string]bool{"2": true}, &cfg, raised, now.Add(time.Hour))
	if len(got) != 1 || got[0].Status == "resolved" || len(got[0].UncapturedMembers) != 1 {
		t.Fatalf("stale catalog overwrote new evidence: %+v", got)
	}
}

func TestStoredReplayRetainsUncapturedChronology(t *testing.T) {
	cfg := config.Config{Rules: []config.RuleConfig{{Source: "fictional", MatchType: "title_regex", MatchValue: "Harbor", TrackKey: "harbor", TrackName: "Harbor", ReleaseRole: "chapter", ContentStrategy: "text_post"}}}
	now := time.Now()
	first := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Harbor Chapter 2", PublishedAt: now}
	second := domain.NormalizedRelease{ProviderReleaseID: "2", Title: "Harbor Chapter 3", PublishedAt: now.Add(time.Hour)}
	edited := first
	edited.PublishedAt = now.Add(2 * time.Hour)

	raised := discovery.Detect("fictional", []domain.NormalizedRelease{edited, second}, &cfg, nil, now)

	got := discovery.Replay("fictional", []domain.NormalizedRelease{first, second}, &cfg, raised, now)
	if len(got) != 1 || got[0].Status == "resolved" || len(got[0].UncapturedMembers) != 1 {
		t.Fatalf("stale chronology resolved candidate: %+v", got)
	}
}

func TestOverrideResolvesMemberInsideExistingCandidate(t *testing.T) {
	now := time.Now()
	posts := []domain.NormalizedRelease{{ProviderReleaseID: "1", Title: "Tide Lantern Chapter 1", PublishedAt: now}, {ProviderReleaseID: "2", Title: "Tide Lantern Chapter 2", PublishedAt: now.Add(time.Hour)}}
	cfg := config.Config{}

	before := discovery.Detect("fictional", posts, &cfg, nil, now)
	cfg.Overrides = []config.OverrideConfig{{Source: "fictional", ReleaseID: "2", Review: true, Reason: "Confirmed excerpt"}}

	got := discovery.Replay("fictional", posts, &cfg, before, now)
	if len(got) != 1 || len(got[0].MemberReleaseIDs) != 1 || len(got[0].ResolvedMembers) != 1 || got[0].ResolvedMembers[0] != "2" {
		t.Fatalf("override did not settle candidate member: %+v", got)
	}
}

func TestHoldingUsesChronologicalEvidenceAndIsOptIn(t *testing.T) {
	now := time.Now()
	posts := []domain.NormalizedRelease{{ProviderReleaseID: "3", Title: "Harbor Chapter 2", PublishedAt: now.Add(2 * time.Hour)}, {ProviderReleaseID: "1", Title: "Harbor Chapter 1", PublishedAt: now}, {ProviderReleaseID: "2", Title: "Harbor Chapter 50", PublishedAt: now.Add(time.Hour)}}
	cfg := config.Config{Rules: []config.RuleConfig{{Source: "fictional", MatchType: "fallback", TrackKey: "harbor", TrackName: "Harbor", ReleaseRole: "chapter", ContentStrategy: "text_post"}}}

	ordinary := discovery.Analyze("fictional", posts, nil, &cfg, nil, now)
	for _, id := range []string{"1", "2", "3"} {
		if ordinary.Decisions[id].Decision.SeriesID != "harbor" {
			t.Fatal("advisory discovery altered routing")
		}
	}
	cfg.Rules[0].HoldCandidates = true

	held := discovery.Analyze("fictional", posts, nil, &cfg, nil, now)
	for _, id := range []string{"1", "3"} {
		if held.Decisions[id].Decision.SeriesID != "unmatched" || len(held.Decisions[id].Explanation.HeldReasons) == 0 {
			t.Fatalf("%s was not held: %+v", id, held.Decisions[id])
		}
	}
	if held.Decisions["2"].Decision.SeriesID != "harbor" {
		t.Fatal("ordinary continuation held")
	}
	if len(held.Candidates) == 0 {
		t.Fatal("held evidence was not reported")
	}
}

func TestDiscoverySequenceDoesNotReplaceSelectedContentSequence(t *testing.T) {
	cfg := config.Config{Rules: []config.RuleConfig{{Source: "fictional", MatchType: "attachment_filename_regex", MatchValue: "Harbor", TrackKey: "harbor", TrackName: "Harbor", ReleaseRole: "chapter", ContentStrategy: "attachment_only"}}}
	post := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "New installment", Attachments: []domain.Attachment{{FileName: "Other Chapter 99.pdf"}, {FileName: "Harbor Chapter 2.epub"}}}

	analysis := discovery.Analyze("fictional", []domain.NormalizedRelease{post}, nil, &cfg, nil, time.Now())
	decision := analysis.Decisions["1"].Decision
	if decision.Sequence.Chapter != 2 || decision.SelectedContent.FileName != "Harbor Chapter 2.epub" {
		t.Fatalf("discovery replaced selected content's sequence: %+v", decision)
	}
}

func TestPinnedLabelDoesNotAcknowledgeANewIdentityWithTheSameName(t *testing.T) {
	cfg := config.Config{Rules: []config.RuleConfig{
		{Source: "fictional", TrackKey: "harbor", Selection: config.Selection{Collections: []config.CollectionSelector{{ID: "old", Name: "Harbor"}}}},
		{Source: "fictional", MatchType: "fallback", TrackKey: "archive", ContentStrategy: "text_post", Guards: config.Guards{HoldCandidates: true}},
	}}
	post := domain.NormalizedRelease{Provider: "patreon", ProviderReleaseID: "1", Title: "Harbor Chapter 2", Collections: []string{"Harbor"}, Enrichment: &domain.ReleaseEnrichment{NormalizerVersion: domain.NormalizerVersion, Collections: []domain.LabelReference{{Provider: "patreon", Campaign: "fictional", Type: "collection", ID: "new", Names: []string{"Harbor"}}}}}

	got := discovery.Analyze("fictional", []domain.NormalizedRelease{post}, nil, &cfg, nil, time.Now()).Decisions["1"]
	if got.Decision.SeriesID != "unmatched" || len(got.Explanation.HeldReasons) != 1 || got.Explanation.HeldReasons[0] != "unknown-collection" {
		t.Fatalf("new identity bypassed hold: %+v", got)
	}
}

func TestDeclaredBookLabelResolvesItsOwnTitleFamilyCandidate(t *testing.T) {
	zero := 0
	cfg := config.Config{Defaults: config.RuleDefaults{MinBodyChars: &zero}, Sources: []config.SourceConfig{{ID: "fictional"}}, Series: []config.SeriesConfig{{ID: "harbor", Title: "Harbor", Source: "fictional", Inputs: []config.SeriesInputConfig{{MatchType: "fallback"}}}}}

	post := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Arrival Chapter 1", Collections: []string{"Arrival"}}

	before := discovery.Detect("fictional", []domain.NormalizedRelease{post}, &cfg, nil, time.Now())
	if len(before) != 1 {
		t.Fatalf("new book was not reported: %+v", before)
	}
	cfg.Series[0].Books = []config.BookConfig{{ID: "arrival", Number: 1, Collection: &config.CollectionSelector{Name: "Arrival"}}}
	cfg.Rules = nil

	after := discovery.Replay("fictional", []domain.NormalizedRelease{post}, &cfg, before, time.Now())
	if len(after) != 1 || after[0].Status != "resolved" {
		t.Fatalf("declared book did not resolve candidate: %+v", after)
	}
}

func TestNumericBookCoincidenceDoesNotHideANewStoryInAFallback(t *testing.T) {
	cfg := config.Config{Series: []config.SeriesConfig{{ID: "harbor", Title: "Harbor", Source: "fictional", Books: []config.BookConfig{{ID: "arrival", Number: 1}}, Inputs: []config.SeriesInputConfig{{MatchType: "fallback"}}}}}

	post := domain.NormalizedRelease{ProviderReleaseID: "1", Title: "Star Garden Book 1 Chapter 1"}

	candidates := discovery.Detect("fictional", []domain.NormalizedRelease{post}, &cfg, nil, time.Now())
	if len(candidates) != 1 {
		t.Fatalf("numeric book coincidence hid new story: %+v", candidates)
	}
}
