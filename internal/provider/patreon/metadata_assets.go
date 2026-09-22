package patreon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

type metadataAssetCache struct {
	root string
	mu   sync.Mutex
}

func metadataImageIdentity(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Hostname() != "patreonusercontent.com" && !strings.HasSuffix(u.Hostname(), ".patreonusercontent.com")) {
		return rawURL
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 8 && parts[1] == "patreon-media" && parts[2] == "p" {
		return strings.Join(parts[:6], "/")
	}
	return u.Hostname() + u.EscapedPath()
}

func (c *Client) captureMetadataAssets(ctx context.Context, session *liveSession, source config.SourceConfig, metadata *domain.ReleaseEnrichment) {
	if metadata == nil || session.metadata == nil || session.metadata.root == "" {
		return
	}
	session.metadata.mu.Lock()
	defer session.metadata.mu.Unlock()
	var assets []*domain.MetadataAsset
	if metadata.Author != nil {
		assets = append(assets, metadata.Author.Portrait)
	}
	for _, collection := range metadata.Metadata {
		assets = append(assets, collection.Cover)
	}
	for _, asset := range assets {
		if asset == nil || asset.Path != "" || asset.SourceURL == "" {
			continue
		}
		u, err := url.Parse(asset.SourceURL)
		if err != nil || u.Scheme != "https" || u.User != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		if host != "patreonusercontent.com" && !strings.HasSuffix(host, ".patreonusercontent.com") && host != "patreon.com" && !strings.HasSuffix(host, ".patreon.com") {
			continue
		}
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(asset.SourceURL)))
		cache := filepath.Join(session.metadata.root, key)
		if info, err := os.Stat(cache + ".failed"); err == nil && time.Since(info.ModTime()) < time.Hour {
			continue
		}
		data, err := os.ReadFile(cache)
		info, statErr := os.Stat(cache)
		if err != nil || statErr != nil || time.Since(info.ModTime()) >= 24*time.Hour {
			if ctx.Err() != nil {
				return
			}
			data, _, err = c.get(ctx, session, asset.SourceURL, source.URL, "image/png,image/jpeg,image/gif")
			if err == nil && len(data) > 16<<20 {
				err = fmt.Errorf("metadata image exceeds 16 MiB")
			}
			if err == nil {
				err = os.MkdirAll(session.metadata.root, 0700)
			}
			if err == nil {
				err = os.WriteFile(cache, data, 0600)
			}
			if err != nil {
				_ = os.MkdirAll(session.metadata.root, 0700)
				_ = os.WriteFile(cache+".failed", nil, 0600)
				reportSourceProgress(ctx, source.ID, "warning", "Optional Patreon artwork unavailable; publishing without it", map[string]any{"asset_url": asset.SourceURL})
				continue
			}
		}
		mediaType := http.DetectContentType(data)
		if mediaType != "image/png" && mediaType != "image/jpeg" && mediaType != "image/gif" {
			continue
		}
		hash := sha256.Sum256(data)
		digest := hex.EncodeToString(hash[:])
		capture := filepath.Join(session.metadata.root, "captures", digest)
		if err := os.MkdirAll(filepath.Dir(capture), 0700); err != nil {
			continue
		}
		if err := os.WriteFile(capture, data, 0600); err != nil {
			continue
		}
		asset.Path, asset.MediaType, asset.SHA256 = capture, mediaType, digest
	}
}
