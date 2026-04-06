package patreon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/pquerna/otp/totp"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/runtime/display"
)

var campaignIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`"campaign"\s*:\s*\{\s*"data"\s*:\s*\{\s*"id"\s*:\s*"([0-9]+)"`),
	regexp.MustCompile(`"campaignId"\s*:\s*"?(?P<id>[0-9]+)"?`),
	regexp.MustCompile(`/api/campaigns/([0-9]+)`),
}

var emailInputSelectors = []string{
	`input[type="email"]`,
	`input[name="email"]`,
	`input[name="identifier"]`,
	`input[autocomplete="email"]`,
}

var passwordInputSelectors = []string{
	`input[type="password"]`,
	`input[name="password"]`,
	`input[autocomplete="current-password"]`,
}

var submitButtonSelectors = []string{
	`button[type="submit"]`,
	`input[type="submit"]`,
	`button[data-tag*="login"]`,
	`button[data-tag*="continue"]`,
	`button[data-testid*="login"]`,
}

var totpInputSelectors = []string{
	`input[autocomplete="one-time-code"]`,
	`input[inputmode="numeric"]`,
	`input[name*="otp"]`,
	`input[name*="code"]`,
	`input[id*="otp"]`,
	`input[id*="code"]`,
}

var collectionPostLinkPattern = regexp.MustCompile(`/posts/[^"'?#>]*-([0-9]+)`)

type sessionBundle struct {
	Provider  string          `json:"provider"`
	SavedAt   time.Time       `json:"saved_at"`
	UserAgent string          `json:"user_agent"`
	Cookies   []sessionCookie `json:"cookies"`
}

type sessionCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"http_only"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"same_site,omitempty"`
}

type currentUserEnvelope struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
	Included []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Name              string `json:"name"`
			URL               string `json:"url"`
			URLForCurrentUser string `json:"url_for_current_user"`
			Vanity            string `json:"vanity"`
			IsFreeMember      bool   `json:"is_free_member"`
			IsFreeTrial       bool   `json:"is_free_trial"`
		} `json:"attributes"`
		Relationships struct {
			Campaign struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"campaign"`
		} `json:"relationships"`
	} `json:"included"`
}

type postsIndexEnvelope struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

type campaignInfo struct {
	ID   string
	Name string
}

type liveSession struct {
	bundle        sessionBundle
	client        *http.Client
	currentUserID string
	campaign      campaignInfo
}

type pageSnapshot struct {
	URL   string
	Title string
	Text  string
}

const (
	liveSyncCursorVersion    = 1
	liveSyncCursorLookback   = 25
	liveSyncCursorRecentKeep = 64
	liveFetchWorkerLimitCold = 1
	liveFetchWorkerLimitWarm = 2
	liveFetchProgressEvery   = 25
	patreonRetryAttempts     = 4
	patreonRetryBaseDelay    = 500 * time.Millisecond
	patreonRetryMaxDelay     = 4 * time.Second
)

type liveSyncCursor struct {
	Version          int      `json:"version"`
	Lookback         int      `json:"lookback"`
	RecentReleaseIDs []string `json:"recent_release_ids"`
}

func reportPatreonProgress(ctx context.Context, level, message, entityKind, entityID string, payload any) {
	provider.ReportProgress(ctx, provider.ProgressEvent{
		Level:      level,
		Component:  "provider",
		Message:    message,
		EntityKind: entityKind,
		EntityID:   entityID,
		Payload:    payload,
	})
}

func reportSourceProgress(ctx context.Context, sourceID, level, message string, payload any) {
	reportPatreonProgress(ctx, level, message, "source", sourceID, payload)
}

func reportRequestProgress(ctx context.Context, requestURL, level, message string, payload any) {
	reportPatreonProgress(ctx, level, message, "request", requestURL, payload)
}

