package patreon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestListReleasesLiveSessionReuse(t *testing.T) {
	t.Parallel()

	server := newPatreonAPITestServer(t)
	client := New()
	client.apiBaseURL = server.URL
	client.bootstrap = func(context.Context, config.AuthProfile, config.SourceConfig, string) (domain.AuthState, error) {
		t.Fatalf("bootstrap should not run when a persisted session is available")
		return domain.AuthStateReauthRequired, nil
	}

	tmp := t.TempDir()
	sessionPath := filepath.Join(tmp, "patreon.json")
	writeTestSessionBundle(t, sessionPath, server.URL)

	result, err := client.ListReleases(context.Background(), config.AuthProfile{
		ID:          "patreon-default",
		Provider:    "patreon",
		Mode:        "username_password",
		SessionPath: sessionPath,
	}, config.SourceConfig{
		ID:          "example-creator",
		Provider:    "patreon",
		URL:         "https://www.patreon.com/c/ExampleCreator/posts",
		AuthProfile: "patreon-default",
		Enabled:     true,
	}, nil)
	if err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if result.AuthState != domain.AuthStateAuthenticated {
		t.Fatalf("authState = %q, want %q", result.AuthState, domain.AuthStateAuthenticated)
	}
	if len(result.Documents) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(result.Documents))
	}
	if got, want := result.Documents[0].Normalized.ProviderReleaseID, "123"; got != want {
		t.Fatalf("ProviderReleaseID = %q, want %q", got, want)
	}
	if len(result.Documents[0].Normalized.Attachments) != 1 {
		t.Fatalf("len(attachments) = %d, want 1", len(result.Documents[0].Normalized.Attachments))
	}
	if got := result.Documents[0].Normalized.Attachments[0].LocalPath; got != "" {
		t.Fatalf("expected attachment to remain remote until prepare, got %q", got)
	}
}

func TestListReleasesBootstrapsWhenSessionMissing(t *testing.T) {
	server := newPatreonAPITestServer(t)
	client := New()
	client.apiBaseURL = server.URL

	tmp := t.TempDir()
	sessionPath := filepath.Join(tmp, "patreon.json")
	bootstrapped := false
	client.bootstrap = func(_ context.Context, auth config.AuthProfile, _ config.SourceConfig, profileDir string) (domain.AuthState, error) {
		bootstrapped = true
		if profileDir == "" {
			t.Fatalf("expected profile dir for bootstrap")
		}
		writeTestSessionBundle(t, auth.SessionPath, server.URL)
		return domain.AuthStateAuthenticated, nil
	}

	t.Setenv("PATREON_USERNAME", "user@example.com")
	t.Setenv("PATREON_PASSWORD", "not-used-in-test")

	result, err := client.ListReleases(context.Background(), config.AuthProfile{
		ID:          "patreon-default",
		Provider:    "patreon",
		Mode:        "username_password",
		UsernameEnv: "PATREON_USERNAME",
		PasswordEnv: "PATREON_PASSWORD",
		SessionPath: sessionPath,
	}, config.SourceConfig{
		ID:          "example-creator",
		Provider:    "patreon",
		URL:         "https://www.patreon.com/c/ExampleCreator/posts",
		AuthProfile: "patreon-default",
		Enabled:     true,
	}, nil)
	if err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if !bootstrapped {
		t.Fatalf("expected bootstrap to run")
	}
	if result.AuthState != domain.AuthStateAuthenticated {
		t.Fatalf("authState = %q, want %q", result.AuthState, domain.AuthStateAuthenticated)
	}
	if len(result.Documents) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(result.Documents))
	}
}

