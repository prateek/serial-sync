package patreon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

type artworkTransport func(*http.Request) (*http.Response, error)

func (f artworkTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestArtworkCacheRefreshRetainsEarlierCaptureAndUsesBudget(t *testing.T) {
	var encoded bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	payload := encoded.Bytes()
	session := &liveSession{metadata: &metadataAssetCache{root: t.TempDir()}, budget: newRequestBudget()}
	calls := 0
	session.client = &http.Client{Transport: artworkTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if session.budget.inFlight != 1 {
			t.Fatal("artwork bypassed the shared request budget")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(bytes.NewReader(payload)), Request: req}, nil
	})}
	url := "https://c10.patreonusercontent.com/portrait.png"
	capture := func() *domain.MetadataAsset {
		m := &domain.ReleaseEnrichment{Author: &domain.AuthorProfile{Portrait: &domain.MetadataAsset{SourceURL: url}}}
		New().captureMetadataAssets(context.Background(), session, config.SourceConfig{ID: "source"}, m)
		return m.Author.Portrait
	}
	first, cached := capture(), capture()
	if first.Path == "" || first.SHA256 != cached.SHA256 || calls != 1 {
		t.Fatal("artwork cache did not reuse captured bytes")
	}
	original, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	encoded.Reset()
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	payload = encoded.Bytes()
	cache := filepath.Join(session.metadata.root, fmt.Sprintf("%x", sha256.Sum256([]byte(url))))
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(cache, old, old); err != nil {
		t.Fatal(err)
	}
	updated := capture()
	retained, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || first.SHA256 == updated.SHA256 || !bytes.Equal(original, retained) {
		t.Fatal("artwork refresh changed an earlier capture")
	}
}

func TestUnavailableArtworkIsOptionalAndFailureIsCached(t *testing.T) {
	calls := 0
	session := &liveSession{metadata: &metadataAssetCache{root: t.TempDir()}, budget: newRequestBudget(), client: &http.Client{Transport: artworkTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil)), Request: req}, nil
	})}}
	for range 2 {
		m := &domain.ReleaseEnrichment{Author: &domain.AuthorProfile{Portrait: &domain.MetadataAsset{SourceURL: "https://c10.patreonusercontent.com/missing.png"}}}
		New().captureMetadataAssets(context.Background(), session, config.SourceConfig{}, m)
		if m.Author.Portrait.Path != "" {
			t.Fatal("failed image became a captured asset")
		}
	}
	if calls != 1 {
		t.Fatal("failed artwork was retried before cooldown")
	}
}