func elapsedMillis(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

func (c *Client) listLiveReleases(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, storedSource *domain.Source) (provider.ListResult, error) {
	startedAt := time.Now()
	reportSourceProgress(ctx, source.ID, "info", "starting Patreon live release listing", map[string]any{
		"source_id":      source.ID,
		"source_kind":    string(detectSourceKind(source.URL)),
		"has_sync_cursor": storedSource != nil && strings.TrimSpace(storedSource.SyncCursor) != "",
	})
	session, authState, err := c.ensureLiveSession(ctx, auth, source)
	if err != nil {
		return provider.ListResult{AuthState: authState}, err
	}
	cursor := parseLiveSyncCursor("")
	if storedSource != nil {
		cursor = parseLiveSyncCursor(storedSource.SyncCursor)
	}
	var postIDs []string
	switch detectSourceKind(source.URL) {
	case sourceKindCollection:
		postIDs, authState, err = c.listCollectionPostIDs(ctx, session, source, cursor)
	default:
		postIDs, authState, err = c.listPostIDs(ctx, session, source, cursor)
	}
	if err != nil {
		return provider.ListResult{AuthState: authState}, err
	}
	docs, authState, err := c.fetchPostDocuments(ctx, session, source, postIDs, liveFetchWorkerLimitForStoredSource(storedSource))
	if err != nil {
		return provider.ListResult{AuthState: authState}, err
	}
	reportSourceProgress(ctx, source.ID, "info", "Patreon live release listing complete", map[string]any{
		"source_id":         source.ID,
		"documents":         len(docs),
		"duration_ms":       elapsedMillis(startedAt),
		"sync_cursor_kept":  min(len(docs), liveSyncCursorRecentKeep),
		"auth_state":        domain.AuthStateAuthenticated,
		"source_kind":       string(detectSourceKind(source.URL)),
	})
	return provider.ListResult{
		Documents:  docs,
		AuthState:  domain.AuthStateAuthenticated,
		SyncCursor: buildLiveSyncCursor(docs),
	}, nil
}

func (c *Client) ensureLiveSession(ctx context.Context, auth config.AuthProfile, source config.SourceConfig) (*liveSession, domain.AuthState, error) {
	resolveStartedAt := time.Now()
	session, authState, err := c.resolveLiveSession(ctx, auth, source)
	if err == nil {
		reportSourceProgress(ctx, source.ID, "info", "resolved Patreon session", map[string]any{
			"source_id":   source.ID,
			"auth_state":  authState,
			"duration_ms": elapsedMillis(resolveStartedAt),
			"reused":      true,
		})
		return session, authState, nil
	}
	if authState == domain.AuthStateChallengeNeeded || authState == domain.AuthStateAuthenticated {
		return nil, authState, err
	}
	if normalizeAuthMode(auth.Mode) != "username_password" {
		return nil, authState, err
	}
	if auth.SessionPath == "" {
		return nil, domain.AuthStateReauthRequired, fmt.Errorf("auth profile %q must set session_path for live Patreon sources", auth.ID)
	}
	if c.bootstrap == nil {
		return nil, domain.AuthStateReauthRequired, errors.New("no Patreon bootstrapper configured")
	}
	reportSourceProgress(ctx, source.ID, "warn", "Patreon session bootstrap required", map[string]any{
		"source_id":   source.ID,
		"auth_state":  authState,
		"duration_ms": elapsedMillis(resolveStartedAt),
		"error":       err.Error(),
	})
	bootstrapStartedAt := time.Now()
	authState, bootErr := c.bootstrap(ctx, auth, source, sessionProfileDir(auth.SessionPath))
	if bootErr != nil {
		return nil, authState, bootErr
	}
	reportSourceProgress(ctx, source.ID, "info", "bootstrapped Patreon session", map[string]any{
		"source_id":   source.ID,
		"auth_state":  authState,
		"duration_ms": elapsedMillis(bootstrapStartedAt),
	})
	session, authState, err = c.resolveLiveSession(ctx, auth, source)
	if err != nil {
		return nil, authState, err
	}
	reportSourceProgress(ctx, source.ID, "info", "resolved Patreon session", map[string]any{
		"source_id":   source.ID,
		"auth_state":  authState,
		"duration_ms": elapsedMillis(resolveStartedAt),
		"reused":      false,
	})
	return session, authState, nil
}

func (c *Client) resolveLiveSession(ctx context.Context, auth config.AuthProfile, source config.SourceConfig) (*liveSession, domain.AuthState, error) {
	bundle, err := loadSessionBundle(auth.SessionPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, domain.AuthStateReauthRequired, fmt.Errorf("no Patreon session found at %s", auth.SessionPath)
		}
		return nil, domain.AuthStateReauthRequired, fmt.Errorf("load Patreon session: %w", err)
	}
	client, err := httpClientFromSession()
	if err != nil {
		return nil, domain.AuthStateReauthRequired, err
	}
	user, authState, err := c.fetchCurrentUser(ctx, client, source, bundle)
	if err != nil {
		return nil, authState, err
	}
	session := &liveSession{
		bundle:        *bundle,
		client:        client,
		currentUserID: user.Data.ID,
	}
	if detectSourceKind(source.URL) != sourceKindCollection {
		campaign, authState, err := c.resolveCampaign(ctx, client, source, bundle, user)
		if err != nil {
			return nil, authState, err
		}
		session.campaign = campaign
	}
	return session, domain.AuthStateAuthenticated, nil
}

func (c *Client) fetchCurrentUser(ctx context.Context, client *http.Client, source config.SourceConfig, bundle *sessionBundle) (*currentUserEnvelope, domain.AuthState, error) {
	requestURL := c.currentUserAPIURL()
	body, authState, err := c.get(ctx, client, requestURL, source.URL, bundle, "application/json")
	if err != nil {
		return nil, authState, err
	}
	var payload currentUserEnvelope
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, domain.AuthStateReauthRequired, fmt.Errorf("decode Patreon current user payload: %w", err)
	}
	if payload.Data.ID == "" {
		return nil, domain.AuthStateExpired, errors.New("Patreon session did not include a current user id")
	}
	return &payload, domain.AuthStateAuthenticated, nil
}

func (c *Client) resolveCampaign(ctx context.Context, client *http.Client, source config.SourceConfig, bundle *sessionBundle, user *currentUserEnvelope) (campaignInfo, domain.AuthState, error) {
	handle, err := sourceHandle(source.URL)
	if err != nil {
		return campaignInfo{}, domain.AuthStateReauthRequired, err
	}
	for _, item := range user.Included {
		if item.Type != "campaign" {
			continue
		}
		if campaignMatchesHandle(item.Attributes.Vanity, item.Attributes.URL, handle) {
			return campaignInfo{ID: item.ID, Name: item.Attributes.Name}, domain.AuthStateAuthenticated, nil
		}
	}
	body, authState, err := c.get(ctx, client, source.URL, source.URL, bundle, "text/html")
	if err != nil {
		return campaignInfo{}, authState, err
	}
	for _, pattern := range campaignIDPatterns {
		matches := pattern.FindStringSubmatch(string(body))
		if len(matches) >= 2 {
			return campaignInfo{ID: matches[len(matches)-1], Name: handle}, domain.AuthStateAuthenticated, nil
		}
	}
	return campaignInfo{}, domain.AuthStateReauthRequired, fmt.Errorf("could not resolve Patreon campaign id for source %q", source.ID)
}

func (c *Client) listPostIDs(ctx context.Context, session *liveSession, source config.SourceConfig, cursor *liveSyncCursor) ([]string, domain.AuthState, error) {
	return c.listPostIDsWithLimit(ctx, session, source, cursor, 0)
}

