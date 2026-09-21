package discovery

import (
	"fmt"
	"regexp"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

// Suggestions returns an uninstalled draft. Anonymous and external candidates
// require a human decision before a useful publishing selector can be proposed.
func Suggestions(candidates []domain.DiscoveryCandidate, releases map[string][]domain.NormalizedRelease) string {
	var fragments []string
	for _, candidate := range candidates {
		if candidate.Status == "resolved" {
			continue
		}
		title := strings.TrimPrefix(candidate.CorrelationKey, "family:")
		selection := config.Selection{}
		external := false
		for _, evidence := range candidate.Evidence {
			if evidence.Kind == "unknown-collection" {
				selection.Collections = append(selection.Collections, config.CollectionSelector{Name: evidence.Value})
			}
			external = external || evidence.Kind == "content-external"
		}
		if len(selection.Collections) > 0 {
			title = selection.Collections[0].Name
		}
		if len(selection.Collections) == 0 && strings.HasPrefix(candidate.CorrelationKey, "family:") {
			selection.TitlePatterns = []string{"(?i)^" + regexp.QuoteMeta(title) + `\b`}
		}
		if !selection.Present() || external {
			fragments = append(fragments, fmt.Sprintf("# %s: inspect releases %s; no publishing selector proposed.\n", candidate.ID, strings.Join(candidate.MemberReleaseIDs, ", ")))
			continue
		}
		strategy := "text_post"
		minimum := 1500
		for _, release := range releases[candidate.Source] {
			if contains(candidate.MemberReleaseIDs, release.ProviderReleaseID) {
				features := Extract(release)
				if features.BookFile && features.BodyChars < 1500 {
					strategy = "attachment_only"
					minimum = 0
				}
			}
		}
		if len(selection.TitlePatterns) > 0 {
			titleEvidence := false
			for _, release := range releases[candidate.Source] {
				if contains(candidate.MemberReleaseIDs, release.ProviderReleaseID) && Extract(release).SequenceOrigin == "title" {
					titleEvidence = true
					break
				}
			}
			if !titleEvidence {
				selection.AttachmentPatterns = selection.TitlePatterns
				selection.TitlePatterns = nil
			}
		}
		input := map[string]any{"content_strategy": strategy, "min_body_chars": minimum}
		if len(selection.Collections) > 0 {
			input["collections"] = selection.Collections
		} else if len(selection.AttachmentPatterns) > 0 {
			input["attachment_patterns"] = selection.AttachmentPatterns
		} else {
			input["title_patterns"] = selection.TitlePatterns
		}
		data, err := toml.Marshal(map[string]any{"series": []any{map[string]any{"id": candidate.ID, "title": title, "source": candidate.Source, "inputs": []any{input}}}})
		if err == nil {
			fragments = append(fragments, "# Review candidate "+candidate.ID+" before installing this draft.\n"+string(data))
		}
	}
	return strings.Join(fragments, "\n")
}
