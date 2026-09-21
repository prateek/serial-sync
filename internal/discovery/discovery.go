package discovery

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/sequence"
)

type group struct {
	candidate domain.DiscoveryCandidate
	features  []Features
	family    string
	labels    map[string]bool
}

// Detect replays the current capture in publication order. Previous candidates supply
// stable operator decisions and observation times, never future detection evidence.
func Detect(source string, releases []domain.NormalizedRelease, cfg *config.Config, previous []domain.DiscoveryCandidate, now time.Time) []domain.DiscoveryCandidate {
	fresh := map[string]bool{}
	for _, release := range releases {
		fresh[release.ProviderReleaseID] = true
	}
	return Observe(source, releases, fresh, cfg, previous, now)
}

func Replay(source string, releases []domain.NormalizedRelease, cfg *config.Config, previous []domain.DiscoveryCandidate, now time.Time) []domain.DiscoveryCandidate {
	return Observe(source, releases, nil, cfg, previous, now)
}

// Observe distinguishes fetched versions from older catalog captures. Only a fresh
// observation may supersede evidence whose newer version was never saved.
func Observe(source string, releases []domain.NormalizedRelease, fresh map[string]bool, cfg *config.Config, previous []domain.DiscoveryCandidate, now time.Time) []domain.DiscoveryCandidate {
	return Analyze(source, releases, fresh, cfg, previous, now).Candidates
}

type Analysis struct {
	Decisions  map[string]classify.ExplainedDecision
	Candidates []domain.DiscoveryCandidate
}

