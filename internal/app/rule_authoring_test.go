package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

func TestDumpSourcesWritesWorkspace(t *testing.T) {
	t.Parallel()

	service := newRuleAuthoringService(t, newStubRuleAuthoringProvider())
	result, err := service.DumpSources(context.Background(), "patreon-default", app.SourceDumpOptions{
		Path:             filepath.Join(t.TempDir(), "workspace"),
		MembershipFilter: "paid",
		CreatorFilters:   []string{"alpha"},
	}, "source dump")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(result.Creators), 1; got != want {
		t.Fatalf("len(result.Creators) = %d, want %d", got, want)
	}
	if got, want := result.TotalPosts, 3; got != want {
		t.Fatalf("result.TotalPosts = %d, want %d", got, want)
	}
	client, ok := service.Providers.Get("patreon")
	if !ok {
		t.Fatal("expected patreon provider to be registered")
	}
	stub := client.(*stubRuleAuthoringProvider)
	if !stub.lastDiscoverOptions.MetadataOnly {
		t.Fatal("expected dump discovery to use metadata-only mode")
	}
	for _, path := range []string{
		result.ManifestFile,
		result.SourcesFile,
		result.SeriesFile,
		result.Creators[0].SourceFile,
		result.Creators[0].PostsFile,
		result.Creators[0].RawPostsDir,
		result.Creators[0].AttachmentsDir,
		filepath.Join(result.Creators[0].RawPostsDir, "a1.json"),
		filepath.Join(result.Creators[0].AttachmentsDir, "a1", "alpha-saga-1.epub"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
	postLines := strings.Split(strings.TrimSpace(string(mustReadFile(t, result.Creators[0].PostsFile))), "\n")
	if len(postLines) != 3 {
		t.Fatalf("expected 3 posts in %s, got %d", result.Creators[0].PostsFile, len(postLines))
	}
	var record struct {
		Normalized domain.NormalizedRelease `json:"normalized"`
	}
	foundHydratedAttachment := false
	for _, line := range postLines {
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unmarshal dumped post: %v", err)
		}
		if len(record.Normalized.Attachments) == 0 {
			continue
		}
		if record.Normalized.Attachments[0].LocalPath != "" {
			foundHydratedAttachment = true
			break
		}
	}
	if !foundHydratedAttachment {
		t.Fatal("expected dumped normalized posts to keep hydrated attachment local_path")
	}
}

func TestPreviewRulesUsesDumpedWorkspace(t *testing.T) {
	t.Parallel()

	service := newRuleAuthoringService(t, newStubRuleAuthoringProvider())
	workspace := filepath.Join(t.TempDir(), "workspace")
	dump, err := service.DumpSources(context.Background(), "patreon-default", app.SourceDumpOptions{
		Path:             workspace,
		MembershipFilter: "paid",
		CreatorFilters:   []string{"alpha"},
	}, "source dump")
	if err != nil {
		t.Fatal(err)
	}
	rulesBody := `[[series]]
id = "alpha-saga"
title = "Alpha Saga"
authors = ["Alpha Author"]

  [series.output]
  format = "epub"
  preface_mode = "prepend_post"

  [[series.inputs]]
  source = "alpha"
  priority = 10
  match_type = "title_regex"
  match_value = "^Alpha Saga"
  release_role = "chapter"
  content_strategy = "text_post"

[[series]]
id = "unmatched-review"
title = "Unmatched Review"
authors = ["Alpha Author"]

  [series.output]
  format = "preserve"
  preface_mode = "none"

  [[series.inputs]]
  source = "alpha"
  priority = 1000
  match_type = "fallback"
  match_value = ""
  release_role = "unknown"
  content_strategy = "manual"
`
	if err := os.WriteFile(dump.SeriesFile, []byte(rulesBody), 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewRules(context.Background(), app.RulesPreviewOptions{
		WorkspacePath: workspace,
		SeriesFile:    dump.SeriesFile,
		ShowPosts:     true,
	}, "rules preview")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(preview.Creators), 1; got != want {
		t.Fatalf("len(preview.Creators) = %d, want %d", got, want)
	}
	if got, want := preview.Materializable, 2; got != want {
		t.Fatalf("preview.Materializable = %d, want %d", got, want)
	}
	if got, want := preview.FallbackPosts, 1; got != want {
		t.Fatalf("preview.FallbackPosts = %d, want %d", got, want)
	}
	if got, want := preview.Creators[0].Preview.Groups[0].TrackKey, "alpha-saga"; got != want {
		t.Fatalf("preview group track = %q, want %q", got, want)
	}
	if len(preview.Creators[0].Preview.Posts) != 3 {
		t.Fatalf("expected 3 preview posts, got %d", len(preview.Creators[0].Preview.Posts))
	}
	text := app.FormatRulesPreviewResult(preview, true)
	if !strings.Contains(text, "alpha-saga") || !strings.Contains(text, "unmatched-review") {
		t.Fatalf("formatted preview missing expected groups: %s", text)
	}
}

type stubRuleAuthoringProvider struct {
	suggestions         []provider.SourceSuggestion
	docs                map[string][]provider.ReleaseDocument
	lastDiscoverOptions provider.DiscoverOptions
}

func newStubRuleAuthoringProvider() *stubRuleAuthoringProvider {
	return &stubRuleAuthoringProvider{
		suggestions: []provider.SourceSuggestion{
			{
				Source: config.SourceConfig{
					ID:          "alpha",
					Provider:    "patreon",
					URL:         "https://www.patreon.com/c/alpha/posts",
					AuthProfile: "patreon-default",
					Enabled:     true,
				},
				CreatorName:    "Alpha Author",
				CreatorHandle:  "alpha",
				MembershipKind: "paid",
			},
			{
				Source: config.SourceConfig{
					ID:          "beta",
					Provider:    "patreon",
					URL:         "https://www.patreon.com/c/beta/posts",
					AuthProfile: "patreon-default",
					Enabled:     true,
				},
				CreatorName:    "Beta Author",
				CreatorHandle:  "beta",
				MembershipKind: "free",
			},
		},
		docs: map[string][]provider.ReleaseDocument{
			"alpha": {
				{
					Normalized: domain.NormalizedRelease{
						Provider:          "patreon",
						ProviderReleaseID: "a1",
						Title:             "Alpha Saga - Chapter 1",
						PublishedAt:       time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
						TextHTML:          "<p>One</p>",
						Attachments: []domain.Attachment{{
							FileName:    "alpha-saga-1.epub",
							MIMEType:    "application/epub+zip",
							DownloadURL: "https://example.invalid/a1.epub",
						}},
					},
					RawJSON: []byte(`{"id":"a1","title":"Alpha Saga - Chapter 1"}`),
				},
				{
					Normalized: domain.NormalizedRelease{
						Provider:          "patreon",
						ProviderReleaseID: "a2",
						Title:             "Alpha Saga - Chapter 2",
						PublishedAt:       time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC),
						TextHTML:          "<p>Two</p>",
					},
					RawJSON: []byte(`{"id":"a2","title":"Alpha Saga - Chapter 2"}`),
				},
				{
					Normalized: domain.NormalizedRelease{
						Provider:          "patreon",
						ProviderReleaseID: "a3",
						Title:             "Alpha note",
						PublishedAt:       time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC),
						TextHTML:          "",
					},
					RawJSON: []byte(`{"id":"a3","title":"Alpha note"}`),
				},
			},
			"beta": {
				{
					Normalized: domain.NormalizedRelease{
						Provider:          "patreon",
						ProviderReleaseID: "b1",
						Title:             "Beta Story 1",
						PublishedAt:       time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
						TextHTML:          "<p>Beta</p>",
					},
					RawJSON: []byte(`{"id":"b1","title":"Beta Story 1"}`),
				},
			},
		},
	}
}

