package classify

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

type ExplainedDecision struct {
	Decision    domain.TrackDecision
	Rule        *config.RuleConfig         `json:"-"`
	Explanation domain.DecisionExplanation `json:"explanation"`
}

func Decide(sourceID string, release domain.NormalizedRelease, rules []config.CompiledRule) domain.TrackDecision {
	return Explain(sourceID, release, rules).Decision
}

func Explain(sourceID string, release domain.NormalizedRelease, rules []config.CompiledRule) ExplainedDecision {
	result := ExplainedDecision{Decision: reviewDecision(sourceID)}
	for idx, compiled := range rules {
		rule := compiled.Rule
		if rule.Source != sourceID {
			continue
		}
		selectors := selectMatches(compiled, release)
		attempt := domain.RuleAttempt{Input: ruleID(sourceID, idx, rule), Series: fallback(rule.SeriesID, rule.TrackKey), Selectors: selectors, Reason: rule.Reason, Matched: len(selectors) > 0}
		if attempt.Matched {
			attempt.Guards = evaluateGuards(rule.Guards, compiled.Selectors, release)
			for _, guard := range attempt.Guards {
				if !guard.Passed {
					attempt.Matched = false
				}
			}
		}

		attempt.Selected = attempt.Matched && result.Rule == nil
		result.Explanation.Attempts = append(result.Explanation.Attempts, attempt)
		if !attempt.Matched {
			continue
		}
		if result.Rule != nil {
			result.Explanation.Overlaps = append(result.Explanation.Overlaps, attempt.Input)
			continue
		}
		copied := rule
		result.Rule = &copied
		result.Decision = domain.TrackDecision{
			BookID:             rule.BookID,
			TrackKey:           rule.TrackKey,
			TrackName:          fallback(rule.TrackName, rule.TrackKey),
			SeriesID:           fallback(rule.SeriesID, rule.TrackKey),
			RuleID:             ruleID(sourceID, idx, rule),
			ReleaseRole:        domain.ReleaseRole(rule.ReleaseRole),
			ContentStrategy:    domain.ContentStrategy(rule.ContentStrategy),
			OutputFormat:       domain.OutputFormat(fallback(rule.OutputFormat, string(domain.OutputFormatPreserve))),
			PrefaceMode:        domain.PrefaceMode(fallback(rule.PrefaceMode, string(domain.PrefaceModeNone))),
			CanonicalAuthor:    fallback(rule.CanonicalAuthor, release.CreatorName),
			AttachmentGlob:     append([]string(nil), rule.AttachmentGlob...),
			AttachmentPriority: append([]string(nil), rule.AttachmentPriority...),
			Matched:            true,
		}
	}
	if result.Decision.CanonicalAuthor == "" {
		result.Decision.CanonicalAuthor = release.CreatorName
	}
	selection := SelectContent(release, result.Decision)
	if result.Rule != nil && (result.Decision.ContentStrategy == domain.ContentStrategyAttachmentOnly || result.Decision.ContentStrategy == domain.ContentStrategyAttachmentPreferred) {
		var matched []domain.Attachment
		for _, attempt := range result.Explanation.Attempts {
			if attempt.Selected {
				for _, file := range release.Attachments {
					for _, selector := range attempt.Selectors {
						if selector.Attachment == file.FileName {
							matched = append(matched, file)
							break
						}
					}
				}
			}
		}
		if len(matched) > 0 {
			selection = domain.ContentReference{Kind: "none"}
			subset := release
			subset.Attachments = matched
			if file, ok := SelectAttachment(subset, result.Decision); ok {
				selection = attachmentReference(release, file)
			}
		}
	}
	result.Decision.SelectedContent = &selection
	if result.Rule == nil || !result.Rule.Override {
		result.Explanation.Conflicts = decisionConflicts(result.Explanation.Attempts)
	}
	return result
}

func reviewDecision(sourceID string) domain.TrackDecision {
	return domain.TrackDecision{
		TrackKey:        "unmatched",
		TrackName:       "Unmatched",
		SeriesID:        "unmatched",
		RuleID:          "fallback/" + sourceID + "/unmatched",
		ReleaseRole:     domain.ReleaseRoleUnknown,
		ContentStrategy: domain.ContentStrategyManual,
		OutputFormat:    domain.OutputFormatPreserve,
		PrefaceMode:     domain.PrefaceModeNone,
		Matched:         false,
	}
}

func matches(compiled config.CompiledRule, release domain.NormalizedRelease) bool {
	rule := compiled.Rule
	switch rule.MatchType {
	case "tag":
		for _, tag := range release.Tags {
			if strings.EqualFold(tag, rule.MatchValue) {
				return true
			}
		}
		return false
	case "collection":
		for _, collection := range release.Collections {
			if strings.EqualFold(collection, rule.MatchValue) {
				return true
			}
		}
		return false
	case "title_regex":
		return compiled.Selectors.LegacyTitle != nil && compiled.Selectors.LegacyTitle.MatchString(release.Title)
	case "attachment_filename_regex":
		if compiled.Selectors.LegacyAttachment == nil {
			return false
		}
		for _, attachment := range release.Attachments {
			if compiled.Selectors.LegacyAttachment.MatchString(attachment.FileName) {
				return true
			}
		}
		return false
	case "fallback", "fallback_default":
		return true
	default:
		return false
	}
}