func TestListReleasesReturnsChallengeRequiredWhenBootstrapFails(t *testing.T) {
	client := New()
	client.bootstrap = func(context.Context, config.AuthProfile, config.SourceConfig, string) (domain.AuthState, error) {
		return domain.AuthStateChallengeNeeded, fmt.Errorf("Patreon requested a CAPTCHA")
	}

	t.Setenv("PATREON_USERNAME", "user@example.com")
	t.Setenv("PATREON_PASSWORD", "not-used-in-test")

	result, err := client.ListReleases(context.Background(), config.AuthProfile{
		ID:          "patreon-default",
		Provider:    "patreon",
		Mode:        "username_password",
		UsernameEnv: "PATREON_USERNAME",
		PasswordEnv: "PATREON_PASSWORD",
		SessionPath: filepath.Join(t.TempDir(), "patreon.json"),
	}, config.SourceConfig{
		ID:          "example-creator",
		Provider:    "patreon",
		URL:         "https://www.patreon.com/c/ExampleCreator/posts",
		AuthProfile: "patreon-default",
		Enabled:     true,
	}, nil)
	if err == nil {
		t.Fatal("expected bootstrap failure")
	}
	if result.AuthState != domain.AuthStateChallengeNeeded {
		t.Fatalf("authState = %q, want %q", result.AuthState, domain.AuthStateChallengeNeeded)
	}
}

func TestPrepareReleaseDownloadsSelectedAttachment(t *testing.T) {
	t.Parallel()

	server := newPatreonAPITestServer(t)
	client := New()
	client.apiBaseURL = server.URL

	tmp := t.TempDir()
	sessionPath := filepath.Join(tmp, "patreon.json")
	writeTestSessionBundle(t, sessionPath, server.URL)

	result, err := client.ListReleases(context.Background(), config.AuthProfile{
		ID:          "patreon-default",
		Provider:    "patreon",
		Mode:        "username_password",
		SessionPath: sessionPath,
	}, config.SourceConfig{
		ID:          "example-creator",
		Provider:    "patreon",
		URL:         "https://www.patreon.com/c/ExampleCreator/posts",
		AuthProfile: "patreon-default",
		Enabled:     true,
	}, nil)
	if err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	prepared, authState, err := client.PrepareRelease(context.Background(), config.AuthProfile{
		ID:          "patreon-default",
		Provider:    "patreon",
		Mode:        "username_password",
		SessionPath: sessionPath,
	}, config.SourceConfig{
		ID:          "example-creator",
		Provider:    "patreon",
		URL:         "https://www.patreon.com/c/ExampleCreator/posts",
		AuthProfile: "patreon-default",
		Enabled:     true,
	}, result.Documents[0], domain.TrackDecision{
		ContentStrategy:    domain.ContentStrategyAttachmentPreferred,
		AttachmentGlob:     []string{"*.epub", "*.pdf"},
		AttachmentPriority: []string{"epub", "pdf"},
	})
	if err != nil {
		t.Fatalf("PrepareRelease() error = %v", err)
	}
	if authState != domain.AuthStateAuthenticated {
		t.Fatalf("authState = %q, want %q", authState, domain.AuthStateAuthenticated)
	}
	localPath := prepared.Normalized.Attachments[0].LocalPath
	if localPath == "" {
		t.Fatal("expected prepared release to cache the selected attachment")
	}
	if _, err := os.Stat(localPath); err != nil {
		t.Fatalf("cached attachment missing: %v", err)
	}
}