func Analyze(source string, releases []domain.NormalizedRelease, fresh map[string]bool, cfg *config.Config, previous []domain.DiscoveryCandidate, now time.Time) Analysis {
	analysis := Analysis{Decisions: map[string]classify.ExplainedDecision{}}

	byID := map[string]domain.NormalizedRelease{}
	for _, release := range releases {
		previous, exists := byID[release.ProviderReleaseID]
		if !exists || release.EditedAt.After(previous.EditedAt) {
			byID[release.ProviderReleaseID] = release
		}
	}
	ordered := make([]domain.NormalizedRelease, 0, len(byID))
	for _, release := range byID {
		ordered = append(ordered, release)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.PublishedAt.Equal(b.PublishedAt) {
			return a.ProviderReleaseID < b.ProviderReleaseID
		}
		return a.PublishedAt.Before(b.PublishedAt)
	})
	rules := cfg.RulesForSource(source)
	references := map[string]bool{}
	for _, rule := range rules {
		for _, name := range rule.Collections {
			if name.ID != "" {
				references["collection-id:"+name.ID] = true
			} else {
				references["collection:"+labelKey(name.Name)] = true
			}
		}
		for _, name := range rule.Tags {
			references["tag:"+labelKey(name)] = true
		}
		if rule.MatchType == "collection" || rule.MatchType == "tag" {
			references[rule.MatchType+":"+labelKey(rule.MatchValue)] = true
		}
	}
	ignored := map[string]bool{}
	if src, ok := cfg.SourceByID(source); ok {
		for _, label := range src.IgnoreLabels {
			ignored[labelKey(label)] = true
			ignored[strings.ToLower(label)] = true
		}
	}
	var groups []*group
	observed := map[string]string{}
	lastChapter := map[string]int{}
	seenHashes := map[string]string{}
	for _, release := range ordered {
		feature := Extract(release)
		captured, _ := json.Marshal(feature)
		observed[release.ProviderReleaseID] = digest(captured)
		feature.Collections = withoutIgnored(feature.Collections, ignored)
		feature.Tags = withoutIgnored(feature.Tags, ignored)
		explained := classify.Explain(source, release, rules)
		evidence := []domain.CandidateEvidence{}
		labels := map[string]bool{}
		unknownCollection := false
		knownNames := map[string]bool{}
		for _, ref := range feature.CollectionReferences {
			if ignored["id:"+ref.ID] {
				for _, name := range ref.Names {
					knownNames[labelKey(name)] = true
				}
				continue
			}
			labels["collection-id:"+ref.Key()] = true
			if references["collection-id:"+ref.ID] {
				for _, name := range ref.Names {
					knownNames[labelKey(name)] = true
				}
			} else if len(ref.Names) == 0 {
				unknownCollection = true
				evidence = append(evidence, domain.CandidateEvidence{Kind: "unknown-collection-id", Value: ref.ID})
			}
		}
		for _, name := range feature.Collections {
			key := labelKey(name)
			if ignored[key] {
				continue
			}
			labels["collection:"+key] = true
			if !references["collection:"+key] && !knownNames[key] {
				unknownCollection = true
				evidence = append(evidence, domain.CandidateEvidence{Kind: "unknown-collection", Value: name})
			}
		}
		for _, name := range feature.Tags {
			key := labelKey(name)
			if !ignored[key] {
				labels["tag:"+key] = true
			}
		}
		decision := sequence.ApplyDetected(cfg, source, release.ProviderReleaseID, explained.Decision, feature.Sequence)
		mappedFamily := decision.Matched && (familyExplained(feature.Family, decision) || bookExplained(cfg, source, release.ProviderReleaseID, decision))
		stream := decision.SeriesID + "/" + feature.Family + "/" + decision.Sequence.BookID
		chapter := decision.Sequence.Chapter
		reset := chapter > 0 && lastChapter[stream] > chapter
		if chapter > 0 {
			lastChapter[stream] = chapter
		}
		if strings.HasPrefix(decision.Sequence.BookID, "book:") {
			evidence = append(evidence, domain.CandidateEvidence{Kind: "unresolved-book", Value: decision.Sequence.BookID})
		}

		explained.Decision = sequence.Apply(cfg, source, release, explained.Decision)
		if explained.Rule != nil && explained.Rule.HoldCandidates && !explained.Rule.Override {
			var reasons []string
			if feature.First {
				reasons = append(reasons, "first-chapter")
			}
			if reset {
				reasons = append(reasons, "numbering-reset")
			}
			if unknownCollection {
				reasons = append(reasons, "unknown-collection")
			}
			if len(reasons) > 0 {
				explained = classify.Hold(source, explained, reasons)
				evidence = append(evidence, domain.CandidateEvidence{Kind: "held-review", Value: strings.Join(reasons, ", ")})
			}
		}
		analysis.Decisions[release.ProviderReleaseID] = explained
		for _, conflict := range explained.Explanation.Conflicts {
			evidence = append(evidence, domain.CandidateEvidence{Kind: "label-title-conflict", Value: conflict.LabelSeries + " / " + conflict.TitleSeries})
		}
		confirmed := explained.Rule != nil && explained.Rule.Override
		if feature.First && !mappedFamily && !confirmed {
			evidence = append(evidence, domain.CandidateEvidence{Kind: "first-chapter", Value: feature.Sequence.MatchedText})
		}
		if reset && !confirmed {
			evidence = append(evidence, domain.CandidateEvidence{Kind: "numbering-reset", Value: feature.Sequence.MatchedText})
		}
		if feature.Family != "" && feature.Sequence.HasChapter() && !mappedFamily && !confirmed {
			evidence = append(evidence, domain.CandidateEvidence{Kind: "new-title-family", Value: feature.Family})
		}
		if !confirmed && decision.ContentStrategy == domain.ContentStrategyManual && (feature.BodyChars >= 1500 || feature.BookFile) {
			evidence = append(evidence, domain.CandidateEvidence{Kind: "substantial-review"})
		}
		if feature.Teaser && decision.ContentStrategy == domain.ContentStrategyManual {
			evidence = append(evidence, domain.CandidateEvidence{Kind: "teaser"})
		}
		if feature.External && feature.Sequence.HasChapter() {
			evidence = append(evidence, domain.CandidateEvidence{Kind: "content-external"})
		}
		if len(feature.Attachments) > 1 {
			evidence = append(evidence, domain.CandidateEvidence{Kind: "multiple-files", Value: strings.Join(feature.Attachments, ", ")})
		}
		if earlier, ok := seenHashes[feature.ContentHash]; ok && earlier != feature.ReleaseID {
			evidence = append(evidence, domain.CandidateEvidence{Kind: "repost", Value: earlier})
		}
		seenHashes[feature.ContentHash] = feature.ReleaseID
		if confirmed {
			continue
		}
		candidateGroup := findGroup(groups, feature, labels)
		if len(evidence) == 0 && candidateGroup == nil {
			continue
		}
		if candidateGroup == nil {
			kind := "series"
			if unknownCollection && mappedFamily {
				kind = "book"
			}
			key := "release:" + feature.ReleaseID
			if feature.Family != "" {
				key = "family:" + feature.Family
			}
			candidateGroup = &group{family: feature.Family, labels: map[string]bool{}, candidate: domain.DiscoveryCandidate{ID: "candidate_" + digest([]byte(source + "\x00" + feature.ReleaseID))[:20], Source: source, Kind: kind, CorrelationKey: key, ExtractorVersion: ExtractorVersion, Status: "possible", FirstObserved: now, LastEvidenceChange: now}}
			groups = append(groups, candidateGroup)
		}
		if candidateGroup.family == "" && feature.Family != "" {
			candidateGroup.family = feature.Family
			candidateGroup.candidate.CorrelationKey = "family:" + feature.Family
		}
		candidateGroup.features = append(candidateGroup.features, feature)
		candidateGroup.candidate.MemberReleaseIDs = append(candidateGroup.candidate.MemberReleaseIDs, feature.ReleaseID)
		for label := range labels {
			candidateGroup.labels[label] = true
		}
		candidateGroup.candidate.Evidence = append(candidateGroup.candidate.Evidence, evidence...)
	}
	var detected []domain.DiscoveryCandidate
	for _, group := range groups {
		candidate := group.candidate
		candidate.Evidence = uniqueEvidence(candidate.Evidence)
		candidate.MemberFingerprints = map[string]domain.CandidateMemberFingerprint{}
		for _, feature := range group.features {
			data, _ := json.Marshal(feature)
			candidate.MemberFingerprints[feature.ReleaseID] = domain.CandidateMemberFingerprint{Capture: observed[feature.ReleaseID], Evidence: digest(data)}
		}
		candidate.EvidenceFingerprint = candidateFingerprint(candidate)
		if len(candidate.MemberReleaseIDs) > 1 {
			candidate.Status = "needs_decision"
		}
		detected = append(detected, candidate)
	}
	analysis.Candidates = reconcile(detected, previous, observed, fresh)
	return analysis
}

