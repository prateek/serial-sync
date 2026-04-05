package patreon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
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

	docs, authState, err := client.ListReleases(context.Background(), config.AuthProfile{
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
	})
	if err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if authState != domain.AuthStateAuthenticated {
		t.Fatalf("authState = %q, want %q", authState, domain.AuthStateAuthenticated)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
	if got, want := docs[0].Normalized.ProviderReleaseID, "123"; got != want {
		t.Fatalf("ProviderReleaseID = %q, want %q", got, want)
	}
	if len(docs[0].Normalized.Attachments) != 1 {
		t.Fatalf("len(attachments) = %d, want 1", len(docs[0].Normalized.Attachments))
	}
	localPath := docs[0].Normalized.Attachments[0].LocalPath
	if localPath == "" {
		t.Fatalf("expected downloaded attachment local path")
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

	docs, authState, err := client.ListReleases(context.Background(), config.AuthProfile{
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
	})
	if err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if !bootstrapped {
		t.Fatalf("expected bootstrap to run")
	}
	if authState != domain.AuthStateAuthenticated {
		t.Fatalf("authState = %q, want %q", authState, domain.AuthStateAuthenticated)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d, want 1", len(docs))
	}
}

func TestListReleasesReturnsChallengeRequiredWhenBootstrapFails(t *testing.T) {
	client := New()
	client.bootstrap = func(context.Context, config.AuthProfile, config.SourceConfig, string) (domain.AuthState, error) {
		return domain.AuthStateChallengeNeeded, fmt.Errorf("Patreon requested a CAPTCHA")
	}

	t.Setenv("PATREON_USERNAME", "user@example.com")
	t.Setenv("PATREON_PASSWORD", "not-used-in-test")

	_, authState, err := client.ListReleases(context.Background(), config.AuthProfile{
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
	})
	if err == nil {
		t.Fatal("expected bootstrap failure")
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
