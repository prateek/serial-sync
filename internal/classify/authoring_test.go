package classify_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func loadAuthoring(t *testing.T, extra string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	data := `[[sources]]
id="fictional"
provider="patreon"
url="https://example.invalid/fictional"
author="Fictional Author"
enabled=true
` + extra
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestGroupedSelectorsAndGuardExplanations(t *testing.T) {
	cfg := loadAuthoring(t, `[[series]]
id="harbor"
title="Harbor"
source="fictional"
[[series.inputs]]
collections=["Harbor"]
tags=["harbor fiction"]
min_body_chars=10
unless_tags=["news"]
unless_title_patterns=["(?i)question"]
`)
	for _, tc := range []struct {
		name, title, body string
		tags, collections []string
		want              string
	}{
		{"boundary", "Prelude", "1234567890", []string{"harbor fiction"}, nil, "harbor"},
		{"too short", "Chapter 2", "123456789", nil, []string{"Harbor"}, "unmatched"},
		{"typo", "Harbr Chaper 8", "1234567890", nil, []string{"hArBoR"}, "harbor"},
		{"excluded tag", "News", strings.Repeat("x", 20), []string{"news"}, []string{"Harbor"}, "unmatched"},
		{"excluded title", "Questions", strings.Repeat("x", 20), nil, []string{"Harbor"}, "unmatched"},
		{"unicode entities", "Epilogue", "", nil, []string{"Harbor"}, "harbor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			post := domain.NormalizedRelease{ProviderReleaseID: "1", Title: tc.title, TextPlain: tc.body, Tags: tc.tags, Collections: tc.collections}
			if tc.name == "unicode entities" {
				post.TextHTML = "<p>ééééé &amp; 字字字</p><script>ignored</script>"
				post.TextPlain = "wrong"
			}
			got := classify.Explain("fictional", post, cfg.Compiled().ForSource("fictional"))
			if got.Decision.SeriesID != tc.want {
				t.Fatalf("decision: %+v", got)
			}
			if len(got.Explanation.Attempts) != 1 || len(got.Explanation.Attempts[0].Guards) != 3 {
				t.Fatalf("incomplete guard explanation: %+v", got)
			}
		})
	}
}

func TestOverridesAndReviewUseOneOrderedRuleModel(t *testing.T) {
	cfg := loadAuthoring(t, `[[overrides]]
source="fictional"
release_id="bonus"
series="harbor"
reason="Confirmed bonus story"
[[overrides]]
source="fictional"
release_id="exclude"
review=true
reason="Index only"
[[review]]
source="fictional"
priority=1
tags=["news"]
min_body_chars=0
reason="Administrative posts"
[[series]]
id="harbor"
title="Harbor"
source="fictional"
[[series.inputs]]
tags=["harbor"]
min_body_chars=0
`)
	for _, tc := range []struct{ id, want string }{{"normal", "unmatched"}, {"bonus", "harbor"}, {"exclude", "unmatched"}} {
		got := classify.Explain("fictional", domain.NormalizedRelease{ProviderReleaseID: tc.id, Tags: []string{"news", "harbor"}, TextPlain: strings.Repeat("x", 2000)}, cfg.Compiled().ForSource("fictional"))
		if got.Decision.SeriesID != tc.want {
			t.Fatalf("%s: %+v", tc.id, got)
		}
	}
}

func TestAuthoringDefaultsAndExplicitInputOverrides(t *testing.T) {
	cfg := loadAuthoring(t, `[sources.defaults]
format="epub"
content_strategy="attachment_only"
min_body_chars=12
attachment_glob=["*.pdf"]
attachment_priority=["pdf"]
[defaults]
format="preserve"
preface_mode="prepend_post"
release_role="extra"
[[series]]
id="harbor"
title="Harbor"
source="fictional"
[series.output]
format="preserve"
[[series.inputs]]
priority=10
title_patterns=["^Body"]
content_strategy="text_post"
format="epub"
min_body_chars=0
[[series.inputs]]
collections=["Harbor"]
`)
	cases := []struct {
		title, body, strategy, format string
		role                          domain.ReleaseRole
	}{{"Body prelude", "short", "text_post", "epub", domain.ReleaseRoleExtra}, {"Chapter 9", strings.Repeat("x", 12), "attachment_only", "preserve", domain.ReleaseRoleExtra}}
	for _, tc := range cases {
		got := classify.Decide("fictional", domain.NormalizedRelease{Title: tc.title, TextPlain: tc.body, Collections: []string{"Harbor"}, CreatorName: "Provider Author"}, cfg.Compiled().ForSource("fictional"))
		if got.SeriesID != "harbor" || string(got.ContentStrategy) != tc.strategy || string(got.OutputFormat) != tc.format || got.ReleaseRole != tc.role || got.CanonicalAuthor != "Fictional Author" || got.PrefaceMode != domain.PrefaceModePrependPost {
			t.Fatalf("inheritance for %s: %+v", tc.title, got)
		}
	}
}

