package app

import (
	"context"
	"fmt"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/provider"
	"github.com/prateek/serial-sync/internal/store"
)

// Authoring evaluates captured corpora without initializing or changing state.
type Authoring struct {
	config *config.Config
	*catalogReader
}

// NewAuthoring uses a validated config and optional captured catalog and enricher.
// With a workspace and catalog, dump evidence determines replay decisions and
// source scope; applied-library actions use the catalog's captured releases.
// Without a catalog, discovery history is empty and applied state is unavailable.
func NewAuthoring(cfg *config.Config, catalog store.CatalogReader, enricher provider.CapturedEnricher) *Authoring {
	return &Authoring{config: cfg, catalogReader: &catalogReader{catalog: catalog, enricher: enricher}}
}

// Preview evaluates captured evidence without initializing state or delivering files.
func (s *Authoring) Preview(ctx context.Context, options RulesPreviewOptions) (RulesPreviewResult, error) {
	if s.config == nil {
		return RulesPreviewResult{}, fmt.Errorf("authoring requires a validated config")
	}
	return s.replay(ctx, options)
}

type authoringScope struct {
	replaceSeries   bool
	sources         []config.SourceConfig
	series          []config.SeriesConfig
	includedSources []string
	restrictSources bool
}

func scopedAuthoringConfig(cfg *config.Config, scope authoringScope) *config.Config {
	scoped := *cfg
	if scope.replaceSeries {
		scoped.Sources, scoped.Series = scope.sources, scope.series
		scoped.Rules, scoped.Review, scoped.Overrides = nil, nil, nil
	}
	if scope.restrictSources {
		scoped.Sources = append([]config.SourceConfig(nil), scoped.Sources...)
		included := map[string]bool{}
		for _, id := range scope.includedSources {
			included[id] = true
		}
		for i := range scoped.Sources {
			scoped.Sources[i].Enabled = scoped.Sources[i].Enabled && included[scoped.Sources[i].ID]
		}
	}
	return &scoped
}
