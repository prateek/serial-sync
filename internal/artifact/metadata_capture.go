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