func (c *Client) listPostIDsWithLimit(ctx context.Context, session *liveSession, source config.SourceConfig, cursor *liveSyncCursor, limit int) ([]string, domain.AuthState, error) {
	startedAt := time.Now()
	nextURL := c.postsIndexAPIURL(session.campaign.ID, session.currentUserID)
	seen := map[string]struct{}{}
	ids := make([]string, 0, 32)
	known := map[string]struct{}{}
	lookback := 0
	pageCount := 0
	stopReason := "exhausted"
	if cursor != nil {
		lookback = cursor.Lookback
		for _, id := range cursor.RecentReleaseIDs {
			if strings.TrimSpace(id) != "" {
				known[id] = struct{}{}
			}
		}
	}
	for nextURL != "" {
		pageCount++
		body, authState, err := c.get(ctx, session.client, nextURL, source.URL, &session.bundle, "application/json")
		if err != nil {
			return nil, authState, err
		}
		var page postsIndexEnvelope
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, domain.AuthStateReauthRequired, fmt.Errorf("decode Patreon posts feed: %w", err)
		}
		for _, item := range page.Data {
			if item.ID == "" {
				continue
			}
			if _, ok := seen[item.ID]; ok {
				continue
			}
			seen[item.ID] = struct{}{}
			ids = append(ids, item.ID)
			if limit > 0 && len(ids) >= limit {
				stopReason = "limit_reached"
				reportSourceProgress(ctx, source.ID, "info", "Patreon feed page fetched", map[string]any{
					"source_id":      source.ID,
					"page":           pageCount,
					"discovered_ids": len(ids),
					"next_page":      strings.TrimSpace(page.Links.Next) != "",
					"duration_ms":    elapsedMillis(startedAt),
				})
				reportSourceProgress(ctx, source.ID, "info", "Patreon feed pagination complete", map[string]any{
					"source_id":      source.ID,
					"pages":          pageCount,
					"discovered_ids": len(ids),
					"stop_reason":    stopReason,
					"duration_ms":    elapsedMillis(startedAt),
				})
				return ids, domain.AuthStateAuthenticated, nil
			}
			if lookback > 0 && len(ids) >= lookback {
				if _, ok := known[item.ID]; ok {
					stopReason = "known_recent_boundary"
					reportSourceProgress(ctx, source.ID, "info", "Patreon feed page fetched", map[string]any{
						"source_id":      source.ID,
						"page":           pageCount,
						"discovered_ids": len(ids),
						"next_page":      strings.TrimSpace(page.Links.Next) != "",
						"duration_ms":    elapsedMillis(startedAt),
					})
					reportSourceProgress(ctx, source.ID, "info", "Patreon feed scan stopped at known recent post", map[string]any{
						"source_id":           source.ID,
						"provider_release_id": item.ID,
						"lookback":            lookback,
						"pages":               pageCount,
						"discovered_ids":      len(ids),
						"duration_ms":         elapsedMillis(startedAt),
					})
					reportSourceProgress(ctx, source.ID, "info", "Patreon feed pagination complete", map[string]any{
						"source_id":      source.ID,
						"pages":          pageCount,
						"discovered_ids": len(ids),
						"stop_reason":    stopReason,
						"duration_ms":    elapsedMillis(startedAt),
					})
					return ids, domain.AuthStateAuthenticated, nil
				}
			}
		}
		reportSourceProgress(ctx, source.ID, "info", "Patreon feed page fetched", map[string]any{
			"source_id":      source.ID,
			"page":           pageCount,
			"discovered_ids": len(ids),
			"next_page":      strings.TrimSpace(page.Links.Next) != "",
			"duration_ms":    elapsedMillis(startedAt),
		})
		nextURL = resolveRelativeURL(nextURL, page.Links.Next)
	}
	reportSourceProgress(ctx, source.ID, "info", "Patreon feed pagination complete", map[string]any{
		"source_id":      source.ID,
		"pages":          pageCount,
		"discovered_ids": len(ids),
		"stop_reason":    stopReason,
		"duration_ms":    elapsedMillis(startedAt),
	})
	return ids, domain.AuthStateAuthenticated, nil
}

func (c *Client) listCollectionPostIDs(ctx context.Context, session *liveSession, source config.SourceConfig, cursor *liveSyncCursor) ([]string, domain.AuthState, error) {
	startedAt := time.Now()
	body, authState, err := c.get(ctx, session.client, source.URL, source.URL, &session.bundle, "text/html")
	if err != nil {
		return nil, authState, err
	}
	ids := extractCollectionPostIDs(body)
	if len(ids) == 0 {
		return nil, domain.AuthStateReauthRequired, fmt.Errorf("could not discover Patreon collection posts for %q", source.ID)
	}
	trimmed := trimPostIDsWithCursor(ids, cursor)
	reportSourceProgress(ctx, source.ID, "info", "Patreon collection scan complete", map[string]any{
		"source_id":      source.ID,
		"discovered_ids": len(trimmed),
		"source_kind":    string(sourceKindCollection),
		"duration_ms":    elapsedMillis(startedAt),
	})
	return trimmed, domain.AuthStateAuthenticated, nil
}

func (c *Client) fetchPostDetail(ctx context.Context, session *liveSession, source config.SourceConfig, postID string) ([]byte, domain.AuthState, error) {
	requestURL := c.postDetailAPIURL(postID)
	return c.get(ctx, session.client, requestURL, source.URL, &session.bundle, "application/json")
}

type postDocumentResult struct {
	index     int
	document  provider.ReleaseDocument
	authState domain.AuthState
	err       error
}