func TestListReleasesUsesStoredCursorLookback(t *testing.T) {
	t.Parallel()

	postIDs := make([]string, 0, 40)
	for idx := 40; idx >= 1; idx-- {
		postIDs = append(postIDs, fmt.Sprintf("%d", idx))
	}
	server, detailRequests := newPatreonPagedTestServer(t, postIDs)
	client := New()
	client.apiBaseURL = server.URL
	client.bootstrap = func(context.Context, config.AuthProfile, config.SourceConfig, string) (domain.AuthState, error) {
		t.Fatalf("bootstrap should not run when a persisted session is available")
		return domain.AuthStateReauthRequired, nil
	}

	tmp := t.TempDir()
	sessionPath := filepath.Join(tmp, "patreon.json")
	writeTestSessionBundle(t, sessionPath, server.URL)
	cursorJSON, err := json.Marshal(liveSyncCursor{
		Version:          liveSyncCursorVersion,
		Lookback:         5,
		RecentReleaseIDs: postIDs,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	result, err := client.ListReleases(context.Background(), config.AuthProfile{
		ID:          "patreon-default",
		Provider:    "patreon",
		Mode:        "username_password",
		SessionPath: sessionPath,
	}, config.SourceConfig{
		ID:          "example-creator",
		Provider:    "patreon",
		URL:         "https://www.patreon.com/c/ExampleCreator/posts",
		AuthProfile: "patreon-default",
		Enabled:     true,
	}, &domain.Source{ID: "example-creator", SyncCursor: string(cursorJSON)})
	if err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if got, want := len(result.Documents), 5; got != want {
		t.Fatalf("len(docs) = %d, want %d", got, want)
	}
	if got := atomic.LoadInt32(detailRequests); got != 5 {
		t.Fatalf("detail requests = %d, want 5", got)
	}
}

func TestListReleasesCollectionSourceUsesHTMLLinks(t *testing.T) {
	t.Parallel()

	server := newPatreonCollectionTestServer(t)
	client := New()
	client.apiBaseURL = server.URL

	tmp := t.TempDir()
	sessionPath := filepath.Join(tmp, "patreon.json")
	writeTestSessionBundle(t, sessionPath, server.URL)

	result, err := client.ListReleases(context.Background(), config.AuthProfile{
		ID:          "patreon-default",
		Provider:    "patreon",
		Mode:        "username_password",
		SessionPath: sessionPath,
	}, config.SourceConfig{
		ID:          "example-collection",
		Provider:    "patreon",
		URL:         server.URL + "/collection/abc123",
		AuthProfile: "patreon-default",
		Enabled:     true,
	}, nil)
	if err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if got, want := len(result.Documents), 2; got != want {
		t.Fatalf("len(docs) = %d, want %d", got, want)
	}
	if got, want := result.Documents[0].Normalized.SourceType, "collection"; got != want {
		t.Fatalf("SourceType = %q, want %q", got, want)
	}
}

func TestValidateSourceAcceptsCollectionURL(t *testing.T) {
	t.Parallel()

	client := New()
	if err := client.ValidateSource(config.SourceConfig{
		ID:       "example-collection",
		Provider: "patreon",
		URL:      "https://www.patreon.com/collection/abc123",
	}); err != nil {
		t.Fatalf("ValidateSource() error = %v", err)
	}
}

func TestCampaignMatchesHandleNormalizesVanityVariants(t *testing.T) {
	t.Parallel()

	if !campaignMatchesHandle("plum_parrot", "https://www.patreon.com/plum_parrot", "PlumParrot") {
		t.Fatal("expected PlumParrot to match plum_parrot vanity")
	}
}

func TestCookieHeaderForURLMatchesPatreonSessionCookies(t *testing.T) {
	t.Parallel()

	bundle := &sessionBundle{
		UserAgent: "serial-sync-test",
		Cookies: []sessionCookie{
			{Name: "session_id", Value: "abc", Domain: ".patreon.com", Path: "/", Secure: true},
			{Name: "stream_user_token", Value: "def", Domain: "www.patreon.com", Path: "/api", Secure: true},
			{Name: "skip_me", Value: "ghi", Domain: "example.com", Path: "/", Secure: true},
		},
	}

	got := cookieHeaderForURL(bundle, "https://www.patreon.com/api/current_user")
	if !strings.Contains(got, "session_id=abc") {
		t.Fatalf("cookieHeaderForURL() = %q, want session cookie", got)
	}
	if !strings.Contains(got, "stream_user_token=def") {
		t.Fatalf("cookieHeaderForURL() = %q, want API-scoped cookie", got)
	}
	if strings.Contains(got, "skip_me=ghi") {
		t.Fatalf("cookieHeaderForURL() = %q, did not expect unrelated domain cookie", got)
	}
}

func TestClassifyHTTPAuthFailureIgnoresChallengeWordsInJSON(t *testing.T) {
	t.Parallel()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.api+json"}},
	}
	authState, err := classifyHTTPAuthFailure(resp, []byte(`{"content":"just a moment later the chapter begins"}`))
	if err != nil {
		t.Fatalf("classifyHTTPAuthFailure() error = %v", err)
	}
	if authState != "" {
		t.Fatalf("authState = %q, want empty", authState)
	}
}

func TestClassifyHTTPAuthFailureFlagsHTMLChallengePage(t *testing.T) {
	t.Parallel()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
	}
	authState, err := classifyHTTPAuthFailure(resp, []byte("<html><title>Just a moment...</title></html>"))
	if err == nil {
		t.Fatal("expected challenge page error")
	}
	if authState != domain.AuthStateChallengeNeeded {
		t.Fatalf("authState = %q, want %q", authState, domain.AuthStateChallengeNeeded)
	}
}

