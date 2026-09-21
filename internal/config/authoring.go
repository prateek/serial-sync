package config

import (
	"fmt"
	"strings"
)

type Selection struct {
	Collections        []CollectionSelector `toml:"collections"`
	Tags               []string             `toml:"tags"`
	TitlePatterns      []string             `toml:"title_patterns"`
	AttachmentPatterns []string             `toml:"attachment_patterns"`
}

func (s Selection) Present() bool {
	return len(s.Collections)+len(s.Tags)+len(s.TitlePatterns)+len(s.AttachmentPatterns) > 0
}

type Guards struct {
	HoldCandidates      bool     `toml:"hold_candidates"`
	MinBodyChars        *int     `toml:"min_body_chars"`
	UnlessTags          []string `toml:"unless_tags"`
	UnlessTitlePatterns []string `toml:"unless_title_patterns"`
}

type RuleDefaults struct {
	Format             string   `toml:"format"`
	PrefaceMode        string   `toml:"preface_mode"`
	ReleaseRole        string   `toml:"release_role"`
	ContentStrategy    string   `toml:"content_strategy"`
	AttachmentGlob     []string `toml:"attachment_glob"`
	AttachmentPriority []string `toml:"attachment_priority"`
	MinBodyChars       *int     `toml:"min_body_chars"`
}

type ReviewConfig struct {
	Selection
	Guards
	Source   string `toml:"source"`
	Priority int    `toml:"priority"`
	Reason   string `toml:"reason"`
}

type OverrideConfig struct {
	Source    string `toml:"source"`
	ReleaseID string `toml:"release_id"`
	Series    string `toml:"series"`
	Review    bool   `toml:"review"`
	Reason    string `toml:"reason"`
}

func overlay(base, next RuleDefaults) RuleDefaults {
	base.Format = firstNonEmptyString(next.Format, base.Format)
	base.PrefaceMode = firstNonEmptyString(next.PrefaceMode, base.PrefaceMode)
	base.ReleaseRole = firstNonEmptyString(next.ReleaseRole, base.ReleaseRole)
	base.ContentStrategy = firstNonEmptyString(next.ContentStrategy, base.ContentStrategy)
	if next.AttachmentGlob != nil {
		base.AttachmentGlob = next.AttachmentGlob
	}
	if next.AttachmentPriority != nil {
		base.AttachmentPriority = next.AttachmentPriority
	}
	if next.MinBodyChars != nil {
		base.MinBodyChars = next.MinBodyChars
	}
	return base
}

func (c *Config) sourceDefaults(source string) RuleDefaults {
	defaults := overlay(RuleDefaults{Format: "preserve", PrefaceMode: "none", ReleaseRole: "chapter", ContentStrategy: "text_post"}, c.Defaults)
	if src, ok := c.SourceByID(source); ok {
		defaults = overlay(defaults, src.Defaults)
	}
	return defaults
}

func (c *Config) SeriesOutput(series SeriesConfig) SeriesOutputConfig {
	output := series.Output
	defaults := c.sourceDefaults(series.Source)
	for i, input := range series.AuthoringInputs() {
		rule := c.compileInput(series, input, i)
		if i == 0 {
			defaults.Format = rule.OutputFormat
			defaults.PrefaceMode = rule.PrefaceMode
		} else {
			if defaults.Format != rule.OutputFormat {
				defaults.Format = c.sourceDefaults(series.Source).Format
			}
			if defaults.PrefaceMode != rule.PrefaceMode {
				defaults.PrefaceMode = c.sourceDefaults(series.Source).PrefaceMode
			}
		}
	}
	output.Format = firstNonEmptyString(output.Format, defaults.Format)
	output.PrefaceMode = firstNonEmptyString(output.PrefaceMode, defaults.PrefaceMode)
	return SeriesOutputDefaults(output)
}

func (c *Config) compileInput(series SeriesConfig, input SeriesInputConfig, index int) RuleConfig {
	priority := input.Priority
	if priority == 0 {
		priority = 10 + index*10
	}
	return c.inheritRule(RuleConfig{
		Selection: input.Selection, Guards: input.Guards,
		BookID: input.BookID, SeriesID: series.ID,
		Source: firstNonEmptyString(input.Source, series.Source), Priority: priority,
		MatchType: input.MatchType, MatchValue: input.MatchValue,
		TrackKey: series.ID, TrackName: series.Title,
		CanonicalAuthor: firstNonEmptyString(series.Authors...),
		ReleaseRole:     input.ReleaseRole, ContentStrategy: input.ContentStrategy,
		OutputFormat:   firstNonEmptyString(input.Format, series.Output.Format),
		PrefaceMode:    firstNonEmptyString(input.PrefaceMode, series.Output.PrefaceMode),
		AttachmentGlob: input.AttachmentGlob, AttachmentPriority: input.AttachmentPriority,
		AnthologyMode: input.AnthologyMode,
	})
}

func (c *Config) inheritRule(rule RuleConfig) RuleConfig {
	defaults := c.sourceDefaults(rule.Source)
	rule.ReleaseRole = firstNonEmptyString(rule.ReleaseRole, defaults.ReleaseRole)
	rule.ContentStrategy = firstNonEmptyString(rule.ContentStrategy, defaults.ContentStrategy)
	rule.OutputFormat = firstNonEmptyString(rule.OutputFormat, defaults.Format)
	rule.PrefaceMode = firstNonEmptyString(rule.PrefaceMode, defaults.PrefaceMode)
	if rule.MinBodyChars == nil {
		rule.MinBodyChars = defaults.MinBodyChars
	}
	if rule.MinBodyChars == nil && rule.Selection.Present() {
		n := 1500
		rule.MinBodyChars = &n
	}
	if rule.AttachmentGlob == nil {
		rule.AttachmentGlob = defaults.AttachmentGlob
	}
	if rule.AttachmentPriority == nil {
		rule.AttachmentPriority = defaults.AttachmentPriority
	}
	if source, ok := c.SourceByID(rule.Source); ok {
		rule.CanonicalAuthor = firstNonEmptyString(rule.CanonicalAuthor, source.Author)
	}
	return rule
}