func TestGroupedInputsDefaultGuardDoesNotChangeLegacy(t *testing.T) {
	cfg := loadAuthoring(t, `[[series]]
id="guarded"
title="Guarded"
source="fictional"
[[series.inputs]]
tags=["story"]
[[series]]
id="legacy"
title="Legacy"
source="fictional"
[[series.inputs]]
match_type="title_regex"
match_value="^Legacy"
release_role="chapter"
content_strategy="text_post"
`)
	for _, tc := range []struct{ title, body, want string }{{"Prelude", strings.Repeat("x", 1499), "unmatched"}, {"Prelude", strings.Repeat("x", 1500), "guarded"}, {"Legacy Chapter 1", "short", "legacy"}} {
		got := classify.Decide("fictional", domain.NormalizedRelease{Title: tc.title, TextPlain: tc.body, Tags: []string{"story"}}, cfg.Compiled().ForSource("fictional"))
		if got.SeriesID != tc.want {
			t.Fatalf("%s: %+v", tc.title, got)
		}
	}
}

func TestSourceOutputDefaultsAgreeWithVolumeValidation(t *testing.T) {
	cfg := loadAuthoring(t, `[sources.defaults]
format="epub"
[[series]]
id="harbor"
title="Harbor"
[series.output]
bundling="volume"
[[series.inputs]]
source="fictional"
tags=["harbor"]
`)
	if cfg.SeriesOutput(cfg.Series[0]).Format != "epub" {
		t.Fatal("volume output lost input source defaults")
	}
}

func TestGroupedLegacyRuleUsesSameBodyDefault(t *testing.T) {
	cfg := loadAuthoring(t, `[[rules]]
source="fictional"
track_key="harbor"
tags=["harbor"]
`)
	got := classify.Explain("fictional", domain.NormalizedRelease{Tags: []string{"harbor"}, TextPlain: "notice"}, cfg.Compiled().ForSource("fictional"))
	if got.Decision.SeriesID != "unmatched" {
		t.Fatalf("legacy location bypassed grouped guard: %+v", got)
	}
}

func TestFilenameSelectorPinsTheMatchedContent(t *testing.T) {
	cfg := loadAuthoring(t, `[[series]]
id="harbor"
title="Harbor"
source="fictional"
[[series.inputs]]
attachment_patterns=['^Harbor.*\.pdf$']
content_strategy="attachment_only"
attachment_priority=["epub","pdf"]
min_body_chars=0
`)
	post := domain.NormalizedRelease{Title: "New installment", Attachments: []domain.Attachment{{FileName: "Other.epub"}, {FileName: "Harbor Chapter 1.pdf"}}}
	decision := classify.Decide("fictional", post, cfg.Compiled().ForSource("fictional"))
	selected := classify.SelectContent(post, decision)
	if selected.FileName != "Harbor Chapter 1.pdf" {
		t.Fatalf("selector lost its matching file: %+v", selected)
	}
	file, ok := classify.SelectAttachment(post, decision)
	if !ok || file.FileName != selected.FileName {
		t.Fatalf("preparation selected another file: %+v", file)
	}
}

func TestCollectionIDSurvivesRenameAndNameSelectorStops(t *testing.T) {
	cfg := loadAuthoring(t, `[[series]]
id="harbor"
title="Harbor"
source="fictional"
[[series.inputs]]
collections=[{id="collection-1",name="Harbor"}]
min_body_chars=0
`)
	post := domain.NormalizedRelease{Provider: "patreon", Title: "Epilogue", Collections: []string{"Harbor [completed]"}, Enrichment: &domain.ReleaseEnrichment{NormalizerVersion: domain.NormalizerVersion, Collections: []domain.LabelReference{{Provider: "patreon", Campaign: "campaign-1", Type: "collection", ID: "collection-1", Names: []string{"Harbor [completed]"}}}}}
	got := classify.Explain("fictional", post, cfg.Compiled().ForSource("fictional"))
	if got.Decision.SeriesID != "harbor" || got.Explanation.Attempts[0].Selectors[0].ID != "collection-1" {
		t.Fatalf("ID lost rename: %+v", got)
	}
	cfg.Series[0].Inputs[0].Collections = []config.CollectionSelector{{Name: "Harbor"}}

	if decision := classify.Decide("fictional", post, cfg.Compiled().ForSource("fictional")); decision.SeriesID != "unmatched" {
		t.Fatalf("name silently matched rename: %+v", decision)
	}
}