func newPatreonAPITestServer(t *testing.T) *httptest.Server {
	t.Helper()

	var server *httptest.Server
	mux := http.NewServeMux()
	requireAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Cookie"), "session_id=patreon-test-session") {
				http.Error(w, "login required", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("/api/current_user", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"id":"user-1"},"included":[{"id":"campaign-1","type":"campaign","attributes":{"name":"Example Creator","url":"https://www.patreon.com/ExampleCreator","vanity":"ExampleCreator"}}]}`)
	}))
	mux.HandleFunc("/api/posts", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"123"}],"links":{"next":""}}`)
	}))
	mux.HandleFunc("/api/posts/123", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
  "data": {
    "id": "123",
    "type": "post",
    "attributes": {
      "title": "Chapter 1",
      "post_type": "text",
      "current_user_can_view": true,
      "url": "https://www.patreon.com/posts/chapter-1-123",
      "content": "<p>Hello world</p>",
      "content_json_string": "",
      "published_at": "2026-04-01T00:00:00Z",
      "edited_at": "2026-04-01T00:00:00Z"
    },
    "relationships": {
      "campaign": { "data": { "id": "campaign-1" } },
      "user": { "data": { "id": "creator-user-1" } },
      "collections": { "data": [] },
      "user_defined_tags": { "data": [] },
      "attachments_media": { "data": [{ "id": "attachment-1" }] }
    }
  },
  "included": [
    {
      "id": "campaign-1",
      "type": "campaign",
      "attributes": { "name": "Example Creator" },
      "relationships": { "creator": { "data": { "id": "creator-user-1" } } }
    },
    {
      "id": "creator-user-1",
      "type": "user",
      "attributes": { "full_name": "Example Creator", "vanity": "ExampleCreator" },
      "relationships": {}
    },
    {
      "id": "attachment-1",
      "type": "media",
      "attributes": {
        "file_name": "chapter-1.epub",
        "mimetype": "application/epub+zip",
        "download_url": %q
      },
      "relationships": {}
    }
  ]
}`, server.URL+"/files/chapter-1.epub")
	}))
	mux.HandleFunc("/files/chapter-1.epub", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/epub+zip")
		fmt.Fprint(w, "epub-bytes")
	})

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func newPatreonPagedTestServer(t *testing.T, postIDs []string) (*httptest.Server, *int32) {
	t.Helper()

	var detailRequests int32
	var server *httptest.Server
	mux := http.NewServeMux()
	requireAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Cookie"), "session_id=patreon-test-session") {
				http.Error(w, "login required", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("/api/current_user", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"id":"user-1"},"included":[{"id":"campaign-1","type":"campaign","attributes":{"name":"Example Creator","url":"https://www.patreon.com/ExampleCreator","vanity":"ExampleCreator"}}]}`)
	}))
	mux.HandleFunc("/api/posts", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		const pageSize = 10
		page := 1
		if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
			fmt.Sscanf(raw, "%d", &page)
			if page < 1 {
				page = 1
			}
		}
		start := (page - 1) * pageSize
		if start > len(postIDs) {
			start = len(postIDs)
		}
		end := start + pageSize
		if end > len(postIDs) {
			end = len(postIDs)
		}
		var items []string
		for _, id := range postIDs[start:end] {
			items = append(items, fmt.Sprintf(`{"id":"%s"}`, id))
		}
		next := ""
		if end < len(postIDs) {
			next = fmt.Sprintf(`/api/posts?page=%d`, page+1)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[%s],"links":{"next":%q}}`, strings.Join(items, ","), next)
	}))
	mux.HandleFunc("/api/posts/", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		postID := strings.TrimPrefix(r.URL.Path, "/api/posts/")
		atomic.AddInt32(&detailRequests, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
  "data": {
    "id": %q,
    "type": "post",
    "attributes": {
      "title": "Chapter %s",
      "post_type": "text",
      "current_user_can_view": true,
      "url": "https://www.patreon.com/posts/chapter-%s-%s",
      "content": "<p>Hello %s</p>",
      "content_json_string": "",
      "published_at": "2026-04-01T00:00:00Z",
      "edited_at": "2026-04-01T00:00:00Z"
    },
    "relationships": {
      "campaign": { "data": { "id": "campaign-1" } },
      "user": { "data": { "id": "creator-user-1" } },
      "collections": { "data": [] },
      "user_defined_tags": { "data": [] },
      "attachments_media": { "data": [] }
    }
  },
  "included": [
    {
      "id": "campaign-1",
      "type": "campaign",
      "attributes": { "name": "Example Creator" },
      "relationships": { "creator": { "data": { "id": "creator-user-1" } } }
    },
    {
      "id": "creator-user-1",
      "type": "user",
      "attributes": { "full_name": "Example Creator", "vanity": "ExampleCreator" },
      "relationships": {}
    }
  ]
}`, postID, postID, postID, postID, postID)
	}))

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &detailRequests
}

func newPatreonCollectionTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	var server *httptest.Server
	mux := http.NewServeMux()
	requireAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Cookie"), "session_id=patreon-test-session") {
				http.Error(w, "login required", http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("/api/current_user", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"id":"user-1"},"included":[]}`)
	}))
	mux.HandleFunc("/collection/abc123", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body>
<a href="https://www.patreon.com/posts/chapter-one-123">First</a>
<a href="https://www.patreon.com/posts/chapter-two-456">Second</a>
<a href="https://www.patreon.com/posts/chapter-two-456">Second duplicate</a>
</body></html>`)
	}))
	mux.HandleFunc("/api/posts/123", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, collectionPostJSON("123"))
	}))
	mux.HandleFunc("/api/posts/456", requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, collectionPostJSON("456"))
	}))

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func collectionPostJSON(id string) string {
	return fmt.Sprintf(`{
  "data": {
    "id": %q,
    "type": "post",
    "attributes": {
      "title": "Collection Chapter %s",
      "post_type": "text",
      "current_user_can_view": true,
      "url": "https://www.patreon.com/posts/collection-%s",
      "content": "<p>Hello %s</p>",
      "content_json_string": "",
      "published_at": "2026-04-01T00:00:00Z",
      "edited_at": "2026-04-01T00:00:00Z"
    },
    "relationships": {
      "campaign": { "data": { "id": "campaign-1" } },
      "user": { "data": { "id": "creator-user-1" } },
      "collections": { "data": [] },
      "user_defined_tags": { "data": [] },
      "attachments_media": { "data": [] }
    }
  },
  "included": [
    {
      "id": "campaign-1",
      "type": "campaign",
      "attributes": { "name": "Example Creator" },
      "relationships": { "creator": { "data": { "id": "creator-user-1" } } }
    },
    {
      "id": "creator-user-1",
      "type": "user",
      "attributes": { "full_name": "Example Creator", "vanity": "ExampleCreator" },
      "relationships": {}
    }
  ]
}`, id, id, id, id)
}

func writeTestSessionBundle(t *testing.T, path string, baseURL string) {
	t.Helper()

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	bundle := sessionBundle{
		Provider:  "patreon",
		UserAgent: "serial-sync-test",
		Cookies: []sessionCookie{
			{
				Name:     "session_id",
				Value:    "patreon-test-session",
				Domain:   parsed.Hostname(),
				Path:     "/",
				HTTPOnly: true,
			},
		},
	}
	if err := saveSessionBundle(path, bundle); err != nil {
		t.Fatalf("saveSessionBundle() error = %v", err)
	}
}
