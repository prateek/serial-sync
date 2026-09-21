package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

func decodeStrict(path string, data []byte, target any) error {
	err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(target)
	var unknown *toml.StrictMissingError
	if errors.As(err, &unknown) {
		var fields []string
		for _, field := range unknown.Errors {
			row, _ := field.Position()
			fields = append(fields, fmt.Sprintf("%s (line %d)", strings.Join(field.Key(), "."), row))
		}
		return fmt.Errorf("%s: unknown field(s) %s; remove them or correct their spelling", path, strings.Join(fields, ", "))
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := ValidateExplicitOutputFields(data); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func LoadSeries(path string, sources []SourceConfig) ([]SeriesConfig, error) {
	return LoadSeriesWithDefaults(path, sources, RuleDefaults{})
}

func LoadSeriesWithDefaults(path string, sources []SourceConfig, defaults RuleDefaults) ([]SeriesConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document struct {
		Series []SeriesConfig `toml:"series"`
	}
	if err := decodeStrict(path, data, &document); err != nil {
		return nil, err
	}
	if err := validateRuleDefaults("defaults", defaults); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg := Config{Sources: sources, Series: document.Series, Defaults: defaults}
	if err := cfg.ValidateSeries(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg.Series, nil
}

func LoadSources(path string) ([]SourceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document struct {
		Sources []SourceConfig `toml:"sources"`
	}
	if err := decodeStrict(path, data, &document); err != nil {
		return nil, err
	}
	if _, err := validateSources(document.Sources); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return document.Sources, nil
}

func validateSources(sources []SourceConfig) (map[string]struct{}, error) {
	ids := map[string]struct{}{}
	for _, source := range sources {
		if err := validateRuleDefaults("source "+source.ID+" defaults", source.Defaults); err != nil {
			return nil, err
		}
		if strings.TrimSpace(source.ID) == "" {
			return nil, fmt.Errorf("source id is required")
		}
		if _, exists := ids[source.ID]; exists {
			return nil, fmt.Errorf("duplicate source id %q", source.ID)
		}
		ids[source.ID] = struct{}{}
		if source.Provider == "" {
			return nil, fmt.Errorf("source %q provider is required", source.ID)
		}
		if source.URL == "" {
			return nil, fmt.Errorf("source %q url is required", source.ID)
		}
	}
	return ids, nil
}

func (c *Config) validateRuleReferences(rule RuleConfig) error {
	if rule.SeriesID == "" && rule.BookID == "" {
		return nil
	}
	seriesID := firstNonEmptyString(rule.SeriesID, rule.TrackKey)
	for _, series := range c.Series {
		if series.ID != seriesID {
			continue
		}
		if series.Output.Bundling == "volume" && rule.OutputFormat != "epub" {
			return fmt.Errorf("series %q volume output requires effective format = epub", series.ID)
		}
		if rule.BookID == "" {
			return nil
		}
		for _, book := range series.Books {
			if book.ID == rule.BookID {
				return nil
			}
		}
		return fmt.Errorf("book_id %q is unknown in series %q; declare the book or correct the reference", rule.BookID, seriesID)
	}
	if rule.SeriesID == "" {
		return fmt.Errorf("book_id %q requires a declared series %q; declare the series or correct track_key", rule.BookID, seriesID)
	}
	return fmt.Errorf("series_id %q is unknown; declare the series or correct the reference", rule.SeriesID)
}

func validateRuleFields(rule RuleConfig) error {
	if rule.Selection.Present() && (rule.MatchType != "" || rule.MatchValue != "") {
		return fmt.Errorf("selector lists cannot be mixed with match_type or match_value; choose one form")
	}
	if rule.MinBodyChars != nil && *rule.MinBodyChars < 0 {
		return fmt.Errorf("min_body_chars must be zero or positive")
	}
	for field, patterns := range map[string][]string{"title_patterns": rule.TitlePatterns, "attachment_patterns": rule.AttachmentPatterns, "unless_title_patterns": rule.UnlessTitlePatterns} {
		for _, pattern := range patterns {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("%s %q is invalid: %w", field, pattern, err)
			}
		}
	}
	for _, collection := range rule.Collections {
		if strings.TrimSpace(collection.Name) == "" && strings.TrimSpace(collection.ID) == "" {
			return fmt.Errorf("collections entries require name or id")
		}
	}
	for field, names := range map[string][]string{"tags": rule.Tags, "unless_tags": rule.UnlessTags} {
		for _, name := range names {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("%s entries must not be empty", field)
			}
		}
	}
	if err := validateOutputFormat(rule.OutputFormat); err != nil {
		return fmt.Errorf("format: %w", err)
	}
	if err := validatePrefaceMode(rule.PrefaceMode); err != nil {
		return fmt.Errorf("preface_mode: %w", err)
	}
	switch rule.MatchType {
	case "":
		if !rule.Selection.Present() {
			return fmt.Errorf("input requires match_type or selector lists")
		}
	case "title_regex", "attachment_filename_regex":
		if _, err := regexp.Compile(rule.MatchValue); err != nil {
			return fmt.Errorf("match_value is not a valid regular expression: %w; correct the pattern", err)
		}
	case "tag", "collection":
		if strings.TrimSpace(rule.MatchValue) == "" {
			return fmt.Errorf("match_value must name a %s", rule.MatchType)
		}
	case "fallback", "fallback_default":
	default:
		return fmt.Errorf("match_type %q is unsupported; use tag, collection, title_regex, attachment_filename_regex, or fallback", rule.MatchType)
	}
	switch rule.ReleaseRole {
	case "chapter", "extra", "release_attachment", "announcement", "schedule", "preview_bundle", "unknown":
	default:
		return fmt.Errorf("release_role %q is unsupported; use chapter, extra, release_attachment, announcement, schedule, preview_bundle, or unknown", rule.ReleaseRole)
	}
	switch rule.ContentStrategy {
	case "text_post", "attachment_preferred", "attachment_only", "text_plus_attachments", "manual":
	default:
		return fmt.Errorf("content_strategy %q is unsupported; use text_post, attachment_preferred, attachment_only, text_plus_attachments, or manual", rule.ContentStrategy)
	}
	for _, pattern := range rule.AttachmentGlob {
		if _, err := filepath.Match(pattern, ""); err != nil {
			return fmt.Errorf("attachment_glob %q is invalid: %w; correct the glob", pattern, err)
		}
	}
	return nil
}

func (c *Config) Warnings() []string {
	var warnings []string
	rules := append([]RuleConfig(nil), c.Rules...)
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
	fallbacks := map[string]bool{}
	selectors := map[string]string{}
	for _, rule := range rules {
		if rule.Override {
			continue
		}
		if fallbacks[rule.Source] {
			warnings = append(warnings, fmt.Sprintf("source %s: input for %s follows a fallback and cannot match; move the fallback below specific inputs", rule.Source, rule.TrackKey))
		}
		selection, _ := json.Marshal(rule.Selection)
		selector := strings.Join([]string{rule.Source, rule.MatchType, rule.MatchValue, string(selection)}, "\x00")
		if previous, ok := selectors[selector]; ok {
			warnings = append(warnings, fmt.Sprintf("source %s: %s and %s have identical selectors; the earlier input wins", rule.Source, previous, rule.TrackKey))
		}
		selectors[selector] = rule.TrackKey
		if (rule.MatchType == "fallback" || rule.MatchType == "fallback_default") && rule.MinBodyChars == nil && len(rule.UnlessTags) == 0 && len(rule.UnlessTitlePatterns) == 0 {
			fallbacks[rule.Source] = true
		}

		if rule.AnthologyMode != nil {
			warnings = append(warnings, fmt.Sprintf("anthology_mode is deprecated for source %s; remove it and configure series.output.bundling when needed", rule.Source))
		}
	}
	return warnings
}

func hashConfig(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func (c *Config) Fingerprint() string {
	if c.fileHash != "" {
		return c.fileHash
	}
	data, _ := json.Marshal(c)
	return hashConfig(data)
}