func TestLabelTitleConflictKeepsOperatorOrder(t *testing.T) {
	cfg := loadAuthoring(t, `[[series]]
id="harbor"
title="Harbor"
source="fictional"
[[series.inputs]]
priority=10
collections=["Harbor"]
min_body_chars=0
[[series]]
id="tide"
title="Tide"
source="fictional"
[[series.inputs]]
priority=20
title_patterns=["^Tide"]
min_body_chars=0
`)
	post := domain.NormalizedRelease{Title: "Tide Chapter 3", Collections: []string{"Harbor"}}
	for _, want := range []string{"harbor", "tide"} {
		got := classify.Explain("fictional", post, cfg.Compiled().ForSource("fictional"))
		if got.Decision.SeriesID != want || len(got.Explanation.Conflicts) != 1 {
			t.Fatalf("lost order or conflict: %+v", got)
		}
		cfg.Series[1].Inputs[0].Priority = 1

	}
}

func TestBookLabelsSelectSeriesAndBookThroughTheNormalRuleModel(t *testing.T) {
	cfg := config.Config{Sources: []config.SourceConfig{{ID: "fictional", Provider: "patreon", URL: "https://example.invalid"}}, Series: []config.SeriesConfig{{ID: "harbor", Title: "Harbor", Source: "fictional", Books: []config.BookConfig{{ID: "arrival", Number: 1, Collection: &config.CollectionSelector{ID: "book-1", Name: "Old name"}}, {ID: "return", Number: 2, Tag: "Return"}}}}}

	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	release := domain.NormalizedRelease{Provider: "patreon", ProviderReleaseID: "1", Title: "Chapter 2", TextPlain: strings.Repeat("Filler ", 250), Collections: []string{"Renamed"}, Enrichment: &domain.ReleaseEnrichment{Collections: []domain.LabelReference{{Provider: "patreon", Type: "collection", ID: "book-1", Names: []string{"Renamed"}}}}}
	got := classify.Decide("fictional", release, cfg.Compiled().ForSource("fictional"))
	if got.SeriesID != "harbor" || got.BookID != "arrival" {
		t.Fatalf("collection did not select book: %+v", got)
	}
	release.Collections = nil
	release.Enrichment = nil
	release.Tags = []string{"return"}
	got = classify.Decide("fictional", release, cfg.Compiled().ForSource("fictional"))
	if got.BookID != "return" {
		t.Fatalf("tag did not select book: %+v", got)
	}
}

func TestSelectorMatchesStayAlignedAfterAnInvalidPattern(t *testing.T) {
	rules := (&config.Config{Rules: []config.RuleConfig{{Source: "alpha", TrackKey: "harbor", Selection: config.Selection{TitlePatterns: []string{"(", "^Harbor"}}, ReleaseRole: "chapter", ContentStrategy: "text_post"}}}).CompileRuleSet().ForSource("alpha")
	explained := classify.Explain("alpha", domain.NormalizedRelease{ProviderReleaseID: "p1", Title: "Harbor Chapter 1"}, rules)
	attempts := explained.Explanation.Attempts
	if len(attempts) != 1 || len(attempts[0].Selectors) != 1 || attempts[0].Selectors[0].Value != "^Harbor" {
		t.Fatalf("selector match reports the wrong authored pattern: %+v", attempts)
	}
}

func TestUnmatchedDecisionKeepsTheCreatorAsAuthor(t *testing.T) {
	explained := classify.Explain("alpha", domain.NormalizedRelease{ProviderReleaseID: "p1", Title: "Note", CreatorName: "Alpha Author"}, nil)
	if explained.Decision.Matched || explained.Decision.CanonicalAuthor != "Alpha Author" {
		t.Fatalf("unmatched decision lost the creator as author: %+v", explained.Decision)
	}
}
