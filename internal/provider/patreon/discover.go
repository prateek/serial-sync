package patreon

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

const defaultDiscoverySampleLimit = 5

func (c *Client) DiscoverSources(ctx context.Context, auth config.AuthProfile, existingSources []config.SourceConfig, sampleLimit int) (provider.DiscoverResult, error) {
	result := provider.DiscoverResult{
		Provider:  c.Name(),
		AuthState: domain.AuthStateReauthRequired,
	}
	if normalizeAuthMode(auth.Mode) == "fixture" {
		return result, fmt.Errorf("Patreon source discovery requires a live username_password auth profile")
	}
	if normalizeAuthMode(auth.Mode) != "username_password" {
		return result, fmt.Errorf("Patreon source discovery requires username_password mode for auth profile %q", auth.ID)
	}
	if sampleLimit <= 0 {
		sampleLimit = defaultDiscoverySampleLimit
	}
	session, user, authState, err := c.ensureDiscoverySession(ctx, auth)
	result.AuthState = authState
	if err != nil {
		return result, err
	}
	existingByHandle := buildExistingSourceIndex(existingSources)
	membershipKinds := membershipKindsByCampaign(user)
	suggestions := make([]provider.SourceSuggestion, 0, len(user.Included))
	for _, item := range user.Included {
		if item.Type != "campaign" {
			continue
		}
		sourceURL, handle := campaignPostsURL(item.Attributes.Vanity, item.Attributes.URLForCurrentUser, item.Attributes.URL)
		if sourceURL == "" {
			continue
		}
		sourceID := slugifyPatreonIdentifier(firstNonEmpty(handle, item.Attributes.Name, item.ID))
		if sourceID == "" {
			sourceID = "patreon-source"
		}
		suggestion := provider.SourceSuggestion{
			Source: config.SourceConfig{
				ID:          sourceID,
				Provider:    "patreon",
				URL:         sourceURL,
				AuthProfile: auth.ID,
				Enabled:     true,
			},
			CreatorName:    firstNonEmpty(item.Attributes.Name, humanizePatreonIdentifier(sourceID)),
			CreatorHandle:  handle,
			MembershipKind: membershipKinds[item.ID],
		}
		if existing, ok := matchExistingSource(existingByHandle, sourceURL, handle); ok {
			suggestion.AlreadyConfigured = true
			suggestion.ExistingSourceID = existing.ID
		}
		sessionWithCampaign := *session
		sessionWithCampaign.campaign = campaignInfo{ID: item.ID, Name: suggestion.CreatorName}
		sampledDocs, sampleErr := c.sampleDiscoveryDocuments(ctx, &sessionWithCampaign, suggestion.Source, sampleLimit)
		if sampleErr != nil {
			return result, sampleErr
		}
		suggestion.SampleTitles = sampleTitles(sampledDocs, sampleLimit)
		suggestion.SampleTags = sampleValues(sampledDocs, sampleLimit, func(doc provider.ReleaseDocument) []string { return doc.Normalized.Tags })
		suggestion.SampleCollections = sampleValues(sampledDocs, sampleLimit, func(doc provider.ReleaseDocument) []string { return doc.Normalized.Collections })
		suggestion.SuggestedRules = suggestRulesForSource(suggestion.Source.ID, sampledDocs)
		suggestions = append(suggestions, suggestion)
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		return strings.ToLower(suggestions[i].CreatorName) < strings.ToLower(suggestions[j].CreatorName)
	})
	result.Suggestions = suggestions
	result.AuthState = domain.AuthStateAuthenticated
	return result, nil
}