func (c *Client) fetchPostDocuments(ctx context.Context, session *liveSession, source config.SourceConfig, postIDs []string, workerLimit int) ([]provider.ReleaseDocument, domain.AuthState, error) {
	if len(postIDs) == 0 {
		return nil, domain.AuthStateAuthenticated, nil
	}
	startedAt := time.Now()
	workerCount := min(len(postIDs), max(1, workerLimit))
	reportSourceProgress(ctx, source.ID, "info", "starting Patreon post detail fetch", map[string]any{
		"source_id":   source.ID,
		"total_posts": len(postIDs),
		"concurrency": workerCount,
	})
	jobs := make(chan int)
	results := make(chan postDocumentResult, len(postIDs))
	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				doc, authState, err := c.fetchPostDocument(ctx, session, source, postIDs[index])
				results <- postDocumentResult{
					index:     index,
					document:  doc,
					authState: authState,
					err:       err,
				}
			}
		}()
	}
	go func() {
		for index := range len(postIDs) {
			jobs <- index
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	docs := make([]provider.ReleaseDocument, len(postIDs))
	authState := domain.AuthStateAuthenticated
	var firstErr error
	completed := 0
	failed := 0
	lastProgress := time.Now()
	for result := range results {
		completed++
		if result.err != nil {
			failed++
			reportSourceProgress(ctx, source.ID, "error", "Patreon post detail fetch failed", map[string]any{
				"source_id":           source.ID,
				"provider_release_id": postIDs[result.index],
				"completed":           completed,
				"total_posts":         len(postIDs),
				"failed":              failed,
				"auth_state":          result.authState,
				"error":               result.err.Error(),
				"duration_ms":         elapsedMillis(startedAt),
			})
			if firstErr == nil {
				firstErr = result.err
				authState = result.authState
			}
			continue
		}
		docs[result.index] = result.document
		if completed == len(postIDs) || completed%liveFetchProgressEvery == 0 || time.Since(lastProgress) >= 10*time.Second {
			lastProgress = time.Now()
			reportSourceProgress(ctx, source.ID, "info", "Patreon post details progress", map[string]any{
				"source_id":   source.ID,
				"completed":   completed,
				"total_posts": len(postIDs),
				"failed":      failed,
				"duration_ms": elapsedMillis(startedAt),
			})
		}
	}
	if firstErr != nil {
		return nil, authState, firstErr
	}
	provider.SortReleaseDocuments(docs)
	reportSourceProgress(ctx, source.ID, "info", "Patreon post detail fetch complete", map[string]any{
		"source_id":   source.ID,
		"completed":   completed,
		"total_posts": len(postIDs),
		"failed":      failed,
		"duration_ms": elapsedMillis(startedAt),
	})
	return docs, domain.AuthStateAuthenticated, nil
}

func liveFetchWorkerLimitForStoredSource(storedSource *domain.Source) int {
	if storedSource != nil && strings.TrimSpace(storedSource.SyncCursor) != "" {
		return liveFetchWorkerLimitWarm
	}
	return liveFetchWorkerLimitCold
}

func (c *Client) fetchPostDocument(ctx context.Context, session *liveSession, source config.SourceConfig, postID string) (provider.ReleaseDocument, domain.AuthState, error) {
	raw, authState, err := c.fetchPostDetail(ctx, session, source, postID)
	if err != nil {
		return provider.ReleaseDocument{}, authState, err
	}
	norm, err := parsePost(raw, "")
	if err != nil {
		return provider.ReleaseDocument{}, domain.AuthStateReauthRequired, fmt.Errorf("parse Patreon post %s: %w", postID, err)
	}
	norm.SourceType = string(detectSourceKind(source.URL))
	return provider.ReleaseDocument{
		Normalized: norm,
		RawJSON:    append(json.RawMessage(nil), raw...),
	}, domain.AuthStateAuthenticated, nil
}

func (c *Client) prepareLiveRelease(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, doc provider.ReleaseDocument, decision domain.TrackDecision) (provider.ReleaseDocument, domain.AuthState, error) {
	switch decision.ContentStrategy {
	case domain.ContentStrategyAttachmentPreferred, domain.ContentStrategyAttachmentOnly:
	default:
		return doc, domain.AuthStateAuthenticated, nil
	}
	attachment, ok := classify.SelectAttachment(doc.Normalized, decision)
	if !ok {
		return doc, domain.AuthStateAuthenticated, nil
	}
	index := selectedAttachmentIndex(doc.Normalized.Attachments, attachment)
	if index < 0 {
		return doc, domain.AuthStateAuthenticated, nil
	}
	if doc.Normalized.Attachments[index].LocalPath != "" ||
		strings.TrimSpace(doc.Normalized.Attachments[index].DownloadURL) == "" ||
		strings.TrimSpace(doc.Normalized.Attachments[index].FileName) == "" {
		return doc, domain.AuthStateAuthenticated, nil
	}
	bundle, err := loadSessionBundle(auth.SessionPath)
	if err != nil {
		return doc, domain.AuthStateReauthRequired, fmt.Errorf("load Patreon session: %w", err)
	}
	client, err := httpClientFromSession()
	if err != nil {
		return doc, domain.AuthStateReauthRequired, err
	}
	session := &liveSession{
		bundle: *bundle,
		client: client,
	}
	authState, err := c.downloadLiveAttachment(ctx, session, auth, source, &doc.Normalized, index)
	if err != nil {
		return doc, authState, err
	}
	return doc, domain.AuthStateAuthenticated, nil
}

func (c *Client) downloadLiveAttachment(ctx context.Context, session *liveSession, auth config.AuthProfile, source config.SourceConfig, release *domain.NormalizedRelease, index int) (domain.AuthState, error) {
	if index < 0 || index >= len(release.Attachments) {
		return domain.AuthStateAuthenticated, nil
	}
	attachment := &release.Attachments[index]
	if attachment.LocalPath != "" || strings.TrimSpace(attachment.DownloadURL) == "" || strings.TrimSpace(attachment.FileName) == "" {
		return domain.AuthStateAuthenticated, nil
	}
	targetDir := filepath.Join(sessionCacheRoot(auth.SessionPath), "attachments", source.ID, release.ProviderReleaseID)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return domain.AuthStateReauthRequired, err
	}
	targetPath := filepath.Join(targetDir, sanitizeAttachmentFileName(attachment.FileName))
	if _, err := os.Stat(targetPath); err == nil {
		attachment.LocalPath = targetPath
		reportPatreonProgress(ctx, "info", "Patreon attachment cache hit", "release", release.ProviderReleaseID, map[string]any{
			"source_id":           source.ID,
			"provider_release_id": release.ProviderReleaseID,
			"file_name":           attachment.FileName,
			"cached_path":         targetPath,
		})
		return domain.AuthStateAuthenticated, nil
	}
	startedAt := time.Now()
	body, authState, err := c.get(ctx, session.client, attachment.DownloadURL, source.URL, &session.bundle, "*/*")
	if err != nil {
		return authState, fmt.Errorf("download Patreon attachment %q (%s): %s: %w", attachment.FileName, attachment.DownloadURL, authState, err)
	}
	if err := os.WriteFile(targetPath, body, 0o644); err != nil {
		return domain.AuthStateReauthRequired, err
	}
	attachment.LocalPath = targetPath
	reportPatreonProgress(ctx, "info", "downloaded Patreon attachment", "release", release.ProviderReleaseID, map[string]any{
		"source_id":           source.ID,
		"provider_release_id": release.ProviderReleaseID,
		"file_name":           attachment.FileName,
		"bytes":               len(body),
		"duration_ms":         elapsedMillis(startedAt),
	})
	return domain.AuthStateAuthenticated, nil
}