func familyExplained(family string, decision domain.TrackDecision) bool {
	if family == "" {
		return false
	}
	for _, name := range []string{decision.TrackKey, decision.TrackName} {
		key := labelKey(name)
		if key != "" && (key == family || strings.Contains(key, " "+family) || strings.Contains(family, key+" ")) {
			return true
		}
	}
	return false
}

func findGroup(groups []*group, feature Features, labels map[string]bool) *group {
	for _, group := range groups {
		if feature.Family != "" && feature.Family == group.family {
			return group
		}
	}
	var compatible []*group
	for _, group := range groups {
		if feature.Family != "" && group.family != "" {
			continue
		}
		linked := false
		for label := range labels {
			if group.labels[label] {
				linked = true
				break
			}
		}
		if !linked && feature.Family == "" && group.family == "" && len(labels) == 0 && len(group.labels) == 0 && len(group.features) > 0 {
			last := group.features[len(group.features)-1].Sequence.Chapter
			linked = last > 0 && feature.Sequence.Chapter == last+1
		}
		if linked {
			compatible = append(compatible, group)
		}
	}
	if len(compatible) == 1 {
		return compatible[0]
	}
	return nil
}

func uniqueEvidence(evidence []domain.CandidateEvidence) []domain.CandidateEvidence {
	sort.Slice(evidence, func(i, j int) bool {
		a, b := evidence[i], evidence[j]
		if a.Kind == b.Kind {
			return a.Value < b.Value
		}
		return a.Kind < b.Kind
	})
	result := evidence[:0]
	for _, item := range evidence {
		if len(result) == 0 || result[len(result)-1] != item {
			result = append(result, item)
		}
	}
	return result
}

