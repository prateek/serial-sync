package classify

import (
	"fmt"
	"strings"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/textcontent"
)

func selectMatches(compiled config.CompiledRule, release domain.NormalizedRelease) []domain.SelectorMatch {
	rule := compiled.Rule
	if rule.Override {
		if rule.ReleaseID == release.ProviderReleaseID {
			return []domain.SelectorMatch{{Kind: "release_id", Value: rule.ReleaseID}}
		}
		return nil
	}
	if rule.MatchType == "attachment_filename_regex" && compiled.Selectors.LegacyAttachment != nil {
		var matches []domain.SelectorMatch
		for _, file := range release.Attachments {
			if compiled.Selectors.LegacyAttachment.MatchString(file.FileName) {
				matches = append(matches, domain.SelectorMatch{Kind: "attachment_filename_regex", Value: rule.MatchValue, Attachment: file.FileName})
			}
		}
		return matches
	}
	if rule.MatchType != "" {
		if matches(compiled, release) {
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
	for i := range compiled.Selectors.Title {
		if compiled.Selectors.Title[i] != nil && compiled.Selectors.Title[i].MatchString(release.Title) {
			matches = append(matches, domain.SelectorMatch{Kind: "title_regex", Value: rule.TitlePatterns[i]})
		}
	}
	for i := range compiled.Selectors.Attachments {
		if compiled.Selectors.Attachments[i] == nil {
			continue
		}
		for _, file := range release.Attachments {
			if compiled.Selectors.Attachments[i].MatchString(file.FileName) {
				matches = append(matches, domain.SelectorMatch{Kind: "attachment_filename_regex", Value: rule.AttachmentPatterns[i], Attachment: file.FileName})
			}
		}
	}
	return matches
}

func evaluateGuards(guards config.Guards, selectors config.CompiledSelectors, release domain.NormalizedRelease) []domain.GuardResult {
	var result []domain.GuardResult
	if guards.MinBodyChars != nil {
		actual := textcontent.Count(release)
		result = append(result, domain.GuardResult{Field: "min_body_chars", Passed: actual >= *guards.MinBodyChars, Detail: fmt.Sprintf("%d visible characters; requires %d", actual, *guards.MinBodyChars)})
	}
	if len(guards.UnlessTags) > 0 {
		var found []string
		for _, tag := range release.Tags {
			if containsFold(guards.UnlessTags, tag) {
				found = append(found, tag)
			}
		}
		result = append(result, domain.GuardResult{Field: "unless_tags", Passed: len(found) == 0, Detail: strings.Join(found, ", ")})
	}
	if len(selectors.UnlessTitle) > 0 {
		var matched []string
		for i := range selectors.UnlessTitle {
			if selectors.UnlessTitle[i] != nil && selectors.UnlessTitle[i].MatchString(release.Title) {
				matched = append(matched, guards.UnlessTitlePatterns[i])
			}
		}
		result = append(result, domain.GuardResult{Field: "unless_title_patterns", Passed: len(matched) == 0, Detail: strings.Join(matched, ", ")})
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
