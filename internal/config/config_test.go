package config

import "testing"

func TestCompileSeriesRules(t *testing.T) {
	t.Parallel()

	rules := CompileSeriesRules([]SeriesConfig{{
		ID:      "alpha-saga",
		Title:   "Alpha Saga",
		Authors: []string{"Alpha Author"},
		Output: SeriesOutputConfig{
			Format:      "epub",
			PrefaceMode: "prepend_post",
		},
		Inputs: []SeriesInputConfig{{
			Source:          "alpha",
			Priority:        10,
			MatchType:       "title_regex",
			MatchValue:      "^Alpha Saga",
			ReleaseRole:     "chapter",
			ContentStrategy: "text_post",
		}},
	}})

	if got, want := len(rules), 1; got != want {
		t.Fatalf("len(rules) = %d, want %d", got, want)
	}
	rule := rules[0]
	if got, want := rule.TrackKey, "alpha-saga"; got != want {
		t.Fatalf("rule.TrackKey = %q, want %q", got, want)
	}
	if got, want := rule.OutputFormat, "epub"; got != want {
		t.Fatalf("rule.OutputFormat = %q, want %q", got, want)
	}
	if got, want := rule.PrefaceMode, "prepend_post"; got != want {
		t.Fatalf("rule.PrefaceMode = %q, want %q", got, want)
	}
	if got, want := rule.CanonicalAuthor, "Alpha Author"; got != want {
		t.Fatalf("rule.CanonicalAuthor = %q, want %q", got, want)
	}
}
