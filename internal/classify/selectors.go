package classify

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/textcontent"
)

func selectMatches(rule config.RuleConfig, release domain.NormalizedRelease) []domain.SelectorMatch {
	if rule.Override {
		if rule.ReleaseID == release.ProviderReleaseID {
			return []domain.SelectorMatch{{Kind: "release_id", Value: rule.ReleaseID}}
		}
		return nil
	}
	if rule.MatchType == "attachment_filename_regex" {
		pattern := rule.MatchValue
		rule.MatchType = ""
		rule.AttachmentPatterns = []string{pattern}
	}
	if rule.MatchType != "" {
		if matches(rule, release) {
			return []domain.SelectorMatch{{Kind: rule.MatchType, Value: rule.MatchValue}}
		}
		return nil
	}
	var matches []domain.SelectorMatch
	for _, collection := range rule.Collections {
		matched := false
		if collection.ID != "" {
			if release.Enrichment != nil {
				for _, ref := range release.Enrichment.Collections {
					if ref.ID == collection.ID && ref.Provider == release.Provider && ref.Type == "collection" {
						matched = true
						break
					}
				}
			}
		} else {
			matched = containsFold(release.Collections, collection.Name)
		}
		if matched {
			matches = append(matches, domain.SelectorMatch{Kind: "collection", Value: collection.Name, ID: collection.ID})
		}
	}

	for _, name := range rule.Tags {
		if containsFold(release.Tags, name) {
			matches = append(matches, domain.SelectorMatch{Kind: "tag", Value: name})
		}
	}
	for _, pattern := range rule.TitlePatterns {
		if matchesPattern(pattern, release.Title) {
			matches = append(matches, domain.SelectorMatch{Kind: "title_regex", Value: pattern})
		}
	}
	for _, pattern := range rule.AttachmentPatterns {
		for _, file := range release.Attachments {
			if matchesPattern(pattern, file.FileName) {
				matches = append(matches, domain.SelectorMatch{Kind: "attachment_filename_regex", Value: pattern, Attachment: file.FileName})
			}
		}
	}
	return matches
}

func evaluateGuards(guards config.Guards, release domain.NormalizedRelease) []domain.GuardResult {
	var result []domain.GuardResult
	if guards.MinBodyChars != nil {
		actual := textcontent.Count(release)
		result = append(result, domain.GuardResult{Field: "min_body_chars", Passed: actual >= *guards.MinBodyChars, Detail: fmt.Sprintf("%d visible characters; requires %d", actual, *guards.MinBodyChars)})
	}
	if len(guards.UnlessTags) > 0 {
		var found []string
		for _, tag := range guards.UnlessTags {
			if containsFold(release.Tags, tag) {
				found = append(found, tag)
			}
		}
		result = append(result, domain.GuardResult{Field: "unless_tags", Passed: len(found) == 0, Detail: strings.Join(found, ", ")})
	}
	if len(guards.UnlessTitlePatterns) > 0 {
		var found []string
		for _, pattern := range guards.UnlessTitlePatterns {
			if matchesPattern(pattern, release.Title) {
				found = append(found, pattern)
			}
		}
		result = append(result, domain.GuardResult{Field: "unless_title_patterns", Passed: len(found) == 0, Detail: strings.Join(found, ", ")})
	}
	return result
}
func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}
func matchesPattern(pattern, value string) bool {
	re, err := regexp.Compile(pattern)
	return err == nil && re.MatchString(value)
}