func (c *Client) get(ctx context.Context, client *http.Client, requestURL, referer string, bundle *sessionBundle, accept string) ([]byte, domain.AuthState, error) {
	for attempt := range patreonRetryAttempts {
		body, authState, retryDelay, err := c.getOnce(ctx, client, requestURL, referer, bundle, accept, attempt+1)
		if retryDelay < 0 {
			return body, authState, err
		}
		if attempt == patreonRetryAttempts-1 {
			return nil, authState, err
		}
		if waitErr := sleepWithContext(ctx, retryDelay); waitErr != nil {
			return nil, authState, waitErr
		}
	}
	return nil, domain.AuthStateAuthenticated, fmt.Errorf("Patreon request retries exhausted for %s", requestURL)
}

func (c *Client) getOnce(ctx context.Context, client *http.Client, requestURL, referer string, bundle *sessionBundle, accept string, attempt int) ([]byte, domain.AuthState, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, domain.AuthStateReauthRequired, -1, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", firstNonEmpty(bundleUserAgent(bundle), defaultUserAgent))
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if cookieHeader := cookieHeaderForURL(bundle, requestURL); cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, domain.AuthStateReauthRequired, -1, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, domain.AuthStateReauthRequired, -1, err
	}
	if authState, authErr := classifyHTTPAuthFailure(resp, body); authErr != nil {
		reportRequestProgress(ctx, requestURL, "error", "Patreon request failed authentication checks", map[string]any{
			"request_url": requestURL,
			"status":      resp.StatusCode,
			"auth_state":  authState,
			"attempt":     attempt,
			"error":       authErr.Error(),
		})
		return nil, authState, -1, authErr
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		delay := retryDelay(resp.Header.Get("Retry-After"), attempt)
		reportRequestProgress(ctx, requestURL, "warn", "Patreon rate limited request; backing off", map[string]any{
			"request_url": requestURL,
			"status":      resp.StatusCode,
			"attempt":     attempt,
			"delay_ms":    delay.Milliseconds(),
			"retry_after": strings.TrimSpace(resp.Header.Get("Retry-After")),
		})
		return nil, domain.AuthStateAuthenticated, delay, fmt.Errorf("Patreon rate limited request with status %d for %s", resp.StatusCode, requestURL)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		reportRequestProgress(ctx, requestURL, "error", "unexpected Patreon response status", map[string]any{
			"request_url": requestURL,
			"status":      resp.StatusCode,
			"attempt":     attempt,
		})
		return nil, domain.AuthStateReauthRequired, -1, fmt.Errorf("unexpected Patreon response status %d for %s", resp.StatusCode, requestURL)
	}
	return body, domain.AuthStateAuthenticated, -1, nil
}

func classifyHTTPAuthFailure(resp *http.Response, body []byte) (domain.AuthState, error) {
	bodyText := strings.ToLower(string(body))
	isHTML := responseLooksLikeHTML(resp, bodyText)
	if location := resp.Header.Get("Location"); location != "" && strings.Contains(strings.ToLower(location), "/login") {
		return domain.AuthStateExpired, fmt.Errorf("Patreon redirected to login")
	}
	if isHTML && (strings.Contains(bodyText, "just a moment") || strings.Contains(bodyText, "cf-chl") || strings.Contains(bodyText, "captcha")) {
		return domain.AuthStateChallengeNeeded, errors.New("Patreon presented a challenge page")
	}
	if isHTML && (strings.Contains(bodyText, "two-factor") || strings.Contains(bodyText, "two factor") || strings.Contains(bodyText, "one-time code") || strings.Contains(bodyText, "verify it")) {
		return domain.AuthStateChallengeNeeded, errors.New("Patreon requires an interactive verification step")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return domain.AuthStateExpired, fmt.Errorf("Patreon rejected the saved session with status %d", resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		return domain.AuthStateReauthRequired, fmt.Errorf("unexpected Patreon auth response status %d", resp.StatusCode)
	}
	return "", nil
}

func responseLooksLikeHTML(resp *http.Response, bodyText string) bool {
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") {
		return true
	}
	trimmed := strings.TrimSpace(bodyText)
	return strings.HasPrefix(trimmed, "<!doctype html") || strings.HasPrefix(trimmed, "<html")
}

func retryDelay(header string, attempt int) time.Duration {
	header = strings.TrimSpace(header)
	if header != "" {
		if seconds, err := strconv.Atoi(header); err == nil && seconds >= 0 {
			return clampRetryDelay(time.Duration(seconds) * time.Second)
		}
		if when, err := http.ParseTime(header); err == nil {
			return clampRetryDelay(time.Until(when))
		}
	}
	return clampRetryDelay(backoffDelay(attempt))
}

func clampRetryDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	if delay > patreonRetryMaxDelay {
		return patreonRetryMaxDelay
	}
	return delay
}

func backoffDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return patreonRetryBaseDelay
	}
	delay := patreonRetryBaseDelay
	for remaining := 1; remaining < attempt; remaining++ {
		delay *= 2
		if delay >= patreonRetryMaxDelay {
			return patreonRetryMaxDelay
		}
	}
	return delay
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func httpClientFromSession() (*http.Client, error) {
	return &http.Client{
		Timeout:       45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

func bundleUserAgent(bundle *sessionBundle) string {
	if bundle == nil {
		return ""
	}
	return bundle.UserAgent
}

func cookieHeaderForURL(bundle *sessionBundle, rawURL string) string {
	if bundle == nil || len(bundle.Cookies) == 0 {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	path := firstNonEmpty(u.EscapedPath(), "/")
	now := time.Now()
	parts := make([]string, 0, len(bundle.Cookies))
	for _, item := range bundle.Cookies {
		if item.Name == "" || item.Value == "" {
			continue
		}
		if item.Expires > 0 && now.After(time.Unix(int64(item.Expires), 0).UTC()) {
			continue
		}
		if item.Secure && u.Scheme != "https" {
			continue
		}
		if !cookieDomainMatches(host, item.Domain) {
			continue
		}
		if !cookiePathMatches(path, item.Path) {
			continue
		}
		parts = append(parts, item.Name+"="+item.Value)
	}
	return strings.Join(parts, "; ")
}

func cookieDomainMatches(host, domain string) bool {
	normalizedDomain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(domain), "."))
	if normalizedDomain == "" || host == "" {
		return false
	}
	return host == normalizedDomain || strings.HasSuffix(host, "."+normalizedDomain)
}

func cookiePathMatches(requestPath, cookiePath string) bool {
	normalizedCookiePath := firstNonEmpty(cookiePath, "/")
	normalizedRequestPath := firstNonEmpty(requestPath, "/")
	if normalizedCookiePath == "/" {
		return true
	}
	return strings.HasPrefix(normalizedRequestPath, normalizedCookiePath)
}

func loadSessionBundle(path string) (*sessionBundle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bundle sessionBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return nil, err
	}
	if bundle.Provider != "" && bundle.Provider != "patreon" {
		return nil, fmt.Errorf("unsupported session provider %q", bundle.Provider)
	}
	return &bundle, nil
}

