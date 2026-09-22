package patreon

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func (c *Client) DiscoverSources(ctx context.Context, auth config.AuthProfile, existingSources []config.SourceConfig, options provider.DiscoverOptions) (provider.DiscoverResult, error) {
	result := provider.DiscoverResult{
		Provider:  c.Name(),
		AuthState: domain.AuthStateReauthRequired,
	}
	startedAt := time.Now()
	if normalizeAuthMode(auth.Mode) == "fixture" {
		return result, fmt.Errorf("Patreon source discovery requires a live username_password auth profile")
	}
	if normalizeAuthMode(auth.Mode) != "username_password" {
		return result, fmt.Errorf("Patreon source discovery requires username_password mode for auth profile %q", auth.ID)
	}
	_, user, authState, err := c.ensureDiscoverySession(ctx, auth, false)
	result.AuthState = authState
	if err != nil {
		return result, err
	}
	provider.ReportProgress(ctx, provider.ProgressEvent{
		Level:      "info",
		Component:  "discover",
		Message:    "Patreon discovery session ready",
		EntityKind: "auth_profile",
		EntityID:   auth.ID,
		Payload: map[string]any{
			"auth_profile_id": auth.ID,
			"auth_state":      authState,
			"duration_ms":     elapsedMillis(startedAt),
		},
	})
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
		if !matchesMembershipFilter(suggestion.MembershipKind, options.MembershipFilter) {
			continue
		}
		if existing, ok := matchExistingSource(existingByHandle, sourceURL, handle); ok {
			suggestion.AlreadyConfigured = true
			suggestion.ExistingSourceID = existing.ID
		}
		if !matchesCreatorFilters(suggestion, options.CreatorFilters) {
			continue
		}
		provider.ReportProgress(ctx, provider.ProgressEvent{
			Level:      "info",
			Component:  "discover",
			Message:    "discovered Patreon creator",
			EntityKind: "source",
			EntityID:   suggestion.Source.ID,
			Payload: map[string]any{
				"source_id":          suggestion.Source.ID,
				"creator_name":       suggestion.CreatorName,
				"creator_handle":     suggestion.CreatorHandle,
				"membership_kind":    suggestion.MembershipKind,
				"already_configured": suggestion.AlreadyConfigured,
			},
		})
		suggestions = append(suggestions, suggestion)
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		return strings.ToLower(suggestions[i].CreatorName) < strings.ToLower(suggestions[j].CreatorName)
	})
	result.Suggestions = suggestions
	result.AuthState = domain.AuthStateAuthenticated
	provider.ReportProgress(ctx, provider.ProgressEvent{
		Level:      "info",
		Component:  "discover",
		Message:    "Patreon discovery complete",
		EntityKind: "auth_profile",
		EntityID:   auth.ID,
		Payload: map[string]any{
			"auth_profile_id": auth.ID,
			"suggestions":     len(suggestions),
			"duration_ms":     elapsedMillis(startedAt),
		},
	})
	return result, nil
}

func matchesMembershipFilter(kind, filter string) bool {
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case "", "all":
		return true
	default:
		return strings.EqualFold(firstNonEmpty(kind, "unknown"), filter)
	}
}

func matchesCreatorFilters(suggestion provider.SourceSuggestion, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	candidates := []string{
		strings.ToLower(strings.TrimSpace(suggestion.Source.ID)),
		strings.ToLower(strings.TrimSpace(suggestion.CreatorHandle)),
		strings.ToLower(strings.TrimSpace(suggestion.CreatorName)),
	}
	for _, raw := range filters {
		filter := strings.ToLower(strings.TrimSpace(raw))
		if filter == "" {
			continue
		}
		for _, candidate := range candidates {
			if candidate == "" {
				continue
			}
			if candidate == filter || strings.Contains(candidate, filter) {
				return true
			}
		}
	}
	return false
}

func (c *Client) ensureDiscoverySession(ctx context.Context, auth config.AuthProfile, allowBootstrap bool) (*liveSession, *currentUserEnvelope, domain.AuthState, error) {
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
	profile, err := c.profileSessions(auth)
	if err == nil {
		session := &liveSession{
			sourceID: dummySource.ID,
			bundle:   profile.bundle,
			client:   profile.client,
			budget:   profile.budget,
		}
		user, authState, userErr := c.fetchCurrentUser(ctx, session, dummySource.URL)
		if userErr == nil {
			session.currentUserID = user.Data.ID
			return session, user, authState, nil
		}
		if authState == domain.AuthStateChallengeNeeded || authState == domain.AuthStateAuthenticated {
			return nil, nil, authState, userErr
		}
	}
	if !allowBootstrap {
		return nil, nil, domain.AuthStateReauthRequired, fmt.Errorf("saved Patreon session is unavailable or expired; run setup auth --auth-profile %s", auth.ID)
	}
	if c.bootstrap == nil {
		return nil, nil, domain.AuthStateReauthRequired, fmt.Errorf("no Patreon bootstrapper configured")
	}
	authState, bootErr := c.bootstrap(ctx, auth, dummySource, sessionProfileDir(auth.SessionPath))
	if bootErr != nil {
		return nil, nil, authState, bootErr
	}
	delete(c.profiles, auth.ID)
	profile, err = c.profileSessions(auth)
	if err != nil {
		return nil, nil, domain.AuthStateReauthRequired, fmt.Errorf("load Patreon session after bootstrap: %w", err)
	}
	session := &liveSession{
		sourceID: dummySource.ID,
		bundle:   profile.bundle,
		client:   profile.client,
		budget:   profile.budget,
	}
	user, authState, err := c.fetchCurrentUser(ctx, session, dummySource.URL)
	if err != nil {
		return nil, nil, authState, err
	}
	session.currentUserID = user.Data.ID
	return session, user, domain.AuthStateAuthenticated, nil
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