func (c *Config) compileSeriesRules() []RuleConfig {
	var rules []RuleConfig
	for _, series := range c.Series {
		for i, input := range series.AuthoringInputs() {
			rules = append(rules, c.compileInput(series, input, i))
		}
	}
	return rules
}

// CompileRules compiles the authored document once. Its result replaces Rules.
func (c *Config) CompileRules() []RuleConfig {
	rules := make([]RuleConfig, 0, len(c.Rules))
	for _, rule := range c.Rules {
		rules = append(rules, c.inheritRule(rule))
	}

	rules = append(rules, c.compileSeriesRules()...)
	for i, review := range c.Review {
		rule := c.compileInput(SeriesConfig{ID: "unmatched", Title: "Unmatched"}, SeriesInputConfig{Selection: review.Selection, Guards: review.Guards, Source: review.Source, Priority: review.Priority, ContentStrategy: "manual", ReleaseRole: "unknown"}, i)
		// Review is deliberate routing; a body threshold applies only when requested.
		if review.MinBodyChars == nil {
			rule.MinBodyChars = nil
		}
		rule.SeriesID = ""
		rule.Reason = review.Reason
		rules = append(rules, rule)
	}
	for _, override := range c.Overrides {
		target := SeriesConfig{ID: "unmatched", Title: "Unmatched"}
		for _, series := range c.Series {
			if series.ID == override.Series {
				target = series
				break
			}
		}
		input := SeriesInputConfig{Source: override.Source, MatchType: "fallback"}
		if override.Review {
			input.ContentStrategy = "manual"
			input.ReleaseRole = "unknown"
		}
		rule := c.compileInput(target, input, 0)
		if override.Review {
			rule.SeriesID = ""
		}
		rule.MinBodyChars = nil
		rule.Override = true
		rule.ReleaseID = override.ReleaseID
		rule.Reason = override.Reason
		rules = append(rules, rule)
	}
	return rules
}

func (c *Config) validateAuthoring() error {

	if err := validateRuleDefaults("defaults", c.Defaults); err != nil {
		return err
	}
	for _, source := range c.Sources {
		if err := validateRuleDefaults("source "+source.ID+" defaults", source.Defaults); err != nil {
			return err
		}
	}
	for i, review := range c.Review {
		if strings.TrimSpace(review.Reason) == "" {
			return fmt.Errorf("review[%d] reason is required", i+1)
		}
	}
	seen := map[string]bool{}
	for i, override := range c.Overrides {
		owner := fmt.Sprintf("overrides[%d]", i+1)
		if strings.TrimSpace(override.Reason) == "" {
			return fmt.Errorf("%s reason is required", owner)
		}
		if _, ok := c.SourceByID(override.Source); !ok {
			return fmt.Errorf("%s source %q is unknown", owner, override.Source)
		}
		key := override.Source + "/" + override.ReleaseID
		if strings.TrimSpace(override.ReleaseID) == "" || seen[key] {
			return fmt.Errorf("%s release_id must be present and unique per source", owner)
		}
		seen[key] = true
		if (override.Series != "") == override.Review {
			return fmt.Errorf("%s must set either series or review = true", owner)
		}
		if override.Series != "" {
			found := false
			for _, series := range c.Series {
				if series.ID == override.Series {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%s series %q is unknown", owner, override.Series)
			}
		}
	}
	return nil
}

func validateRuleDefaults(owner string, defaults RuleDefaults) error {
	d := overlay(RuleDefaults{Format: "preserve", PrefaceMode: "none", ReleaseRole: "chapter", ContentStrategy: "text_post"}, defaults)
	err := validateRuleFields(RuleConfig{MatchType: "fallback", ReleaseRole: d.ReleaseRole, ContentStrategy: d.ContentStrategy, OutputFormat: d.Format, PrefaceMode: d.PrefaceMode, AttachmentGlob: d.AttachmentGlob, Guards: Guards{MinBodyChars: d.MinBodyChars}})
	if err != nil {
		return fmt.Errorf("%s: %w", owner, err)
	}
	return nil
}

type CollectionSelector struct {
	ID   string `toml:"id,omitempty" json:"id,omitempty"`
	Name string `toml:"name,omitempty" json:"name,omitempty"`
}

func (s *CollectionSelector) UnmarshalText(text []byte) error { s.Name = string(text); return nil }

// AuthoringInputs expands book labels into ordinary ordered inputs. Book labels
// precede explicit inputs at priority 10; lower priorities can override them.
func (series SeriesConfig) AuthoringInputs() []SeriesInputConfig {
	inputs := make([]SeriesInputConfig, 0, len(series.Books)+len(series.Inputs))
	for _, book := range series.Books {
		input := SeriesInputConfig{Source: series.Source, BookID: book.ID, Priority: 10}
		if book.Collection != nil {
			input.Collections = []CollectionSelector{*book.Collection}
		}
		if book.Tag != "" {
			input.Tags = []string{book.Tag}
		}
		if input.Selection.Present() {
			inputs = append(inputs, input)
		}
	}
	for i, input := range series.Inputs {
		if input.Priority == 0 {
			input.Priority = 10 + i*10
		}
		inputs = append(inputs, input)
	}
	return inputs
}