func SelectAttachment(release domain.NormalizedRelease, decision domain.TrackDecision) (domain.Attachment, bool) {
	if ref := decision.SelectedContent; ref != nil {
		if ref.Kind != "attachment" || ref.AttachmentIndex < 0 || ref.AttachmentIndex >= len(release.Attachments) {
			return domain.Attachment{}, false
		}
		file := release.Attachments[ref.AttachmentIndex]
		return file, file.FileName == ref.FileName && (ref.SHA256 == "" || file.SHA256 == "" || ref.SHA256 == file.SHA256)
	}

	var candidates []domain.Attachment
	if len(decision.AttachmentGlob) == 0 {
		candidates = append(candidates, release.Attachments...)
	} else {
		for _, attachment := range release.Attachments {
			for _, pattern := range decision.AttachmentGlob {
				if ok, _ := filepath.Match(pattern, attachment.FileName); ok {
					candidates = append(candidates, attachment)
					break
				}
			}
		}
	}
	if len(candidates) == 0 {
		return domain.Attachment{}, false
	}
	if len(decision.AttachmentPriority) == 0 {
		return candidates[0], true
	}
	for _, preferred := range decision.AttachmentPriority {
		needle := "." + strings.TrimPrefix(strings.ToLower(preferred), ".")
		for _, attachment := range candidates {
			if strings.HasSuffix(strings.ToLower(attachment.FileName), needle) {
				return attachment, true
			}
		}
	}
	return candidates[0], true
}

func releaseHasText(release domain.NormalizedRelease) bool {
	return strings.TrimSpace(release.TextHTML) != "" || strings.TrimSpace(release.TextPlain) != ""
}

func CanMaterialize(release domain.NormalizedRelease, decision domain.TrackDecision) bool {
	switch SelectContent(release, decision).Kind {
	case "body":
		return releaseHasText(release) || decision.ContentStrategy == domain.ContentStrategyTextPlusAttachment && len(release.Attachments) > 0
	case "attachment":
		file, ok := SelectAttachment(release, decision)
		return ok && (file.LocalPath != "" || file.DownloadURL != "")
	default:
		return false
	}
}

func fallback(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}

func ruleID(sourceID string, idx int, rule config.RuleConfig) string {
	key := strings.ReplaceAll(strings.TrimSpace(rule.TrackKey), " ", "-")
	if key == "" {
		key = "default"
	}
	matchType := strings.TrimSpace(rule.MatchType)
	if matchType == "" {
		matchType = "selectors"
	}
	if rule.Override {
		return fmt.Sprintf("%s/override/%s", sourceID, rule.ReleaseID)
	}
	return fmt.Sprintf("%s/rule/%s/%s/%d", sourceID, key, matchType, idx)
}

func SelectContent(release domain.NormalizedRelease, decision domain.TrackDecision) domain.ContentReference {
	if decision.SelectedContent != nil {
		return *decision.SelectedContent
	}
	switch decision.ContentStrategy {
	case domain.ContentStrategyTextPost, domain.ContentStrategyTextPlusAttachment:
		return domain.ContentReference{Kind: "body"}
	case domain.ContentStrategyAttachmentOnly, domain.ContentStrategyAttachmentPreferred:
		if attachment, ok := SelectAttachment(release, decision); ok {
			if decision.ContentStrategy == domain.ContentStrategyAttachmentPreferred && attachment.LocalPath == "" && attachment.DownloadURL == "" {
				return domain.ContentReference{Kind: "body"}
			}
			return attachmentReference(release, attachment)
		}
		if decision.ContentStrategy == domain.ContentStrategyAttachmentPreferred {
			return domain.ContentReference{Kind: "body"}
		}
	}
	return domain.ContentReference{Kind: "none"}
}

func attachmentReference(release domain.NormalizedRelease, attachment domain.Attachment) domain.ContentReference {
	for i, file := range release.Attachments {
		if file == attachment {
			return domain.ContentReference{Kind: "attachment", FileName: file.FileName, SHA256: file.SHA256, AttachmentIndex: i}
		}
	}
	return domain.ContentReference{Kind: "none"}
}

func Hold(source string, explained ExplainedDecision, reasons []string) ExplainedDecision {
	decision := reviewDecision(source)
	decision.RuleID = explained.Decision.RuleID
	decision.Matched = true
	decision.SelectedContent = &domain.ContentReference{Kind: "none"}
	explained.Decision = decision
	explained.Explanation.HeldReasons = reasons
	return explained
}

func decisionConflicts(attempts []domain.RuleAttempt) []domain.DecisionConflict {
	var labels, titles []domain.RuleAttempt
	for _, attempt := range attempts {
		if !attempt.Matched {
			continue
		}
		label, title := false, false
		for _, selector := range attempt.Selectors {
			label = label || selector.Kind == "collection" || selector.Kind == "tag"
			title = title || selector.Kind == "title_regex" || selector.Kind == "attachment_filename_regex"
		}
		if label {
			labels = append(labels, attempt)
		}
		if title {
			titles = append(titles, attempt)
		}
	}
	var conflicts []domain.DecisionConflict
	for _, label := range labels {
		for _, title := range titles {
			if label.Series != title.Series {
				conflicts = append(conflicts, domain.DecisionConflict{LabelSeries: label.Series, TitleSeries: title.Series, LabelInput: label.Input, TitleInput: title.Input})
			}
		}
	}
	return conflicts
}
