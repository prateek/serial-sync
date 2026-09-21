package discovery

import (
	"sort"
	"strings"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

type LabelDrift struct {
	Series   string   `json:"series"`
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"configured_name"`
	Observed []string `json:"observed_names,omitempty"`
	Detail   string   `json:"detail"`
}

type LabelReport struct {
	Source            string                  `json:"source"`
	CapturedPosts     int                     `json:"captured_posts"`
	RecentPosts       int                     `json:"recent_posts"`
	MissingIdentities int                     `json:"posts_without_identity_enrichment"`
	Labels            []domain.LabelReference `json:"labels"`
	PossibleDrift     []LabelDrift            `json:"possible_drift,omitempty"`
}

func Labels(source string, releases []domain.NormalizedRelease, rules []config.RuleConfig, decisions map[string]classify.ExplainedDecision, history []domain.LabelReference) LabelReport {
	report := LabelReport{Source: source, CapturedPosts: len(releases)}
	observed := map[string]domain.LabelReference{}
	merge := func(label domain.LabelReference) {
		old := observed[label.Key()]
		if old.ID == "" {
			old = label
			old.Names = nil
		}
		for _, name := range label.Names {
			if !contains(old.Names, name) {
				old.Names = append(old.Names, name)
			}
		}
		sort.Strings(old.Names)
		observed[label.Key()] = old
	}
	for _, release := range releases {
		if release.Enrichment == nil {
			report.MissingIdentities++
			continue
		}
		for _, label := range release.Enrichment.Collections {
			merge(label)
		}
	}
	for _, label := range history {
		if _, ok := observed[label.Key()]; ok {
			merge(label)
		}
	}
	for _, label := range observed {
		report.Labels = append(report.Labels, label)
	}
	sort.Slice(report.Labels, func(i, j int) bool { return report.Labels[i].Key() < report.Labels[j].Key() })
	recent := append([]domain.NormalizedRelease(nil), releases...)
	sort.SliceStable(recent, func(i, j int) bool {
		if recent[i].PublishedAt.Equal(recent[j].PublishedAt) {
			return recent[i].ProviderReleaseID < recent[j].ProviderReleaseID
		}
		return recent[i].PublishedAt.After(recent[j].PublishedAt)
	})
	if len(recent) > 20 {
		recent = recent[:20]
	}
	report.RecentPosts = len(recent)
	seen := map[string]bool{}
	for _, rule := range rules {
		collections := rule.Collections
		if rule.MatchType == "collection" {
			collections = append(append([]config.CollectionSelector(nil), collections...), config.CollectionSelector{Name: rule.MatchValue})
		}
		for _, selector := range collections {
			if selector.Name == "" {
				continue
			}
			drift := LabelDrift{Series: rule.TrackKey, ID: selector.ID, Name: selector.Name}
			present, titleMatch := false, false
			for _, release := range recent {
				for _, name := range release.Collections {
					present = present || strings.EqualFold(name, selector.Name)
				}
				if release.Enrichment != nil && selector.ID != "" {
					for _, label := range release.Enrichment.Collections {
						if label.ID == selector.ID {
							for _, name := range label.Names {
								if !strings.EqualFold(name, selector.Name) && !contains(drift.Observed, name) {
									drift.Observed = append(drift.Observed, name)
								}
							}
						}
					}
				}
				explained := decisions[release.ProviderReleaseID]
				if explained.Decision.SeriesID == rule.TrackKey {
					for _, attempt := range explained.Explanation.Attempts {
						if attempt.Selected {
							for _, match := range attempt.Selectors {
								titleMatch = titleMatch || match.Kind == "title_regex"
							}
						}
					}
				}
			}
			if selector.ID != "" && len(drift.Observed) > 0 {
				drift.Detail = "pinned ID observed under another name"
			} else if selector.ID == "" && !present && titleMatch {
				drift.Detail = "name absent from recent captures while the series still matches titles"
			}
			key := drift.Series + "/" + drift.ID + "/" + drift.Name
			if drift.Detail != "" && !seen[key] {
				sort.Strings(drift.Observed)
				report.PossibleDrift = append(report.PossibleDrift, drift)
				seen[key] = true
			}
		}
	}
	return report
}
