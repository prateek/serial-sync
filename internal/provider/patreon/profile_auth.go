package patreon

import (
	"context"
	"os"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func (c *Client) BootstrapProfile(ctx context.Context, auth config.AuthProfile, force bool) (provider.AuthBootstrapResult, error) {
	if normalizeAuthMode(auth.Mode) == "fixture" {
		return provider.AuthBootstrapResult{State: domain.AuthStateAuthenticated, Action: "fixture"}, nil
	}
	if force {
		if err := os.Remove(auth.SessionPath); err != nil && !os.IsNotExist(err) {
			return provider.AuthBootstrapResult{}, err
		}
	}
	copy := *c
	bootstrapped := false
	if c.bootstrap != nil {
		copy.bootstrap = func(ctx context.Context, auth config.AuthProfile, source config.SourceConfig, path string) (domain.AuthState, error) {
			bootstrapped = true
			return c.bootstrap(ctx, auth, source, path)
		}
	}
	_, _, state, err := copy.ensureDiscoverySession(ctx, auth, true)
	action := "verified"
	if bootstrapped {
		action = "bootstrapped"
	}
	if err != nil {
		action = "failed"
	}
	return provider.AuthBootstrapResult{State: state, Action: action}, err
}
