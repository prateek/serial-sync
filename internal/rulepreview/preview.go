package rulepreview

import (
	"sort"
	"strings"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func Build(sourceID string, releases []domain.NormalizedRelease, rules []config.RuleConfig, includePosts bool) provider.DiscoveryPreview {
	preview := provider.DiscoveryPreview{
		SampledPosts: len(releases),
	}
	if includePosts {
		preview.Posts = make([]provider.DiscoveryPreviewPost, 0, len(releases))
	}
	groupIndex := map[string]int{}
	for _, release := range releases {
		explained := classify.Explain(sourceID, release, rules)
		materializable := classify.CanMaterialize(release, explained.Decision)
		matchType := "unmatched"
		matchValue := ""
		if explained.Rule != nil {
			matchType = explained.Rule.MatchType
			matchValue = explained.Rule.MatchValue
		}
		if explained.Rule == nil || matchType == "fallback" {
			preview.FallbackPosts++
		}
		if materializable {
			preview.Materializable++
		}
		if includePosts {
			attachments := make([]string, 0, len(release.Attachments))
			for _, attachment := range release.Attachments {
				attachments = append(attachments, attachment.FileName)
			}
			preview.Posts = append(preview.Posts, provider.DiscoveryPreviewPost{
				ProviderReleaseID: release.ProviderReleaseID,
				Title:             release.Title,
				PublishedAt:       release.PublishedAt,
				Tags:              append([]string(nil), release.Tags...),
				Collections:       append([]string(nil), release.Collections...),
				Attachments:       attachments,
				TrackKey:          explained.Decision.TrackKey,
				TrackName:         explained.Decision.TrackName,
				MatchType:         matchType,
				MatchValue:        matchValue,
				ContentStrategy:   explained.Decision.ContentStrategy,
				Materializable:    materializable,
			})
		}
		groupKey := strings.Join([]string{
			explained.Decision.TrackKey,
			explained.Decision.TrackName,
			matchType,
			matchValue,
			string(explained.Decision.ContentStrategy),
		}, "\x00")
		groupPos, ok := groupIndex[groupKey]
		if !ok {
			groupPos = len(preview.Groups)
			groupIndex[groupKey] = groupPos
			preview.Groups = append(preview.Groups, provider.DiscoveryPreviewGroup{
				TrackKey:        explained.Decision.TrackKey,
				TrackName:       explained.Decision.TrackName,
				MatchType:       matchType,
				MatchValue:      matchValue,
				ContentStrategy: explained.Decision.ContentStrategy,
			})
		}
		group := &preview.Groups[groupPos]
		group.Total++
		if materializable {
			group.Materializable++
		}
		if title := strings.TrimSpace(release.Title); title != "" && len(group.SampleTitles) < 5 {
			group.SampleTitles = append(group.SampleTitles, title)
		}
	}
	sort.SliceStable(preview.Groups, func(i, j int) bool {
		if preview.Groups[i].Total == preview.Groups[j].Total {
			return strings.ToLower(preview.Groups[i].TrackKey) < strings.ToLower(preview.Groups[j].TrackKey)
		}
		return preview.Groups[i].Total > preview.Groups[j].Total
	})
	return preview
}
