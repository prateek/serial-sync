package classify

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

type ExplainedDecision struct {
	Decision domain.TrackDecision
	Rule     *config.RuleConfig
}

func Decide(sourceID string, release domain.NormalizedRelease, rules []config.RuleConfig) domain.TrackDecision {
	return Explain(sourceID, release, rules).Decision
}

func Explain(sourceID string, release domain.NormalizedRelease, rules []config.RuleConfig) ExplainedDecision {
	sorted := append([]config.RuleConfig(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})
	for idx, rule := range sorted {
		if matches(rule, release) {
			copied := rule
			return ExplainedDecision{
				Decision: domain.TrackDecision{
					TrackKey:           rule.TrackKey,
					TrackName:          fallback(rule.TrackName, rule.TrackKey),
					RuleID:             ruleID(sourceID, idx, rule),
					ReleaseRole:        domain.ReleaseRole(rule.ReleaseRole),
					ContentStrategy:    domain.ContentStrategy(rule.ContentStrategy),
					AttachmentGlob:     append([]string(nil), rule.AttachmentGlob...),
					AttachmentPriority: append([]string(nil), rule.AttachmentPriority...),
					AnthologyMode:      rule.AnthologyMode,
					Matched:            true,
				},
				Rule: &copied,
			}
		}
	}
	return ExplainedDecision{
		Decision: domain.TrackDecision{
			TrackKey:        "unmatched",
			TrackName:       "Unmatched",
			RuleID:          "fallback/" + sourceID + "/unmatched",
			ReleaseRole:     domain.ReleaseRoleUnknown,
			ContentStrategy: domain.ContentStrategyManual,
			Matched:         false,
		},
	}
}

func matches(rule config.RuleConfig, release domain.NormalizedRelease) bool {
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
		re, err := regexp.Compile(rule.MatchValue)
		if err != nil {
			return false
		}
		return re.MatchString(release.Title)
	case "attachment_filename_regex":
		re, err := regexp.Compile(rule.MatchValue)
		if err != nil {
			return false
		}
		for _, attachment := range release.Attachments {
			if re.MatchString(attachment.FileName) {
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
	switch decision.ContentStrategy {
	case domain.ContentStrategyTextPost:
		return releaseHasText(release)
	case domain.ContentStrategyAttachmentPreferred:
		if _, ok := SelectAttachment(release, decision); ok {
			return true
		}
		return releaseHasText(release)
	case domain.ContentStrategyAttachmentOnly:
		_, ok := SelectAttachment(release, decision)
		return ok
	case domain.ContentStrategyTextPlusAttachment:
		return releaseHasText(release) || len(release.Attachments) > 0
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
		matchType = "unknown"
	}
	return fmt.Sprintf("%s/rule/%s/%s/%d", sourceID, key, matchType, idx)
}
