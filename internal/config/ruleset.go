package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"sync"
)

// MinBodyCharsDefault is the one guard default. Grouped selectors inherit it
// when an input states no threshold, and discovery's advisory heuristics use
// the same number as their "substantial content" boundary.
const MinBodyCharsDefault = 1500

// CompiledSelectors holds the authored patterns once compiled; classification
// matches against these instead of recompiling per release.
type CompiledSelectors struct {
	Title            []*regexp.Regexp
	Attachments      []*regexp.Regexp
	UnlessTitle      []*regexp.Regexp
	LegacyTitle      *regexp.Regexp
	LegacyAttachment *regexp.Regexp
}

// CompiledRule pairs an authored rule with its compiled selectors. It is the
// routing unit the rule set hands to classification.
type CompiledRule struct {
	Rule      RuleConfig
	Selectors CompiledSelectors
}

// RuleSet is the compiled, read-only routing model, built once from the
// authored Config. Legacy rules, series inputs, review rules and overrides
// share one evaluation order (overrides first, then priority) with defaults
// and guards already applied; nothing mutates or recompiles it after
// construction. It answers the routing questions the rest of the program
// asks: which rules apply to a source, which labels a source references,
// whether a series exists, and whether any rule holds candidates.
type RuleSet struct {
	items    []CompiledRule
	bySource map[string][]int
	series   map[string]bool
}

// compiledMu guards every Config's memo so concurrent readers (dump workers,
// the daemon) never race on the cached set. Compilation is rare; the lock
// is held for the fingerprint check and the occasional recompile.
var compiledMu sync.Mutex

// Compiled is the one access path for the rule set. The compiled set is
// memoized against the authored inputs it derives from, so mutating the
// config can never leave the set stale: the next read recompiles.
func (c *Config) Compiled() RuleSet {
	fingerprint := c.compileFingerprint()
	compiledMu.Lock()
	defer compiledMu.Unlock()
	if c.compiled == nil || c.compiledFor != fingerprint {
		compiled := c.CompileRuleSet()
		c.compiled = &compiled
		c.compiledFor = fingerprint
	}
	return *c.compiled
}

func (c *Config) compileFingerprint() string {
	data, _ := json.Marshal([]any{c.Rules, c.Series, c.Review, c.Overrides, c.Defaults, c.Sources})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (c *Config) CompileRuleSet() RuleSet {
	rs := RuleSet{
		bySource: map[string][]int{},
		series:   map[string]bool{},
	}
	add := func(rule RuleConfig) {
		rs.items = append(rs.items, CompiledRule{Rule: rule, Selectors: compileSelectors(rule)})
		if rule.SeriesID != "" {
			rs.series[rule.SeriesID] = true
		}
		if rule.TrackKey != "" {
			rs.series[rule.TrackKey] = true
		}
	}
	for _, rule := range c.Rules {
		add(c.inheritRule(rule))
	}
	for _, rule := range c.compileSeriesRules() {
		add(rule)
	}
	for i, review := range c.Review {
		add(c.compileReviewRule(i, review))
	}
	for _, override := range c.Overrides {
		add(c.compileOverrideRule(override))
	}
	sort.SliceStable(rs.items, func(i, j int) bool {
		if rs.items[i].Rule.Override != rs.items[j].Rule.Override {
			return rs.items[i].Rule.Override
		}
		return rs.items[i].Rule.Priority < rs.items[j].Rule.Priority
	})
	rs.bySource = map[string][]int{}
	for i := range rs.items {
		if rule := rs.items[i].Rule; rule.Source != "" {
			rs.bySource[rule.Source] = append(rs.bySource[rule.Source], i)
		}
	}
	return rs
}

// compileReviewRule routes a review entry to the unmatched track as deliberate
// manual routing; a body threshold applies only when the entry requests one.
func (c *Config) compileReviewRule(index int, review ReviewConfig) RuleConfig {
	rule := c.compileInput(SeriesConfig{ID: "unmatched", Title: "Unmatched"}, SeriesInputConfig{Selection: review.Selection, Guards: review.Guards, Source: review.Source, Priority: review.Priority, ContentStrategy: "manual", ReleaseRole: "unknown"}, index)
	if review.MinBodyChars == nil {
		rule.MinBodyChars = nil
	}
	rule.SeriesID = ""
	rule.Reason = review.Reason
	return rule
}

// compileOverrideRule pins one release to a series or to review ahead of every
// other rule.
func (c *Config) compileOverrideRule(override OverrideConfig) RuleConfig {
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
	return rule
}

func compileSelectors(rule RuleConfig) CompiledSelectors {
	// A pattern that fails to compile keeps a nil placeholder so the compiled
	// slices stay index-aligned with the authored patterns they explain.
	compile := func(patterns []string) []*regexp.Regexp {
		out := make([]*regexp.Regexp, 0, len(patterns))
		for _, pattern := range patterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				re = nil
			}
			out = append(out, re)
		}
		return out
	}
	selectors := CompiledSelectors{
		Title:       compile(rule.TitlePatterns),
		Attachments: compile(rule.AttachmentPatterns),
		UnlessTitle: compile(rule.UnlessTitlePatterns),
	}
	if rule.MatchType == "title_regex" {
		if re, err := regexp.Compile(rule.MatchValue); err == nil {
			selectors.LegacyTitle = re
		}
	}
	if rule.MatchType == "attachment_filename_regex" {
		if re, err := regexp.Compile(rule.MatchValue); err == nil {
			selectors.LegacyAttachment = re
		}
	}
	return selectors
}

func (rs RuleSet) Rules() []RuleConfig {
	rules := make([]RuleConfig, 0, len(rs.items))
	for _, item := range rs.items {
		rules = append(rules, item.Rule)
	}
	return rules
}

func (rs RuleSet) ForSource(sourceID string) []CompiledRule {
	out := make([]CompiledRule, 0, len(rs.bySource[sourceID]))
	for _, index := range rs.bySource[sourceID] {
		out = append(out, rs.items[index])
	}
	return out
}

func (rs RuleSet) SeriesExists(seriesID string) bool {
	return rs.series[seriesID]
}

// SourcesRoutingTo lists the sources whose rules route releases into the
// series, whether authored as series inputs or as legacy rules.
func (rs RuleSet) SourcesRoutingTo(seriesID string) []string {
	seen := map[string]bool{}
	var sources []string
	for _, item := range rs.items {
		rule := item.Rule
		if rule.Source == "" || seen[rule.Source] || (rule.SeriesID != seriesID && rule.TrackKey != seriesID) {
			continue
		}
		seen[rule.Source] = true
		sources = append(sources, rule.Source)
	}
	return sources
}