func reconcile(detected, previous []domain.DiscoveryCandidate, observed map[string]string, fresh map[string]bool) []domain.DiscoveryCandidate {
	assigned := map[int]bool{}
	used := map[string]bool{}
	reserved := map[string]bool{}
	for _, old := range previous {
		reserved[old.ID] = true
	}
	for _, old := range previous {
		best, score := -1, 0
		for index, candidate := range detected {
			if assigned[index] || old.Source != candidate.Source {
				continue
			}
			overlap := 0
			for i, id := range old.MemberReleaseIDs {
				if contains(candidate.MemberReleaseIDs, id) {
					overlap += 2
					if i == 0 {
						overlap++
					}
				}
			}
			if overlap > score {
				best, score = index, overlap
			}
		}
		if best < 0 {
			continue
		}
		assigned[best] = true
		used[old.ID] = true
		candidate := &detected[best]
		for _, id := range old.MemberReleaseIDs {
			if observed[id] != "" && (fresh[id] || observed[id] == old.MemberFingerprints[id].Capture) {
				continue
			}
			if !contains(candidate.MemberReleaseIDs, id) {
				candidate.MemberReleaseIDs = append(candidate.MemberReleaseIDs, id)
			}
			candidate.MemberFingerprints[id] = old.MemberFingerprints[id]
			candidate.UncapturedMembers = append(candidate.UncapturedMembers, id)
		}
		if len(candidate.UncapturedMembers) > 0 {
			candidate.Evidence = uniqueEvidence(append(candidate.Evidence, old.Evidence...))
			candidate.EvidenceFingerprint = candidateFingerprint(*candidate)
		}
		candidate.ID = old.ID
		candidate.FirstObserved = old.FirstObserved
		candidate.LastReportedFingerprint = old.LastReportedFingerprint
		candidate.DismissalReason = old.DismissalReason
		candidate.DismissedFingerprint = old.DismissedFingerprint
		if candidate.EvidenceFingerprint == old.EvidenceFingerprint {
			candidate.LastEvidenceChange = old.LastEvidenceChange
		}
		if candidate.DismissedFingerprint == candidate.EvidenceFingerprint {
			candidate.Status = "resolved"
		}
		candidate.Changed = candidate.Status != "resolved" && (candidate.EvidenceFingerprint != old.LastReportedFingerprint || old.Status == "resolved")
		for _, id := range old.MemberReleaseIDs {
			if !contains(candidate.MemberReleaseIDs, id) {
				candidate.ResolvedMembers = append(candidate.ResolvedMembers, id)
			}
		}
	}
	for index := range detected {
		if assigned[index] {
			continue
		}
		candidate := &detected[index]
		for suffix := 0; reserved[candidate.ID]; suffix++ {
			candidate.ID = "candidate_" + digest([]byte(fmt.Sprintf("%s/%s/%s/%d", candidate.Source, candidate.CorrelationKey, candidate.MemberReleaseIDs[0], suffix)))[:20]
		}
		reserved[candidate.ID] = true
		candidate.Changed = true
	}
	for _, old := range previous {
		if used[old.ID] {
			continue
		}
		old.Changed = false
		old.ResolvedMembers = nil
		old.UncapturedMembers = nil
		for _, id := range old.MemberReleaseIDs {
			if observed[id] != "" && (fresh[id] || observed[id] == old.MemberFingerprints[id].Capture) {
				old.ResolvedMembers = append(old.ResolvedMembers, id)
			} else {
				old.UncapturedMembers = append(old.UncapturedMembers, id)
			}
		}
		if len(old.UncapturedMembers) == 0 {
			old.Status = "resolved"
		}
		detected = append(detected, old)
	}
	sort.Slice(detected, func(i, j int) bool {
		a, b := detected[i], detected[j]
		if a.FirstObserved.Equal(b.FirstObserved) {
			return a.ID < b.ID
		}
		return a.FirstObserved.Before(b.FirstObserved)
	})
	return detected
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func withoutIgnored(labels []string, ignored map[string]bool) []string {
	var result []string
	for _, label := range labels {
		if !ignored[labelKey(label)] {
			result = append(result, label)
		}
	}
	return result
}

func candidateFingerprint(candidate domain.DiscoveryCandidate) string {
	members := map[string]string{}
	for id, fingerprint := range candidate.MemberFingerprints {
		members[id] = fingerprint.Evidence
	}
	data, _ := json.Marshal(struct {
		Members  map[string]string
		Evidence []domain.CandidateEvidence
	}{members, candidate.Evidence})
	return digest(data)
}

func bookExplained(cfg *config.Config, source, releaseID string, decision domain.TrackDecision) bool {
	for _, series := range cfg.Series {
		if series.ID != decision.SeriesID {
			continue
		}
		bookID := decision.BookID
		for _, override := range series.SequenceOverrides {
			if override.Source == source && override.ReleaseID == releaseID && override.BookID != "" {
				bookID = override.BookID
			}
		}
		if bookID == "" {
			return false
		}
		for _, book := range series.Books {
			if book.ID == bookID {
				return true
			}
		}
	}
	return false
}