func (s *stubRuleAuthoringProvider) Name() string { return "patreon" }

func (s *stubRuleAuthoringProvider) ValidateSource(config.SourceConfig) error { return nil }

func (s *stubRuleAuthoringProvider) ValidateSession(context.Context, config.AuthProfile, config.SourceConfig) (domain.AuthState, error) {
	return domain.AuthStateAuthenticated, nil
}

func (s *stubRuleAuthoringProvider) BootstrapAuth(context.Context, config.AuthProfile, config.SourceConfig, bool) (provider.AuthBootstrapResult, error) {
	return provider.AuthBootstrapResult{State: domain.AuthStateAuthenticated, Action: "verified"}, nil
}

func (s *stubRuleAuthoringProvider) DiscoverSources(_ context.Context, _ config.AuthProfile, _ []config.SourceConfig, options provider.DiscoverOptions) (provider.DiscoverResult, error) {
	s.lastDiscoverOptions = options
	suggestions := make([]provider.SourceSuggestion, 0, len(s.suggestions))
	for _, suggestion := range s.suggestions {
		if options.MembershipFilter != "" && options.MembershipFilter != "all" && suggestion.MembershipKind != options.MembershipFilter {
			continue
		}
		if len(options.CreatorFilters) > 0 {
			matched := false
			for _, filter := range options.CreatorFilters {
				filter = strings.ToLower(strings.TrimSpace(filter))
				if filter == "" {
					continue
				}
				for _, candidate := range []string{suggestion.Source.ID, suggestion.CreatorHandle, suggestion.CreatorName} {
					if strings.Contains(strings.ToLower(candidate), filter) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}
		}
		suggestions = append(suggestions, suggestion)
	}
	return provider.DiscoverResult{
		Provider:    "patreon",
		AuthState:   domain.AuthStateAuthenticated,
		Suggestions: suggestions,
	}, nil
}

func (s *stubRuleAuthoringProvider) ListReleases(_ context.Context, _ config.AuthProfile, source config.SourceConfig, _ *domain.Source) (provider.ListResult, error) {
	return provider.ListResult{
		Documents: s.docs[source.ID],
		AuthState: domain.AuthStateAuthenticated,
	}, nil
}

func (s *stubRuleAuthoringProvider) PrepareRelease(_ context.Context, _ config.AuthProfile, _ config.SourceConfig, doc provider.ReleaseDocument, _ domain.TrackDecision) (provider.ReleaseDocument, domain.AuthState, error) {
	return doc, domain.AuthStateAuthenticated, nil
}

func (s *stubRuleAuthoringProvider) HydrateDumpReleases(_ context.Context, _ config.AuthProfile, _ config.SourceConfig, docs []provider.ReleaseDocument, fixtureDir string) ([]provider.ReleaseDocument, domain.AuthState, error) {
	for docIndex := range docs {
		for attachmentIndex := range docs[docIndex].Normalized.Attachments {
			attachment := &docs[docIndex].Normalized.Attachments[attachmentIndex]
			targetPath := filepath.Join(fixtureDir, "attachments", docs[docIndex].Normalized.ProviderReleaseID, attachment.FileName)
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return nil, domain.AuthStateReauthRequired, err
			}
			if err := os.WriteFile(targetPath, []byte("epub bytes"), 0o644); err != nil {
				return nil, domain.AuthStateReauthRequired, err
			}
			attachment.LocalPath = targetPath
		}
	}
	return docs, domain.AuthStateAuthenticated, nil
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func newRuleAuthoringService(t *testing.T, stub *stubRuleAuthoringProvider) *app.Service {
	t.Helper()

	tmp := t.TempDir()
	cfg := &config.Config{
		Runtime: config.RuntimeConfig{
			LogRoot:      filepath.Join(tmp, "logs"),
			StoreDriver:  "sqlite",
			StoreDSN:     filepath.Join(tmp, "state.db"),
			ArtifactRoot: filepath.Join(tmp, "artifacts"),
			SupportRoot:  filepath.Join(tmp, "support"),
		},
		AuthProfiles: []config.AuthProfile{{
			ID:       "patreon-default",
			Provider: "patreon",
			Mode:     "username_password",
		}},
	}
	roots := config.Roots{
		ConfigDir:  filepath.Join(tmp, "config"),
		StateDir:   tmp,
		CacheDir:   filepath.Join(tmp, "cache"),
		RuntimeDir: filepath.Join(tmp, "runtime"),
	}
	if err := config.EnsureDirs(roots, cfg); err != nil {
		t.Fatal(err)
	}
	repo, err := sqlite.Open(cfg.Runtime.StoreDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return app.New(cfg, roots, filepath.Join(tmp, "config.toml"), repo, provider.NewRegistry(stub))
}
