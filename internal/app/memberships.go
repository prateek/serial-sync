package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

type Membership struct {
	Creator    string `json:"creator"`
	URL        string `json:"url"`
	Kind       string `json:"kind"`
	Source     string `json:"source,omitempty"`
	Configured bool   `json:"configured"`
	Enabled    bool   `json:"enabled"`
	Referenced bool   `json:"referenced"`
}

func (s *Service) Memberships(ctx context.Context, profile string) ([]Membership, error) {
	auth, err := s.selectAuthProfile(profile)
	if err != nil {
		return nil, err
	}
	client, ok := s.Providers.Get(auth.Provider)
	if !ok {
		return nil, fmt.Errorf("no provider registered for %q", auth.Provider)
	}
	result, err := client.DiscoverSources(ctx, auth, s.Config.Sources, provider.DiscoverOptions{MetadataOnly: true, IncludeConfigured: true, MembershipFilter: "all"})
	if err != nil {
		return nil, err
	}
	memberships := make([]Membership, 0, len(result.Suggestions))
	for _, item := range result.Suggestions {
		row := Membership{Creator: item.CreatorName, URL: item.Source.URL, Kind: item.MembershipKind, Configured: item.AlreadyConfigured, Source: item.ExistingSourceID}
		if source, ok := s.Config.SourceByID(row.Source); ok {
			row.Enabled = source.Enabled
			row.Referenced = len(s.Config.RulesForSource(source.ID)) > 0
		}
		memberships = append(memberships, row)
	}
	return memberships, nil
}

func FormatMemberships(rows []Membership) string {
	lines := []string{"Creator | membership | source | enabled | mapped | URL"}
	for _, row := range rows {
		source := row.Source
		if !row.Configured {
			source = "unconfigured"
		}
		lines = append(lines, fmt.Sprintf("%s | %s | %s | %t | %t | %s", row.Creator, row.Kind, source, row.Enabled, row.Referenced, row.URL))
	}
	return strings.Join(lines, "\n")
}

func (s *Service) bootstrapProfile(ctx context.Context, profile string, force bool, runID string) (AuthBootstrapResult, error) {
	result := AuthBootstrapResult{RunID: runID}
	auth, err := s.selectAuthProfile(profile)
	if err != nil {
		return result, err
	}
	client, ok := s.Providers.Get(auth.Provider)
	if !ok {
		return result, fmt.Errorf("no provider registered for %q", auth.Provider)
	}
	bootstrap, ok := client.(provider.ProfileAuthenticator)
	if !ok {
		return result, fmt.Errorf("provider %s requires a configured source for authentication", auth.Provider)
	}
	outcome, err := bootstrap.BootstrapProfile(ctx, auth, force)
	item := AuthBootstrapItem{Provider: auth.Provider, AuthProfileID: auth.ID, AuthState: outcome.State, Action: outcome.Action}
	if err != nil {
		result.Failed = 1
		item.Message = err.Error()
	} else if outcome.Action == "bootstrapped" {
		result.Bootstrapped = 1
	} else if outcome.State == domain.AuthStateAuthenticated {
		result.Verified = 1
	}
	result.Items = []AuthBootstrapItem{item}
	return result, err
}