func (c *Client) ensureDiscoverySession(ctx context.Context, auth config.AuthProfile) (*liveSession, *currentUserEnvelope, domain.AuthState, error) {
	if auth.SessionPath == "" {
		return nil, nil, domain.AuthStateReauthRequired, fmt.Errorf("auth profile %q must define session_path", auth.ID)
	}
	dummySource := config.SourceConfig{
		ID:          "patreon-discovery",
		Provider:    "patreon",
		URL:         c.apiBaseURL,
		AuthProfile: auth.ID,
		Enabled:     true,
	}
	bundle, err := loadSessionBundle(auth.SessionPath)
	if err == nil {
		client, clientErr := httpClientFromSession()
		if clientErr != nil {
			return nil, nil, domain.AuthStateReauthRequired, clientErr
		}
		user, authState, userErr := c.fetchCurrentUser(ctx, client, dummySource, bundle)
		if userErr == nil {
			return &liveSession{
				bundle:        *bundle,
				client:        client,
				currentUserID: user.Data.ID,
			}, user, authState, nil
		}
		if authState == domain.AuthStateChallengeNeeded {
			return nil, nil, authState, userErr
		}
	}
	if c.bootstrap == nil {
		return nil, nil, domain.AuthStateReauthRequired, fmt.Errorf("no Patreon bootstrapper configured")
	}
	authState, bootErr := c.bootstrap(ctx, auth, dummySource, sessionProfileDir(auth.SessionPath))
	if bootErr != nil {
		return nil, nil, authState, bootErr
	}
	bundle, err = loadSessionBundle(auth.SessionPath)
	if err != nil {
		return nil, nil, domain.AuthStateReauthRequired, fmt.Errorf("load Patreon session after bootstrap: %w", err)
	}
	client, err := httpClientFromSession()
	if err != nil {
		return nil, nil, domain.AuthStateReauthRequired, err
	}
	user, authState, err := c.fetchCurrentUser(ctx, client, dummySource, bundle)
	if err != nil {
		return nil, nil, authState, err
	}
	return &liveSession{
		bundle:        *bundle,
		client:        client,
		currentUserID: user.Data.ID,
	}, user, domain.AuthStateAuthenticated, nil
}

func (c *Client) sampleDiscoveryDocuments(ctx context.Context, session *liveSession, source config.SourceConfig, sampleLimit int) ([]provider.ReleaseDocument, error) {
	postIDs, authState, err := c.listPostIDsWithLimit(ctx, session, source, nil, sampleLimit)
	if err != nil {
		return nil, fmt.Errorf("discover Patreon posts for %q (%s): %w", source.ID, authState, err)
	}
	docs := make([]provider.ReleaseDocument, 0, len(postIDs))
	for _, postID := range postIDs {
		raw, authState, err := c.fetchPostDetail(ctx, session, source, postID)
		if err != nil {
			return nil, fmt.Errorf("fetch Patreon post %s for %q (%s): %w", postID, source.ID, authState, err)
		}
		norm, err := parsePost(raw, "")
		if err != nil {
			return nil, fmt.Errorf("parse Patreon post %s for %q: %w", postID, source.ID, err)
		}
		norm.SourceType = string(sourceKindCreatorFeed)
		docs = append(docs, provider.ReleaseDocument{
			Normalized: norm,
			RawJSON:    append([]byte(nil), raw...),
		})
	}
	provider.SortReleaseDocuments(docs)
	return docs, nil
}

func buildExistingSourceIndex(existingSources []config.SourceConfig) map[string]config.SourceConfig {
	index := map[string]config.SourceConfig{}
	for _, source := range existingSources {
		if strings.TrimSpace(source.Provider) != "patreon" {
			continue
		}
		if detectSourceKind(source.URL) != sourceKindCreatorFeed {
			continue
		}
		handle, err := sourceHandle(source.URL)
		if err != nil {
			continue
		}
		key := normalizeHandleToken(handle)
		if key == "" {
			continue
		}
		index[key] = source
	}
	return index
}

func membershipKindsByCampaign(user *currentUserEnvelope) map[string]string {
	kinds := map[string]string{}
	if user == nil {
		return kinds
	}
	for _, item := range user.Included {
		if item.Type != "member" {
			continue
		}
		campaignID := strings.TrimSpace(item.Relationships.Campaign.Data.ID)
		if campaignID == "" {
			continue
		}
		switch {
		case item.Attributes.IsFreeTrial:
			kinds[campaignID] = "trial"
		case item.Attributes.IsFreeMember:
			kinds[campaignID] = "free"
		default:
			kinds[campaignID] = "paid"
		}
	}
	return kinds
}

func matchExistingSource(index map[string]config.SourceConfig, sourceURL, handle string) (config.SourceConfig, bool) {
	if handle != "" {
		if source, ok := index[normalizeHandleToken(handle)]; ok {
			return source, true
		}
	}
	if handle, err := sourceHandle(sourceURL); err == nil {
		if source, ok := index[normalizeHandleToken(handle)]; ok {
			return source, true
		}
	}
	return config.SourceConfig{}, false
}

func campaignPostsURL(vanity, currentUserURL, rawURL string) (string, string) {
	handle := normalizeHandleToken(firstNonEmpty(vanity, handleFromURL(currentUserURL), handleFromURL(rawURL)))
	if handle == "" {
		return "", ""
	}
	return "https://www.patreon.com/c/" + handle + "/posts", handle
}

func handleFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
	if len(parts) == 0 {
		return ""
	}
	if parts[0] == "c" || parts[0] == "cw" {
		if len(parts) >= 2 {
			return parts[1]
		}
		return ""
	}
	return parts[len(parts)-1]
}

