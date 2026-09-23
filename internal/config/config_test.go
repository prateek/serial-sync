package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileRuleSetForSeries(t *testing.T) {
	t.Parallel()

	rs := (&Config{Series: []SeriesConfig{{
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
	}}}).CompileRuleSet()

	if got, want := len(rs.Rules()), 1; got != want {
		t.Fatalf("len(rules) = %d, want %d", got, want)
	}
	rule := rs.Rules()[0]
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
	if !rs.SeriesExists("alpha-saga") {
		t.Fatalf("rule set answers diverged: %+v", rs)
	}
}

func TestLoadRejectsInvalidReviewPatterns(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `[[auth_profiles]]
id="fixture"
provider="patreon"
mode="fixture"
[[sources]]
id="alpha"
provider="patreon"
url="https://www.patreon.com/c/alpha/posts"
auth_profile="fixture"
enabled=true
[[review]]
source="alpha"
title_patterns=["("]
reason="probe"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "review[1]") || !strings.Contains(err.Error(), "title_patterns") {
		t.Fatalf("Load accepted an invalid review pattern: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(body, `["("]`, `["^Note"]`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err != nil {
		t.Fatalf("valid review pattern rejected: %v", err)
	}
}

func TestCompiledTracksSourceDefaults(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Sources: []SourceConfig{{ID: "alpha", Defaults: RuleDefaults{ContentStrategy: "text_post"}}},
		Series:  []SeriesConfig{{ID: "harbor", Title: "Harbor", Inputs: []SeriesInputConfig{{Source: "alpha", MatchType: "fallback", ReleaseRole: "chapter"}}}},
	}
	if got := cfg.Compiled().ForSource("alpha")[0].Rule.ContentStrategy; got != "text_post" {
		t.Fatalf("source default not inherited: %q", got)
	}
	cfg.Sources[0].Defaults.ContentStrategy = "attachment_preferred"
	if got := cfg.Compiled().ForSource("alpha")[0].Rule.ContentStrategy; got != "attachment_preferred" {
		t.Fatalf("compiled set went stale after a source default changed: %q", got)
	}
}

func TestSeriesIndexOrderMustBeAuthorOrRelease(t *testing.T) {
	for value, valid := range map[string]bool{"": true, "author": true, "release": true, "chapter": false} {
		err := validateReaderOutput(SeriesConfig{Output: SeriesOutputConfig{SeriesIndex: value}}, nil)
		if (err == nil) != valid {
			t.Fatalf("series_index %q: err = %v", value, err)
		}
	}
}