func saveSessionBundle(path string, bundle sessionBundle) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

func bootstrapWithChromium(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, profileDir string) (domain.AuthState, error) {
	username := strings.TrimSpace(os.Getenv(auth.UsernameEnv))
	password := strings.TrimSpace(os.Getenv(auth.PasswordEnv))
	if username == "" {
		return domain.AuthStateReauthRequired, fmt.Errorf("auth profile %q expects username in %s", auth.ID, auth.UsernameEnv)
	}
	if password == "" {
		return domain.AuthStateReauthRequired, fmt.Errorf("auth profile %q expects password in %s", auth.ID, auth.PasswordEnv)
	}
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return domain.AuthStateReauthRequired, err
	}
	displaySession, err := display.Ensure(ctx)
	if err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("prepare headed browser environment: %w", err)
	}
	defer func() {
		_ = displaySession.Close()
	}()
	allocOptions := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocOptions = append(allocOptions,
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("headless", false),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1366, 768),
	)
	if chromePath := resolveChromiumBinary(); chromePath != "" {
		allocOptions = append(allocOptions, chromedp.ExecPath(chromePath))
	}
	if env := displaySession.ChromeEnv(); len(env) > 0 {
		allocOptions = append(allocOptions, chromedp.Env(env...))
	}
	if os.Geteuid() == 0 {
		allocOptions = append(allocOptions, chromedp.Flag("no-sandbox", true))
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, allocOptions...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()
	loginCtx, loginCancel := context.WithTimeout(browserCtx, 2*time.Minute)
	defer loginCancel()
	if err := chromedp.Run(loginCtx, network.Enable()); err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("start Chromium for Patreon auth: %w", err)
	}
	if err := chromedp.Run(loginCtx, chromedp.Navigate("https://www.patreon.com/login")); err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("open Patreon login page: %w", err)
	}
	emailSelector, err := waitForSelector(loginCtx, emailInputSelectors, 45*time.Second)
	if err != nil {
		return inferBrowserState(loginCtx, domain.AuthStateChallengeNeeded), fmt.Errorf("could not find Patreon email field: %w", err)
	}
	if err := chromedp.Run(loginCtx,
		chromedp.WaitVisible(emailSelector, chromedp.ByQuery),
		chromedp.Focus(emailSelector, chromedp.ByQuery),
		chromedp.SendKeys(emailSelector, username, chromedp.ByQuery),
	); err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("fill Patreon email field: %w", err)
	}
	passwordSelector, _ := waitForSelector(loginCtx, passwordInputSelectors, 2*time.Second)
	if passwordSelector == "" {
		submitSelector, submitErr := waitForSelector(loginCtx, submitButtonSelectors, 10*time.Second)
		if submitErr == nil && submitSelector != "" {
			_ = chromedp.Run(loginCtx, chromedp.Click(submitSelector, chromedp.ByQuery))
		}
		passwordSelector, err = waitForSelector(loginCtx, passwordInputSelectors, 20*time.Second)
		if err != nil {
			return inferBrowserState(loginCtx, domain.AuthStateChallengeNeeded), fmt.Errorf("could not find Patreon password field: %w", err)
		}
	}
	if err := chromedp.Run(loginCtx,
		chromedp.WaitVisible(passwordSelector, chromedp.ByQuery),
		chromedp.Focus(passwordSelector, chromedp.ByQuery),
		chromedp.SendKeys(passwordSelector, password, chromedp.ByQuery),
	); err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("fill Patreon password field: %w", err)
	}
	submitSelector, err := waitForSelector(loginCtx, submitButtonSelectors, 10*time.Second)
	if err != nil {
		return inferBrowserState(loginCtx, domain.AuthStateChallengeNeeded), fmt.Errorf("could not find Patreon submit button: %w", err)
	}
	if err := chromedp.Run(loginCtx, chromedp.Click(submitSelector, chromedp.ByQuery)); err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("submit Patreon login form: %w", err)
	}
	deadline := time.Now().Add(75 * time.Second)
	for time.Now().Before(deadline) {
		completedTOTP, err := maybeCompleteTOTPChallenge(loginCtx, auth)
		if err != nil {
			return domain.AuthStateChallengeNeeded, err
		}
		if completedTOTP {
			time.Sleep(2 * time.Second)
		}
		snapshot := currentPageSnapshot(loginCtx)
		if !strings.Contains(strings.ToLower(snapshot.URL), "/login") {
			if err := saveCurrentSession(loginCtx, auth.SessionPath); err == nil {
				return domain.AuthStateAuthenticated, nil
			}
		}
		switch inferSnapshotState(snapshot, "") {
		case domain.AuthStateChallengeNeeded:
			return domain.AuthStateChallengeNeeded, fmt.Errorf("Patreon presented an interactive login challenge")
		case domain.AuthStateReauthRequired:
			return domain.AuthStateReauthRequired, fmt.Errorf("Patreon rejected the provided credentials")
		}
		select {
		case <-loginCtx.Done():
			return domain.AuthStateChallengeNeeded, loginCtx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if err := saveCurrentSession(loginCtx, auth.SessionPath); err == nil {
		return domain.AuthStateAuthenticated, nil
	}
	return inferBrowserState(loginCtx, domain.AuthStateChallengeNeeded), fmt.Errorf("Patreon login did not reach an authenticated session for source %q", source.ID)
}

func maybeCompleteTOTPChallenge(ctx context.Context, auth config.AuthProfile) (bool, error) {
	if strings.TrimSpace(auth.TOTPSecretEnv) == "" {
		return false, nil
	}
	secret := strings.TrimSpace(os.Getenv(auth.TOTPSecretEnv))
	if secret == "" {
		return false, fmt.Errorf("auth profile %q expects TOTP secret in %s", auth.ID, auth.TOTPSecretEnv)
	}
	selector, err := waitForSelector(ctx, totpInputSelectors, 2*time.Second)
	if err != nil || selector == "" {
		return false, nil
	}
	code, err := totp.GenerateCode(secret, time.Now().UTC())
	if err != nil {
		return false, fmt.Errorf("generate Patreon TOTP code: %w", err)
	}
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Focus(selector, chromedp.ByQuery),
		chromedp.SendKeys(selector, code, chromedp.ByQuery),
	); err != nil {
		return false, fmt.Errorf("fill Patreon TOTP field: %w", err)
	}
	submitSelector, err := waitForSelector(ctx, submitButtonSelectors, 5*time.Second)
	if err == nil && submitSelector != "" {
		if clickErr := chromedp.Run(ctx, chromedp.Click(submitSelector, chromedp.ByQuery)); clickErr != nil {
			return false, fmt.Errorf("submit Patreon TOTP form: %w", clickErr)
		}
	}
	return true, nil
}