func sampleTitles(docs []provider.ReleaseDocument, limit int) []string {
	out := make([]string, 0, min(len(docs), limit))
	for _, doc := range docs {
		title := strings.TrimSpace(doc.Normalized.Title)
		if title == "" {
			continue
		}
		out = append(out, title)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func sampleValues(docs []provider.ReleaseDocument, limit int, selector func(provider.ReleaseDocument) []string) []string {
	counts := map[string]int{}
	for _, doc := range docs {
		for _, value := range selector(doc) {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			counts[value]++
		}
	}
	return rankedKeys(counts, limit)
}

func suggestRulesForSource(sourceID string, docs []provider.ReleaseDocument) []config.RuleConfig {
	if len(docs) == 0 {
		return []config.RuleConfig{
			defaultFallbackRule(sourceID, domain.ContentStrategyManual, nil, nil),
		}
	}
	attachmentGlob, attachmentPriority := suggestedAttachmentPreferences(docs)
	defaultStrategy := suggestedContentStrategy(docs)
	rules := make([]config.RuleConfig, 0, 4)
	priority := 10
	for _, tag := range rankedKeys(valueCounts(docs, func(doc provider.ReleaseDocument) []string { return doc.Normalized.Tags }), 3) {
		tagDocs := filterDocuments(docs, func(doc provider.ReleaseDocument) bool {
			for _, value := range doc.Normalized.Tags {
				if strings.EqualFold(strings.TrimSpace(value), tag) {
					return true
				}
			}
			return false
		})
		if len(tagDocs) < 2 {
			continue
		}
		strategy := suggestedContentStrategy(tagDocs)
		rules = append(rules, config.RuleConfig{
			Source:             sourceID,
			Priority:           priority,
			MatchType:          "tag",
			MatchValue:         tag,
			TrackKey:           slugifyPatreonIdentifier(tag),
			TrackName:          humanizePatreonIdentifier(tag),
			ReleaseRole:        string(domain.ReleaseRoleChapter),
			ContentStrategy:    string(strategy),
			AttachmentGlob:     attachmentGlob,
			AttachmentPriority: attachmentPriority,
		})
		priority += 10
	}
	for _, collection := range rankedKeys(valueCounts(docs, func(doc provider.ReleaseDocument) []string { return doc.Normalized.Collections }), 3) {
		collectionDocs := filterDocuments(docs, func(doc provider.ReleaseDocument) bool {
			for _, value := range doc.Normalized.Collections {
				if strings.EqualFold(strings.TrimSpace(value), collection) {
					return true
				}
			}
			return false
		})
		if len(collectionDocs) < 2 {
			continue
		}
		rules = append(rules, config.RuleConfig{
			Source:             sourceID,
			Priority:           priority,
			MatchType:          "collection",
			MatchValue:         collection,
			TrackKey:           slugifyPatreonIdentifier(collection),
			TrackName:          humanizePatreonIdentifier(collection),
			ReleaseRole:        string(domain.ReleaseRoleChapter),
			ContentStrategy:    string(suggestedContentStrategy(collectionDocs)),
			AttachmentGlob:     attachmentGlob,
			AttachmentPriority: attachmentPriority,
		})
		priority += 10
	}
	if len(rules) == 0 {
		if prefix, regex := commonTitlePrefixRule(docs); prefix != "" && regex != "" {
			rules = append(rules, config.RuleConfig{
				Source:             sourceID,
				Priority:           priority,
				MatchType:          "title_regex",
				MatchValue:         regex,
				TrackKey:           slugifyPatreonIdentifier(prefix),
				TrackName:          prefix,
				ReleaseRole:        string(domain.ReleaseRoleChapter),
				ContentStrategy:    string(defaultStrategy),
				AttachmentGlob:     attachmentGlob,
				AttachmentPriority: attachmentPriority,
			})
			priority += 10
		}
	}
	if len(rules) == 0 {
		rules = append(rules, config.RuleConfig{
			Source:             sourceID,
			Priority:           10,
			MatchType:          "fallback",
			TrackKey:           "main-series",
			TrackName:          "Main Series",
			ReleaseRole:        string(domain.ReleaseRoleChapter),
			ContentStrategy:    string(defaultStrategy),
			AttachmentGlob:     attachmentGlob,
			AttachmentPriority: attachmentPriority,
		})
		return rules
	}
	rules = append(rules, defaultFallbackRule(sourceID, domain.ContentStrategyManual, nil, nil))
	return rules
}

func defaultFallbackRule(sourceID string, strategy domain.ContentStrategy, attachmentGlob, attachmentPriority []string) config.RuleConfig {
	return config.RuleConfig{
		Source:             sourceID,
		Priority:           1000,
		MatchType:          "fallback",
		TrackKey:           "unmatched-review",
		TrackName:          "Unmatched Review",
		ReleaseRole:        string(domain.ReleaseRoleUnknown),
		ContentStrategy:    string(strategy),
		AttachmentGlob:     attachmentGlob,
		AttachmentPriority: attachmentPriority,
	}
}

func suggestedContentStrategy(docs []provider.ReleaseDocument) domain.ContentStrategy {
	textCount := 0
	attachmentCount := 0
	for _, doc := range docs {
		if strings.TrimSpace(doc.Normalized.TextHTML) != "" || strings.TrimSpace(doc.Normalized.TextPlain) != "" {
			textCount++
		}
		if len(doc.Normalized.Attachments) > 0 {
			attachmentCount++
		}
	}
	switch {
	case attachmentCount == 0 && textCount > 0:
		return domain.ContentStrategyTextPost
	case attachmentCount > 0:
		return domain.ContentStrategyAttachmentPreferred
	default:
		return domain.ContentStrategyManual
	}
}

func suggestedAttachmentPreferences(docs []provider.ReleaseDocument) ([]string, []string) {
	counts := map[string]int{}
	for _, doc := range docs {
		for _, attachment := range doc.Normalized.Attachments {
			ext := strings.ToLower(strings.TrimPrefix(filepathExt(attachment.FileName), "."))
			if ext == "" {
				continue
			}
			counts[ext]++
		}
	}
	order := rankedKeys(counts, 3)
	if len(order) == 0 {
		return nil, nil
	}
	globs := make([]string, 0, len(order))
	for _, ext := range order {
		globs = append(globs, "*."+ext)
	}
	return globs, order
}

func commonTitlePrefixRule(docs []provider.ReleaseDocument) (string, string) {
	counts := map[string]int{}
	for _, doc := range docs {
		title := strings.TrimSpace(doc.Normalized.Title)
		prefix := titlePrefix(title)
		if len(prefix) < 4 {
			continue
		}
		counts[prefix]++
	}
	best := rankedKeys(counts, 1)
	if len(best) == 0 || counts[best[0]] < 2 {
		return "", ""
	}
	quoted := regexp.QuoteMeta(best[0])
	return best[0], "^" + quoted + `(?:\s*[-:|]\s*)`
}

func titlePrefix(title string) string {
	title = strings.TrimSpace(title)
	for _, separator := range []string{" - ", ": ", " | "} {
		if head, _, ok := strings.Cut(title, separator); ok {
			return strings.TrimSpace(head)
		}
	}
	return ""
}

func valueCounts(docs []provider.ReleaseDocument, selector func(provider.ReleaseDocument) []string) map[string]int {
	counts := map[string]int{}
	for _, doc := range docs {
		for _, value := range selector(doc) {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			counts[value]++
		}
	}
	return counts
}

func rankedKeys(counts map[string]int, limit int) []string {
	type item struct {
		Value string
		Count int
	}
	items := make([]item, 0, len(counts))
	for value, count := range counts {
		items = append(items, item{Value: value, Count: count})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return strings.ToLower(items[i].Value) < strings.ToLower(items[j].Value)
		}
		return items[i].Count > items[j].Count
	})
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	out := make([]string, 0, limit)
	for _, item := range items[:limit] {
		out = append(out, item.Value)
	}
	return out
}

func filterDocuments(docs []provider.ReleaseDocument, keep func(provider.ReleaseDocument) bool) []provider.ReleaseDocument {
	filtered := make([]provider.ReleaseDocument, 0, len(docs))
	for _, doc := range docs {
		if keep(doc) {
			filtered = append(filtered, doc)
		}
	}
	return filtered
}

func filepathExt(name string) string {
	lastDot := strings.LastIndex(strings.TrimSpace(name), ".")
	if lastDot < 0 {
		return ""
	}
	return name[lastDot:]
}

func humanizePatreonIdentifier(input string) string {
	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	for idx, part := range parts {
		if part == "" {
			continue
		}
		parts[idx] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	return strings.Join(parts, " ")
}

func slugifyPatreonIdentifier(input string) string {
	input = strings.TrimSpace(strings.ToLower(input))
	var builder strings.Builder
	lastDash := false
	for _, r := range input {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			builder.WriteRune(r)
			lastDash = false
		case !lastDash:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
