package patreon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

type attachmentProvenance struct {
	SourceID    string    `json:"source_id"`
	ReleaseID   string    `json:"release_id"`
	PublishedAt time.Time `json:"published_at"`
	EditedAt    time.Time `json:"edited_at"`
	MediaID     string    `json:"media_id,omitempty"`
	Resource    string    `json:"resource,omitempty"`
	FileName    string    `json:"file_name"`
	MIMEType    string    `json:"mime_type"`
}

type attachmentCapture struct {
	Version    int                  `json:"version"`
	Provenance attachmentProvenance `json:"provenance"`
	SHA256     string               `json:"sha256"`
	Size       int64                `json:"size"`
}

func attachmentIdentity(source config.SourceConfig, release *domain.NormalizedRelease, attachment domain.Attachment) attachmentProvenance {
	identity := attachmentProvenance{
		SourceID: source.ID, ReleaseID: release.ProviderReleaseID,
		PublishedAt: release.PublishedAt.UTC(), EditedAt: release.EditedAt.UTC(),
		MediaID: attachment.ProviderMediaID, FileName: attachment.FileName, MIMEType: attachment.MIMEType,
	}
	// Signed credentials rotate independently of the post revision and media identity.
	if identity.MediaID == "" {
		resource, err := url.Parse(attachment.DownloadURL)
		if err == nil {
			resource.RawQuery, resource.Fragment, resource.User = "", "", nil
			identity.Resource = resource.String()
		}
	}
	return identity
}

func attachmentCacheRoot(auth config.AuthProfile) string {
	return filepath.Join(sessionCacheRoot(auth.SessionPath), "attachment-captures-v1")
}

func isManagedAttachmentPath(auth config.AuthProfile, path string) bool {
	relative, err := filepath.Rel(attachmentCacheRoot(auth), path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (c *Client) downloadLiveAttachment(ctx context.Context, session *liveSession, auth config.AuthProfile, source config.SourceConfig, release *domain.NormalizedRelease, index int) (domain.AuthState, error) {
	if index < 0 || index >= len(release.Attachments) {
		return domain.AuthStateAuthenticated, nil
	}
	attachment := &release.Attachments[index]
	if strings.TrimSpace(attachment.DownloadURL) == "" || strings.TrimSpace(attachment.FileName) == "" {
		return domain.AuthStateAuthenticated, nil
	}
	attachment.LocalPath = ""
	identity := attachmentIdentity(source, release, *attachment)
	identityJSON, err := json.Marshal(identity)
	if err != nil {
		return domain.AuthStateAuthenticated, err
	}
	key := sha256.Sum256(identityJSON)
	directory := filepath.Join(attachmentCacheRoot(auth), hex.EncodeToString(key[:]))
	manifestPath := filepath.Join(directory, "capture.json")
	if err := ctx.Err(); err != nil {
		return domain.AuthStateAuthenticated, err
	}
	if raw, err := os.ReadFile(manifestPath); err == nil {
		var capture attachmentCapture
		if json.Unmarshal(raw, &capture) == nil && capture.matches(identityJSON) {
			path := capture.payloadPath(directory)
			if err := verifyAttachmentCapture(ctx, path, capture.Size, capture.SHA256); err == nil && (attachment.SHA256 == "" || attachment.SHA256 == capture.SHA256) {
				attachment.LocalPath, attachment.SHA256 = path, capture.SHA256
				reportPatreonProgress(ctx, "info", "Patreon attachment cache hit", "release", release.ProviderReleaseID, map[string]any{
					"source_id": source.ID, "provider_release_id": release.ProviderReleaseID,
					"file_name": attachment.FileName, "cached_path": path,
				})
				return domain.AuthStateAuthenticated, nil
			}
		}
	}
	startedAt := time.Now()
	body, authState, err := c.get(ctx, session, attachment.DownloadURL, source.URL, "*/*")
	if err != nil {
		return authState, fmt.Errorf("download Patreon attachment %q: %w", attachment.FileName, err)
	}
	digest := sha256.Sum256(body)
	capture := attachmentCapture{Version: 1, Provenance: identity, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(body))}
	if capture.Size == 0 {
		return domain.AuthStateAuthenticated, fmt.Errorf("empty Patreon attachment %q", attachment.FileName)
	}
	if attachment.SHA256 != "" && attachment.SHA256 != capture.SHA256 {
		return domain.AuthStateAuthenticated, fmt.Errorf("Patreon attachment %q checksum mismatch", attachment.FileName)
	}
	path := capture.payloadPath(directory)
	if err := writeAttachmentFile(ctx, path, body); err != nil {
		return domain.AuthStateAuthenticated, err
	}
	raw, err := json.Marshal(capture)
	if err != nil {
		return domain.AuthStateAuthenticated, err
	}
	// The manifest is the commit point; an interrupted payload write is never reusable.
	if err := writeAttachmentFile(ctx, manifestPath, raw); err != nil {
		return domain.AuthStateAuthenticated, err
	}
	attachment.LocalPath, attachment.SHA256 = path, capture.SHA256
	reportPatreonProgress(ctx, "info", "downloaded Patreon attachment", "release", release.ProviderReleaseID, map[string]any{
		"source_id": source.ID, "provider_release_id": release.ProviderReleaseID,
		"file_name": attachment.FileName, "bytes": len(body), "duration_ms": elapsedMillis(startedAt),
	})
	return domain.AuthStateAuthenticated, nil
}

func (capture attachmentCapture) matches(identityJSON []byte) bool {
	if capture.Version != 1 || !validCaptureDigest(capture.SHA256) {
		return false
	}
	provenanceJSON, err := json.Marshal(capture.Provenance)
	return err == nil && bytes.Equal(provenanceJSON, identityJSON)
}

func validCaptureDigest(digest string) bool {
	raw, err := hex.DecodeString(digest)
	return err == nil && len(raw) == sha256.Size
}

func (capture attachmentCapture) payloadPath(directory string) string {
	return filepath.Join(directory, capture.SHA256, sanitizeAttachmentFileName(capture.Provenance.FileName))
}

func verifyAttachmentCapture(ctx context.Context, path string, size int64, digest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, file)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if size <= 0 || n != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return fmt.Errorf("invalid Patreon attachment capture")
	}
	return nil
}

func exportAttachmentCapture(ctx context.Context, attachment *domain.Attachment, path string) error {
	body, err := os.ReadFile(attachment.LocalPath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != attachment.SHA256 {
		return fmt.Errorf("Patreon attachment changed during dump export")
	}
	if err := writeAttachmentFile(ctx, path, body); err != nil {
		return err
	}
	attachment.LocalPath = path
	return nil
}

func writeAttachmentFile(ctx context.Context, path string, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".capture-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if _, err := temporary.Write(body); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}
