package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/provider/patreon"
)

// The Docker onboarding check supplies a mounted private parent and /fixtures.
// Normal go test runs retain everything in temporary directories.
func TestOnboardingFixtureDump(t *testing.T) {
	privateRoot := os.Getenv("SERIAL_SYNC_ONBOARDING_PRIVATE_ROOT")
	if privateRoot == "" {
		privateRoot = t.TempDir()
	}
	fixtureDir := os.Getenv("SERIAL_SYNC_ONBOARDING_FIXTURE_DIR")
	if fixtureDir == "" {
		var err error
		fixtureDir, err = filepath.Abs("../../testdata/fixtures/patreon/onboarding")
		if err != nil {
			t.Fatal(err)
		}
	}
	service := newRuleAuthoringService(t, newStubRuleAuthoringProvider())
	source := config.SourceConfig{
		ID: "example-author", Provider: "patreon",
		URL:         "https://www.patreon.com/c/SerialSyncFixtureOnly/posts",
		AuthProfile: "patreon-default", FixtureDir: fixtureDir, Enabled: true,
	}
	service.Providers = provider.NewRegistry(&onboardingFixtureProvider{Client: patreon.New(), source: source})
	workspace := filepath.Join(privateRoot, "workspace")
	result, err := service.DumpSources(context.Background(), "patreon-default", app.SourceDumpOptions{
		Path: workspace, CreatorFilters: []string{"SerialSyncFixtureOnly"},
	}, "setup dump fixture onboarding")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Creators) != 1 || result.TotalPosts != 1 || result.Creators[0].SourceID != source.ID {
		t.Fatalf("unexpected fixture dump: %+v", result)
	}
	for _, path := range []string{result.ManifestFile, result.SourcesFile, result.SeriesFile, result.Creators[0].PostsFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}

type onboardingFixtureProvider struct {
	*patreon.Client
	source config.SourceConfig
}

// Only membership discovery is replaced; parsing and capture use the real fixture reader.
func (p *onboardingFixtureProvider) DiscoverSources(context.Context, config.AuthProfile, []config.SourceConfig, provider.DiscoverOptions) (provider.DiscoverResult, error) {
	return provider.DiscoverResult{
		Provider: "patreon", AuthState: domain.AuthStateAuthenticated,
		Suggestions: []provider.SourceSuggestion{{
			Source: p.source, CreatorName: "Example Author", CreatorHandle: "SerialSyncFixtureOnly", MembershipKind: "paid",
		}},
	}, nil
}