func resolveChromiumBinary() string {
	for _, candidate := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
		path, err := exec.LookPath(candidate)
		if err == nil {
			return path
		}
	}
	return ""
}

func waitForSelector(ctx context.Context, selectors []string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		selector, err := firstVisibleSelector(ctx, selectors)
		if err == nil && selector != "" {
			return selector, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return "", context.DeadlineExceeded
}

func firstVisibleSelector(ctx context.Context, selectors []string) (string, error) {
	payload, err := json.Marshal(selectors)
	if err != nil {
		return "", err
	}
	script := fmt.Sprintf(`(() => {
		const selectors = %s;
		for (const selector of selectors) {
			const element = document.querySelector(selector);
			if (!element) continue;
			const rect = element.getBoundingClientRect();
			const style = window.getComputedStyle(element);
			if (rect.width === 0 || rect.height === 0) continue;
			if (style.display === 'none' || style.visibility === 'hidden') continue;
			return selector;
		}
		return '';
	})()`, payload)
	var selector string
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &selector)); err != nil {
		return "", err
	}
	return selector, nil
}

func saveCurrentSession(ctx context.Context, sessionPath string) error {
	var cookies []*network.Cookie
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
		var err error
		cookies, err = network.GetCookies().
			WithURLs([]string{"https://www.patreon.com", "https://www.patreon.com/home"}).
			Do(actionCtx)
		return err
	}))
	if err != nil {
		return err
	}
	if len(cookies) == 0 {
		return errors.New("no Patreon cookies available to persist")
	}
	var userAgent string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`navigator.userAgent`, &userAgent)); err != nil {
		userAgent = defaultUserAgent
	}
	bundle := sessionBundle{
		Provider:  "patreon",
		SavedAt:   time.Now().UTC(),
		UserAgent: firstNonEmpty(userAgent, defaultUserAgent),
		Cookies:   make([]sessionCookie, 0, len(cookies)),
	}
	for _, cookie := range cookies {
		sameSite := ""
		if cookie.SameSite != "" {
			sameSite = string(cookie.SameSite)
		}
		bundle.Cookies = append(bundle.Cookies, sessionCookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     cookie.Path,
			Expires:  cookie.Expires,
			HTTPOnly: cookie.HTTPOnly,
			Secure:   cookie.Secure,
			SameSite: sameSite,
		})
	}
	return saveSessionBundle(sessionPath, bundle)
}

func currentPageSnapshot(ctx context.Context) pageSnapshot {
	snapshot := pageSnapshot{}
	_ = chromedp.Run(ctx,
		chromedp.Location(&snapshot.URL),
		chromedp.Title(&snapshot.Title),
		chromedp.Evaluate(`document.body ? document.body.innerText : ''`, &snapshot.Text),
	)
	return snapshot
}

func inferBrowserState(ctx context.Context, fallback domain.AuthState) domain.AuthState {
	return inferSnapshotState(currentPageSnapshot(ctx), fallback)
}

func inferSnapshotState(snapshot pageSnapshot, fallback domain.AuthState) domain.AuthState {
	blob := strings.ToLower(strings.Join([]string{snapshot.URL, snapshot.Title, snapshot.Text}, "\n"))
	switch {
	case strings.Contains(blob, "captcha"),
		strings.Contains(blob, "just a moment"),
		strings.Contains(blob, "security check"),
		strings.Contains(blob, "verify it"),
		strings.Contains(blob, "verify your identity"),
		strings.Contains(blob, "two-factor"),
		strings.Contains(blob, "two factor"),
		strings.Contains(blob, "one-time code"),
		strings.Contains(blob, "authenticator app"):
		return domain.AuthStateChallengeNeeded
	case strings.Contains(blob, "incorrect password"),
		strings.Contains(blob, "incorrect email"),
		strings.Contains(blob, "unable to log you in"),
		strings.Contains(blob, "could not log you in"),
		strings.Contains(blob, "invalid email"),
		strings.Contains(blob, "invalid password"):
		return domain.AuthStateReauthRequired
	default:
		return fallback
	}
}

func (c *Client) currentUserAPIURL() string {
	base, _ := url.Parse(c.apiBaseURL + "/api/current_user")
	query := base.Query()
	query.Set("include", "active_memberships.campaign")
	query.Set("fields[campaign]", "avatar_photo_image_urls,name,published_at,url,vanity,is_nsfw,url_for_current_user")
	query.Set("fields[member]", "is_free_member,is_free_trial")
	query.Set("json-api-version", "1.0")
	query.Set("json-api-use-default-includes", "false")
	base.RawQuery = query.Encode()
	return base.String()
}

func (c *Client) postsIndexAPIURL(campaignID, currentUserID string) string {
	base, _ := url.Parse(c.apiBaseURL + "/api/posts")
	query := base.Query()
	query.Set("include", strings.Join([]string{
		"campaign",
		"user",
		"collections",
		"user_defined_tags",
		"attachments_media",
	}, ","))
	query.Set("sort", "-published_at")
	query.Set("filter[contains_exclusive_posts]", "true")
	query.Set("filter[is_draft]", "false")
	query.Set("filter[campaign_id]", campaignID)
	if currentUserID != "" {
		query.Set("filter[accessible_by_user_id]", currentUserID)
	}
	query.Set("json-api-version", "1.0")
	query.Set("json-api-use-default-includes", "false")
	base.RawQuery = query.Encode()
	return base.String()
}

