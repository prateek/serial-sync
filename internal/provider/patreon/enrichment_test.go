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

func TestCapturedAuthorProfileUsesLinkedIdentityAndKeepsArtworkRoles(t *testing.T) {
	raw := []byte(`{"data":{"id":"p1","relationships":{"user":{"data":{"id":"editor"}},"campaign":{"data":{"id":"c1"}},"collections":{"data":[{"id":"book"}]}}},"included":[{"id":"stranger","type":"user","attributes":{"full_name":"Wrong author","about":"Wrong biography"}},{"id":"c1","type":"campaign","attributes":{"name":"Campaign","cover_photo_url":"https://example.com/banner.jpg"},"relationships":{"creator":{"data":{"id":"author"}}}},{"id":"author","type":"user","attributes":{"full_name":"Author","about":"<p>Writes &amp; reads.</p>","url":"https://www.patreon.com/author","image_url":"https://c10.patreonusercontent.com/portrait.jpg"}},{"id":"book","type":"collection","attributes":{"title":"Book","description":"<p>A serial.</p>","cover_image_url":"https://c10.patreonusercontent.com/book.jpg"}}]}`)
	release, err := New().EnrichCaptured(domain.NormalizedRelease{ProviderReleaseID: "p1"}, raw)
	if err != nil {
		t.Fatal(err)
	}
	metadata := release.Enrichment
	if metadata.Author == nil || metadata.Author.ID != "patreon:user:author" || metadata.Author.Biography != "Writes & reads." {
		t.Fatalf("incorrect author identity: %+v", metadata.Author)
	}
	if len(metadata.Metadata) != 1 || metadata.Metadata[0].Cover.SourceURL == metadata.Author.Portrait.SourceURL || metadata.Metadata[0].Description != "A serial." {
		t.Fatalf("artwork roles collapsed: %+v", metadata)
	}
	if metadata.Author.Portrait.Path != "" {
		t.Fatal("offline enrichment claimed to fetch an image")
	}
}

func TestCapturedCampaignBiographyAndCollectionThumbnail(t *testing.T) {
	for _, about := range []string{"null", `"<p>User biography.</p>"`} {
		raw := []byte(fmt.Sprintf(`{"data":{"id":"post","relationships":{"user":{"data":{"id":"author"}},"campaign":{"data":{"id":"campaign"}},"collections":{"data":[{"id":"series"}]}}},"included":[{"id":"unrelated","type":"campaign","attributes":{"summary":"Wrong biography"}},{"id":"campaign","type":"campaign","attributes":{"summary":"<p>Writes fantasy &amp; science fiction.</p>","cover_photo_url":"https://example.com/campaign-banner.jpg"},"relationships":{"creator":{"data":{"id":"author"}}}},{"id":"author","type":"user","attributes":{"full_name":"Author","about":%s,"url":"https://www.patreon.com/author","image_url":"https://c10.patreonusercontent.com/portrait.png"}},{"id":"series","type":"collection","attributes":{"title":"Series","description":"A journey.","thumbnail":{"default":"https://c10.patreonusercontent.com/series.png","default_blurred":"https://c10.patreonusercontent.com/blurred.png"}}}]}`, about))
		release, err := New().EnrichCaptured(domain.NormalizedRelease{ProviderReleaseID: "post"}, raw)
		if err != nil {
			t.Fatal(err)
		}
		wantBiography := "Writes fantasy & science fiction."
		if about != "null" {
			wantBiography = "User biography."
		}
		metadata := release.Enrichment
		if metadata.Author == nil || metadata.Author.Biography != wantBiography || metadata.Author.ID != "patreon:user:author" {
			t.Fatalf("linked biography: %+v", metadata.Author)
		}
		if len(metadata.Metadata) != 1 || metadata.Metadata[0].Cover == nil || metadata.Metadata[0].Cover.SourceURL != "https://c10.patreonusercontent.com/series.png" {
			t.Fatalf("collection artwork: %+v", metadata.Metadata)
		}
		if metadata.Author.Portrait.SourceURL != "https://c10.patreonusercontent.com/portrait.png" || metadata.Metadata[0].Cover.Path != "" {
			t.Fatal("artwork roles or offline capture contract changed")
		}
	}
}

func TestCapturedCollectionThumbnailRejectsResizedCreatorArtwork(t *testing.T) {
	for _, mediaID := range []string{"portrait", "avatar", "banner", "series-cover"} {
		imageURL := "https://c10.patreonusercontent.com/4/patreon-media/p/campaign/123/" + mediaID + "/eyJ3Ijo2MjB9/1.png?token-time=new"
		raw := []byte(fmt.Sprintf(`{"data":{"id":"post","relationships":{"user":{"data":{"id":"author"}},"campaign":{"data":{"id":"campaign"}},"collections":{"data":[{"id":"series"}]}}},"included":[{"id":"campaign","type":"campaign","attributes":{"avatar_photo_url":"https://c1.patreonusercontent.com/4/patreon-media/p/campaign/123/avatar/eyJ3IjoyMDB9/1.png?token-time=old","cover_photo_url":"https://c1.patreonusercontent.com/4/patreon-media/p/campaign/123/banner/eyJ3IjoyMDB9/1.png?token-time=old"}},{"id":"author","type":"user","attributes":{"full_name":"Author","image_url":"https://c1.patreonusercontent.com/4/patreon-media/p/campaign/123/portrait/eyJ3IjoyMDB9/1.png?token-time=old"}},{"id":"series","type":"collection","attributes":{"title":"Series","thumbnail":{"default":%q}}}]}`, imageURL))
		release, err := New().EnrichCaptured(domain.NormalizedRelease{ProviderReleaseID: "post"}, raw)
		if err != nil {
			t.Fatal(err)
		}
		cover := release.Enrichment.Metadata[0].Cover
		if mediaID == "series-cover" {
			if cover == nil || cover.SourceURL != imageURL {
				t.Fatalf("distinct series artwork was rejected: %+v", cover)
			}
		} else if cover != nil {
			t.Fatalf("creator %s was selected as series cover: %+v", mediaID, cover)
		}
	}
}

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
			_, err := client.DiscoverSources(context.Background(), config.AuthProfile{ID: "fixture", Provider: "patreon", Mode: "username_password", SessionPath: path}, nil, provider.DiscoverOptions{MembershipFilter: "all"})
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
