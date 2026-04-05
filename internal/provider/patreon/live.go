package patreon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
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
			Name   string `json:"name"`
			URL    string `json:"url"`
			Vanity string `json:"vanity"`
		} `json:"attributes"`
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

func (c *Client) listLiveReleases(ctx context.Context, auth config.AuthProfile, source config.SourceConfig) ([]provider.ReleaseDocument, domain.AuthState, error) {
	session, authState, err := c.ensureLiveSession(ctx, auth, source)
	if err != nil {
		return nil, authState, err
	}
	postIDs, authState, err := c.listPostIDs(ctx, session, source)
	if err != nil {
		return nil, authState, err
	}
	docs := make([]provider.ReleaseDocument, 0, len(postIDs))
	for _, postID := range postIDs {
		raw, authState, err := c.fetchPostDetail(ctx, session, source, postID)
		if err != nil {
			return nil, authState, err
		}
		norm, err := parsePost(raw, "")
		if err != nil {
			return nil, domain.AuthStateReauthRequired, fmt.Errorf("parse Patreon post %s: %w", postID, err)
		}
		if err := c.downloadLiveAttachments(ctx, session, auth, source, &norm); err != nil {
			return nil, domain.AuthStateReauthRequired, err
		}
		docs = append(docs, provider.ReleaseDocument{
			Normalized: norm,
			RawJSON:    append(json.RawMessage(nil), raw...),
		})
	}
	provider.SortReleaseDocuments(docs)
	return docs, domain.AuthStateAuthenticated, nil
}

func (c *Client) ensureLiveSession(ctx context.Context, auth config.AuthProfile, source config.SourceConfig) (*liveSession, domain.AuthState, error) {
	session, authState, err := c.resolveLiveSession(ctx, auth, source)
	if err == nil {
		return session, authState, nil
	}
	if authState == domain.AuthStateChallengeNeeded {
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
	authState, bootErr := c.bootstrap(ctx, auth, source, sessionProfileDir(auth.SessionPath))
	if bootErr != nil {
		return nil, authState, bootErr
	}
	session, authState, err = c.resolveLiveSession(ctx, auth, source)
	if err != nil {
		return nil, authState, err
	}
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
	client, err := httpClientFromSession(*bundle)
	if err != nil {
		return nil, domain.AuthStateReauthRequired, err
	}
	user, authState, err := c.fetchCurrentUser(ctx, client, source, bundle.UserAgent)
	if err != nil {
		return nil, authState, err
	}
	campaign, authState, err := c.resolveCampaign(ctx, client, source, bundle.UserAgent, user)
	if err != nil {
		return nil, authState, err
	}
	return &liveSession{
		bundle:        *bundle,
		client:        client,
		currentUserID: user.Data.ID,
		campaign:      campaign,
	}, domain.AuthStateAuthenticated, nil
}

func (c *Client) fetchCurrentUser(ctx context.Context, client *http.Client, source config.SourceConfig, userAgent string) (*currentUserEnvelope, domain.AuthState, error) {
	requestURL := c.currentUserAPIURL()
	body, authState, err := c.get(ctx, client, requestURL, source.URL, userAgent, "application/json")
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

func (c *Client) resolveCampaign(ctx context.Context, client *http.Client, source config.SourceConfig, userAgent string, user *currentUserEnvelope) (campaignInfo, domain.AuthState, error) {
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
	body, authState, err := c.get(ctx, client, source.URL, source.URL, userAgent, "text/html")
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

func (c *Client) listPostIDs(ctx context.Context, session *liveSession, source config.SourceConfig) ([]string, domain.AuthState, error) {
	nextURL := c.postsIndexAPIURL(session.campaign.ID, session.currentUserID)
	seen := map[string]struct{}{}
	ids := make([]string, 0, 32)
	for nextURL != "" {
		body, authState, err := c.get(ctx, session.client, nextURL, source.URL, session.bundle.UserAgent, "application/json")
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
		}
		nextURL = resolveRelativeURL(nextURL, page.Links.Next)
	}
	return ids, domain.AuthStateAuthenticated, nil
}

func (c *Client) fetchPostDetail(ctx context.Context, session *liveSession, source config.SourceConfig, postID string) ([]byte, domain.AuthState, error) {
	requestURL := c.postDetailAPIURL(postID)
	return c.get(ctx, session.client, requestURL, source.URL, session.bundle.UserAgent, "application/json")
}

func (c *Client) downloadLiveAttachments(ctx context.Context, session *liveSession, auth config.AuthProfile, source config.SourceConfig, release *domain.NormalizedRelease) error {
	for idx := range release.Attachments {
		attachment := &release.Attachments[idx]
		if attachment.LocalPath != "" || strings.TrimSpace(attachment.DownloadURL) == "" || strings.TrimSpace(attachment.FileName) == "" {
			continue
		}
		targetDir := filepath.Join(sessionCacheRoot(auth.SessionPath), "attachments", source.ID, release.ProviderReleaseID)
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return err
		}
		targetPath := filepath.Join(targetDir, sanitizeAttachmentFileName(attachment.FileName))
		if _, err := os.Stat(targetPath); err == nil {
			attachment.LocalPath = targetPath
			continue
		}
		body, authState, err := c.get(ctx, session.client, attachment.DownloadURL, source.URL, session.bundle.UserAgent, "*/*")
		if err != nil {
			return fmt.Errorf("download Patreon attachment %q (%s): %s: %w", attachment.FileName, attachment.DownloadURL, authState, err)
		}
		if err := os.WriteFile(targetPath, body, 0o644); err != nil {
			return err
		}
		attachment.LocalPath = targetPath
	}
	return nil
}

func (c *Client) get(ctx context.Context, client *http.Client, requestURL, referer, userAgent, accept string) ([]byte, domain.AuthState, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, domain.AuthStateReauthRequired, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", firstNonEmpty(userAgent, defaultUserAgent))
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, domain.AuthStateReauthRequired, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, domain.AuthStateReauthRequired, err
	}
	if authState, authErr := classifyHTTPAuthFailure(resp, body); authErr != nil {
		return nil, authState, authErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, domain.AuthStateReauthRequired, fmt.Errorf("unexpected Patreon response status %d for %s", resp.StatusCode, requestURL)
	}
	return body, domain.AuthStateAuthenticated, nil
}