func (c *Client) postDetailAPIURL(postID string) string {
	base, _ := url.Parse(c.apiBaseURL + "/api/posts/" + postID)
	query := base.Query()
	query.Set("include", strings.Join([]string{
		"campaign",
		"user",
		"collections",
		"user_defined_tags",
		"attachments_media",
	}, ","))
	query.Set("json-api-version", "1.0")
	query.Set("json-api-use-default-includes", "false")
	base.RawQuery = query.Encode()
	return base.String()
}

func sourceHandle(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	parts := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
	if len(parts) < 2 {
		return "", fmt.Errorf("could not derive Patreon handle from %q", rawURL)
	}
	return parts[len(parts)-2], nil
}

func buildLiveSyncCursor(docs []provider.ReleaseDocument) string {
	if len(docs) == 0 {
		return ""
	}
	recent := make([]string, 0, min(len(docs), liveSyncCursorRecentKeep))
	seen := map[string]struct{}{}
	for _, doc := range docs {
		id := strings.TrimSpace(doc.Normalized.ProviderReleaseID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		recent = append(recent, id)
		if len(recent) >= liveSyncCursorRecentKeep {
			break
		}
	}
	if len(recent) == 0 {
		return ""
	}
	payload, err := json.Marshal(liveSyncCursor{
		Version:          liveSyncCursorVersion,
		Lookback:         liveSyncCursorLookback,
		RecentReleaseIDs: recent,
	})
	if err != nil {
		return ""
	}
	return string(payload)
}

func parseLiveSyncCursor(raw string) *liveSyncCursor {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var cursor liveSyncCursor
	if err := json.Unmarshal([]byte(raw), &cursor); err != nil {
		return nil
	}
	if cursor.Version != liveSyncCursorVersion {
		return nil
	}
	if cursor.Lookback <= 0 {
		cursor.Lookback = liveSyncCursorLookback
	}
	cursor.RecentReleaseIDs = compactRecentReleaseIDs(cursor.RecentReleaseIDs)
	if len(cursor.RecentReleaseIDs) == 0 {
		return nil
	}
	return &cursor
}

func compactRecentReleaseIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, min(len(ids), liveSyncCursorRecentKeep))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) >= liveSyncCursorRecentKeep {
			break
		}
	}
	return out
}

func selectedAttachmentIndex(attachments []domain.Attachment, selected domain.Attachment) int {
	for idx, attachment := range attachments {
		if attachment.FileName == selected.FileName &&
			attachment.MIMEType == selected.MIMEType &&
			attachment.DownloadURL == selected.DownloadURL {
			return idx
		}
	}
	return -1
}

func trimPostIDsWithCursor(ids []string, cursor *liveSyncCursor) []string {
	if cursor == nil || len(ids) == 0 {
		return ids
	}
	known := map[string]struct{}{}
	for _, id := range cursor.RecentReleaseIDs {
		if strings.TrimSpace(id) != "" {
			known[id] = struct{}{}
		}
	}
	if len(known) == 0 || cursor.Lookback <= 0 {
		return ids
	}
	for idx, id := range ids {
		if idx+1 < cursor.Lookback {
			continue
		}
		if _, ok := known[id]; ok {
			return ids[:idx+1]
		}
	}
	return ids
}

func extractCollectionPostIDs(body []byte) []string {
	matches := collectionPostLinkPattern.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		id := strings.TrimSpace(match[1])
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func campaignMatchesHandle(vanity, campaignURL, handle string) bool {
	handle = normalizeHandleToken(handle)
	if handle == "" {
		return false
	}
	if normalizeHandleToken(vanity) == handle {
		return true
	}
	if campaignURL == "" {
		return false
	}
	parsed, err := url.Parse(campaignURL)
	if err != nil {
		return false
	}
	parts := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
	if len(parts) == 0 {
		return false
	}
	return normalizeHandleToken(parts[len(parts)-1]) == handle
}

func resolveRelativeURL(currentURL, nextURL string) string {
	if strings.TrimSpace(nextURL) == "" {
		return ""
	}
	next, err := url.Parse(nextURL)
	if err != nil || next.IsAbs() {
		return nextURL
	}
	current, err := url.Parse(currentURL)
	if err != nil {
		return nextURL
	}
	return current.ResolveReference(next).String()
}

func sessionProfileDir(sessionPath string) string {
	if filepath.Ext(sessionPath) == "" {
		return filepath.Join(sessionPath, "chromium-profile")
	}
	return strings.TrimSuffix(sessionPath, filepath.Ext(sessionPath)) + ".profile"
}

func sessionCacheRoot(sessionPath string) string {
	if filepath.Ext(sessionPath) == "" {
		return filepath.Join(sessionPath, "cache")
	}
	return strings.TrimSuffix(sessionPath, filepath.Ext(sessionPath)) + ".cache"
}

func sanitizeAttachmentFileName(input string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "\n", " ", "\r", " ")
	return replacer.Replace(strings.TrimSpace(input))
}

func normalizeHandleToken(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	var builder strings.Builder
	for _, r := range input {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func normalizeAuthMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

type sourceKind string

const (
	sourceKindCreatorFeed sourceKind = "creator_feed"
	sourceKindCollection  sourceKind = "collection"
	sourceKindUnknown     sourceKind = ""
)

func detectSourceKind(rawURL string) sourceKind {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return sourceKindUnknown
	}
	parts := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
	if len(parts) >= 2 && parts[0] == "collection" {
		return sourceKindCollection
	}
	if len(parts) >= 2 && parts[len(parts)-1] == "posts" {
		return sourceKindCreatorFeed
	}
	if creatorPattern.MatchString(strings.TrimSpace(rawURL)) {
		return sourceKindCreatorFeed
	}
	if collectionPattern.MatchString(strings.TrimSpace(rawURL)) {
		return sourceKindCollection
	}
	return sourceKindUnknown
}

const defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"
