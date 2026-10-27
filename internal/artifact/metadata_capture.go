package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prateek/serial-sync/internal/domain"
)

func (m *Materializer) snapshotPublication(metadata *domain.PublicationMetadata) (*domain.PublicationMetadata, map[string][]byte, error) {
	if metadata == nil {
		return nil, nil, nil
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil, nil, err
	}
	var snapshot domain.PublicationMetadata
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, nil, err
	}
	assets := []*domain.MetadataAsset{snapshot.Cover}
	for _, author := range snapshot.Authors {
		assets = append(assets, author.Portrait)
	}
	captured := map[string][]byte{}
	for _, asset := range assets {
		if asset == nil || asset.Path == "" {
			continue
		}
		data, err := os.ReadFile(asset.Path)
		if err != nil {
			return nil, nil, err
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != asset.SHA256 {
			return nil, nil, fmt.Errorf("metadata asset changed after selection: %s", asset.Path)
		}
		asset.Path = filepath.Join(m.Root, "metadata-assets", asset.SHA256)
		captured[asset.Path] = data
	}
	return &snapshot, captured, nil
}

func captureCatalogMetadata(source domain.Source, track domain.StoryTrack, release domain.Release) (map[string]any, error) {
	result := map[string]any{"source": source, "track": track, "release": release}
	// Catalog bookkeeping varies across retries; upstream dates and capture inputs do not.
	for name, fields := range map[string][]string{
		"source":  {"last_synced_at", "sync_cursor"},
		"track":   {"created_at", "updated_at"},
		"release": {"discovered_at", "normalized_payload_ref", "raw_payload_ref", "status"},
	} {
		data, err := json.Marshal(result[name])
		if err != nil {
			return nil, err
		}
		var captured map[string]json.RawMessage
		if err := json.Unmarshal(data, &captured); err != nil {
			return nil, err
		}
		for _, field := range fields {
			delete(captured, field)
		}
		result[name] = captured
	}
	return result, nil
}