func classifyHTTPAuthFailure(resp *http.Response, body []byte) (domain.AuthState, error) {
	bodyText := strings.ToLower(string(body))
	if location := resp.Header.Get("Location"); location != "" && strings.Contains(strings.ToLower(location), "/login") {
		return domain.AuthStateExpired, fmt.Errorf("Patreon redirected to login")
	}
	if strings.Contains(bodyText, "just a moment") || strings.Contains(bodyText, "cf-chl") || strings.Contains(bodyText, "captcha") {
		return domain.AuthStateChallengeNeeded, errors.New("Patreon presented a challenge page")
	}
	if strings.Contains(bodyText, "two-factor") || strings.Contains(bodyText, "two factor") || strings.Contains(bodyText, "one-time code") || strings.Contains(bodyText, "verify it") {
		return domain.AuthStateChallengeNeeded, errors.New("Patreon requires an interactive verification step")
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return domain.AuthStateExpired, fmt.Errorf("Patreon rejected the saved session with status %d", resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		return domain.AuthStateReauthRequired, fmt.Errorf("unexpected Patreon auth response status %d", resp.StatusCode)
	}
	return "", nil
}

func httpClientFromSession(bundle sessionBundle) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	grouped := map[string][]*http.Cookie{}
	for _, item := range bundle.Cookies {
		domainHost := strings.TrimPrefix(item.Domain, ".")
		if domainHost == "" {
			continue
		}
		cookie := &http.Cookie{
			Name:     item.Name,
			Value:    item.Value,
			Domain:   item.Domain,
			Path:     firstNonEmpty(item.Path, "/"),
			HttpOnly: item.HTTPOnly,
			Secure:   item.Secure,
		}
		if item.Expires > 0 {
			cookie.Expires = time.Unix(int64(item.Expires), 0).UTC()
		}
		grouped[domainHost] = append(grouped[domainHost], cookie)
	}
	for host, cookies := range grouped {
		u := &url.URL{Scheme: "https", Host: host, Path: "/"}
		jar.SetCookies(u, cookies)
	}
	return &http.Client{
		Jar:           jar,
		Timeout:       45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
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
	allocOptions := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocOptions = append(allocOptions,
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, allocOptions...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()
	loginCtx, loginCancel := context.WithTimeout(browserCtx, 2*time.Minute)
	defer loginCancel()
	if err := chromedp.Run(loginCtx, network.Enable()); err != nil {
		return domain.AuthStateReauthRequired, fmt.Errorf("start headless Chromium for Patreon auth: %w", err)
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
		chromedp.SetValue(emailSelector, "", chromedp.ByQuery),
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
		chromedp.SetValue(passwordSelector, "", chromedp.ByQuery),
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
	cookies, err := network.GetCookies().WithURLs([]string{"https://www.patreon.com"}).Do(ctx)
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

func campaignMatchesHandle(vanity, campaignURL, handle string) bool {
	handle = strings.ToLower(strings.TrimSpace(handle))
	if handle == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(vanity), handle) {
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
	return strings.EqualFold(parts[len(parts)-1], handle)
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

func normalizeAuthMode(mode string) string {
	return strings.ToLower(strings.TrimSpace(mode))
}

const defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"
