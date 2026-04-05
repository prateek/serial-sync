package provider

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

type ReleaseDocument struct {
	Normalized domain.NormalizedRelease
	RawJSON    json.RawMessage
}

type Client interface {
	Name() string
	ValidateSource(source config.SourceConfig) error
	ListReleases(ctx context.Context, auth config.AuthProfile, source config.SourceConfig) ([]ReleaseDocument, domain.AuthState, error)
}

type Registry struct {
	clients map[string]Client
}

func NewRegistry(clients ...Client) *Registry {
	registry := &Registry{clients: map[string]Client{}}
	for _, client := range clients {
		registry.clients[client.Name()] = client
	}
	return registry
}

func (r *Registry) Get(name string) (Client, bool) {
	client, ok := r.clients[name]
	return client, ok
}

func SortReleaseDocuments(items []ReleaseDocument) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Normalized.PublishedAt.After(items[j].Normalized.PublishedAt)
	})
}
