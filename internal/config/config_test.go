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

func TestExampleConfigStartsWithoutFetchingOrMapping(t *testing.T) {
	for _, container := range []string{"true", "false"} {
		t.Run(container, func(t *testing.T) {
			t.Setenv(ContainerModeEnv, container)
			dir := t.TempDir()
			t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(ExampleConfig()), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, roots, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Sources) != 0 || len(cfg.Series) != 0 || len(cfg.Compiled().Rules()) != 0 {
				t.Fatalf("starter must wait for dump authoring: sources=%d series=%d rules=%d", len(cfg.Sources), len(cfg.Series), len(cfg.Compiled().Rules()))
			}
			if len(cfg.AuthProfiles) != 1 || len(cfg.Publishers) != 1 {
				t.Fatalf("starter needs one auth profile and publisher: %+v", cfg)
			}
			if cfg.AuthProfiles[0].SessionPath != filepath.Join(roots.StateDir, "sessions", "patreon-default.json") || cfg.Publishers[0].Path != filepath.Join(roots.StateDir, "published") {
				t.Fatalf("starter paths must use runtime roots: %+v", cfg)
			}
			if _, err := os.Stat(filepath.Join(dir, "state")); !os.IsNotExist(err) {
				t.Fatalf("strict loading created state: %v", err)
			}
		})
	}
}

func TestExampleConfigGroupedMappingUsesCurrentDefaults(t *testing.T) {
	t.Setenv(ContainerModeEnv, "false")
	body := ExampleConfig()
	start := strings.Index(body, "# [[series]]")
	end := strings.Index(body, "# Grouped inputs inherit")
	if start < 0 || end <= start {
		t.Fatal("starter must show a grouped mapping after dumping a source")
	}
	mapping := strings.ReplaceAll(body[start:end], "# ", "")
	body += `
[[sources]]
id = "source-id-from-dump"
provider = "patreon"
url = "https://example.invalid/posts"
auth_profile = "patreon-default"
enabled = true
` + mapping
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rules := cfg.Compiled().Rules()
	if len(rules) != 1 {
		t.Fatalf("expected one grouped story input, got %d", len(rules))
	}
	rule := rules[0]
	if rule.ContentStrategy != "text_post" || rule.ReleaseRole != "chapter" || rule.OutputFormat != "epub" || rule.MinBodyChars == nil || *rule.MinBodyChars != MinBodyCharsDefault {
		t.Fatalf("unexpected inherited policy for the starter mapping: %+v", rule)
	}
}
