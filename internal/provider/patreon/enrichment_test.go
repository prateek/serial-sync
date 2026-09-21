package patreon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func TestCollectionIdentityUsesResourceTypeAndKeepsUnnamedReferences(t *testing.T) {
	raw := []byte(`{"data":{"id":"post-1","attributes":{"title":"Harbor Chapter 1","content":"<p>Filler &amp; text.</p>"},"relationships":{"campaign":{"data":{"id":"campaign-1"}},"collections":{"data":[{"id":"shared"},{"id":"unnamed"}]},"attachments_media":{"data":[{"id":"shared"}]}}},"included":[{"type":"collection","id":"shared","attributes":{"title":"Harbor"}},{"type":"media","id":"shared","attributes":{"file_name":"story.pdf","mimetype":"application/pdf"}}]}`)
	release, err := parsePost(raw, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Collections) != 1 || release.Collections[0] != "Harbor" || len(release.Attachments) != 1 {
		t.Fatalf("resource ID collision corrupted normalization: %+v", release)
	}
	if release.TextPlain != "Filler & text." {
		t.Fatalf("entities not decoded: %q", release.TextPlain)
	}
	if release.Enrichment == nil || len(release.Enrichment.Collections) != 2 || release.Enrichment.Collections[1].ID != "unnamed" || release.Enrichment.Collections[0].Key() != "patreon/campaign-1/collection/shared" {
		t.Fatalf("lost label identity: %+v", release.Enrichment)
	}
	release.Enrichment = nil
	enriched, err := New().EnrichCaptured(release, raw)
	if err != nil || enriched.Enrichment == nil {
		t.Fatalf("offline enrichment: %+v %v", enriched, err)
	}
}

func TestMetadataMembershipsUseOneRequestAndNeverBootstrap(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprint(expired), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if expired {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if !strings.Contains(r.URL.Path, "current_user") {
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"data":{"id":"user-1","type":"user"},"included":[{"id":"campaign-1","type":"campaign","attributes":{"name":"Fictional Author","vanity":"fictional-author","url":"https://www.patreon.com/fictional-author"}}]}`)
			}))
			defer server.Close()
			client := New()
			client.apiBaseURL = server.URL
			client.bootstrap = func(context.Context, config.AuthProfile, config.SourceConfig, string) (domain.AuthState, error) {
				t.Fatal("metadata inspection opened a browser")
				return domain.AuthStateReauthRequired, nil
			}
			path := filepath.Join(t.TempDir(), "session.json")
			writeTestSessionBundle(t, path, server.URL)
			_, err := client.DiscoverSources(context.Background(), config.AuthProfile{ID: "fixture", Provider: "patreon", Mode: "username_password", SessionPath: path}, nil, provider.DiscoverOptions{MetadataOnly: true, MembershipFilter: "all"})
			if expired {
				if err == nil || !strings.Contains(err.Error(), "setup auth") {
					t.Fatalf("expired session error: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if requests != 1 {
				t.Fatalf("requests=%d, want one metadata request", requests)
			}
		})
	}
}

func TestProfileOnlyAuthenticationBootstrapsThenVerifiesMetadata(t *testing.T) {
	requests, bootstraps := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprint(w, `{"data":{"id":"user-1","type":"user"}}`)
	}))
	defer server.Close()
	client := New()
	client.apiBaseURL = server.URL
	path := filepath.Join(t.TempDir(), "session.json")
	client.bootstrap = func(_ context.Context, _ config.AuthProfile, _ config.SourceConfig, _ string) (domain.AuthState, error) {
		bootstraps++
		writeTestSessionBundle(t, path, server.URL)
		return domain.AuthStateAuthenticated, nil
	}
	got, err := client.BootstrapProfile(context.Background(), config.AuthProfile{ID: "fictional", Mode: "username_password", SessionPath: path}, false)
	if err != nil || got.Action != "bootstrapped" || requests != 1 || bootstraps != 1 {
		t.Fatalf("profile bootstrap: %+v requests=%d bootstraps=%d err=%v", got, requests, bootstraps, err)
	}
}
