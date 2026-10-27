package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prateek/serial-sync/internal/domain"
)

const capturePrefix = "art_capture_"

type captureManifest struct {
	Version          int               `json:"version"`
	SourceID         string            `json:"source_id"`
	ReleaseID        string            `json:"release_id"`
	TrackID          string            `json:"track_id"`
	Kind             string            `json:"kind"`
	Filename         string            `json:"filename"`
	MIMEType         string            `json:"mime_type"`
	ContentSHA256    string            `json:"content_sha256"`
	MetadataSHA256   string            `json:"metadata_sha256"`
	NormalizedSHA256 string            `json:"normalized_sha256"`
	RawSHA256        string            `json:"raw_sha256"`
	Assets           map[string]string `json:"assets,omitempty"`
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (m *Materializer) installCapture(source domain.Source, track domain.StoryTrack, release domain.Release, plan domain.ArtifactPlan) (domain.Artifact, error) {
	if digest(plan.SelectedContent) != plan.SHA256 {
		return domain.Artifact{}, fmt.Errorf("reading bytes do not match planned SHA256")
	}
	manifest := captureManifest{
		Version: 1, SourceID: source.ID, ReleaseID: release.ID, TrackID: track.ID,
		Kind: plan.ArtifactKind, Filename: plan.Filename, MIMEType: plan.MIMEType,
		ContentSHA256: plan.SHA256, MetadataSHA256: digest(plan.MetadataJSON),
		NormalizedSHA256: digest(plan.NormalizedJSON), RawSHA256: digest(plan.RawJSON),
		Assets: map[string]string{},
	}
	for path, data := range plan.MetadataAssets {
		if filepath.Clean(path) != filepath.Join(m.Root, "metadata-assets", digest(data)) {
			return domain.Artifact{}, fmt.Errorf("invalid captured metadata asset: %s", path)
		}
		manifest.Assets[path] = digest(data)
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return domain.Artifact{}, err
	}
	id := capturePrefix + digest(manifestJSON)
	dir := filepath.Join(m.Root, "captures", strings.TrimPrefix(id, capturePrefix))
	blob := filepath.Join(m.Root, "blobs", "sha256", plan.SHA256)
	for path, data := range plan.MetadataAssets {
		if err := installArchiveFile(path, data); err != nil {
			return domain.Artifact{}, err
		}
	}
	if err := installArchiveFile(blob, plan.SelectedContent); err != nil {
		return domain.Artifact{}, err
	}
	files := map[string][]byte{
		"manifest.json": manifestJSON, "metadata.json": plan.MetadataJSON,
		"normalized.json": plan.NormalizedJSON, "raw.json": plan.RawJSON,
	}
	if err := installCaptureDirectory(dir, files); err != nil {
		return domain.Artifact{}, err
	}
	info, err := os.Stat(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return domain.Artifact{}, err
	}
	return domain.Artifact{
		ID: id, ReleaseID: release.ID, TrackID: track.ID, ArtifactKind: plan.ArtifactKind,
		IsCanonical: true, Filename: plan.Filename, MIMEType: plan.MIMEType,
		SHA256: plan.SHA256, StorageRef: blob, BuiltAt: info.ModTime().UTC(),
		State:         domain.ArtifactStateMaterialized,
		MetadataRef:   filepath.Join(dir, "metadata.json"),
		NormalizedRef: filepath.Join(dir, "normalized.json"), RawRef: filepath.Join(dir, "raw.json"),
	}, nil
}

func installCaptureDirectory(dir string, files map[string][]byte) error {
	parent := filepath.Dir(dir)
	if err := ensureArchiveDirectory(parent); err != nil {
		return err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		staging, err := os.MkdirTemp(parent, ".capture-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(staging)
		for name, data := range files {
			if err := installArchiveFile(filepath.Join(staging, name), data); err != nil {
				return err
			}
		}
		if err := syncDirectory(staging); err != nil {
			return err
		}
		if err := os.Rename(staging, dir); err == nil {
			return syncDirectory(parent)
		} else if _, statErr := os.Stat(dir); statErr != nil {
			return err
		}
		// A concurrent retry may have installed the same capture.
	}
	// Repair only from the exact plan whose checksums determine this identity.
	for name, data := range files {
		if err := installArchiveFile(filepath.Join(dir, name), data); err != nil {
			return err
		}
	}
	return syncDirectory(dir)
}

func installArchiveFile(path string, data []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if err := ensureArchiveDirectory(parent); err != nil {
		return err
	}
	temp, err := os.CreateTemp(parent, ".install-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(0644); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return err
	}
	return syncDirectory(parent)
}

func ensureArchiveDirectory(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("archive directory is not a directory: %s", path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if err := ensureArchiveDirectory(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0755); err != nil && !os.IsExist(err) {
		return err
	}
	return syncDirectory(parent)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func readCaptureMetadata(art domain.Artifact) ([]byte, error) {
	if !strings.HasPrefix(art.ID, capturePrefix) {
		return os.ReadFile(art.MetadataRef)
	}
	dir := filepath.Dir(art.MetadataRef)
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	if capturePrefix+digest(data) != art.ID {
		return nil, fmt.Errorf("capture manifest checksum mismatch")
	}
	var manifest captureManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	// Delivery can project a collision-safe filename without changing the capture.
	if manifest.Version != 1 || manifest.ReleaseID != art.ReleaseID || manifest.TrackID != art.TrackID || manifest.ContentSHA256 != art.SHA256 || manifest.Kind != art.ArtifactKind || manifest.MIMEType != art.MIMEType {
		return nil, fmt.Errorf("capture manifest does not match catalog reference")
	}
	if art.MetadataRef != filepath.Join(dir, "metadata.json") || art.NormalizedRef != filepath.Join(dir, "normalized.json") || art.RawRef != filepath.Join(dir, "raw.json") {
		return nil, fmt.Errorf("capture sidecar references do not match manifest")
	}
	checks := map[string]string{art.MetadataRef: manifest.MetadataSHA256, art.NormalizedRef: manifest.NormalizedSHA256, art.RawRef: manifest.RawSHA256}
	for path, sha := range manifest.Assets {
		checks[path] = sha
	}
	var metadata []byte
	for path, sha := range checks {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if digest(data) != sha {
			return nil, fmt.Errorf("capture sidecar checksum mismatch: %s", path)
		}
		if path == art.MetadataRef {
			metadata = data
		}
	}
	return metadata, nil
}

// Restore repairs reading bytes while retaining the verified original capture.
func (m *Materializer) Restore(ctx context.Context, art domain.Artifact, plan domain.ArtifactPlan) (domain.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return domain.Artifact{}, err
	}
	if _, err := readCaptureMetadata(art); err != nil {
		return domain.Artifact{}, err
	}
	if art.SHA256 != plan.SHA256 || digest(plan.SelectedContent) != art.SHA256 || art.Filename != plan.Filename || art.MIMEType != plan.MIMEType || art.ArtifactKind != plan.ArtifactKind {
		return domain.Artifact{}, fmt.Errorf("repair plan does not match retained reading copy")
	}
	if plan.ValidateEPUBCheck {
		if err := validateEPUBArchive(ctx, plan.SelectedContent); err != nil {
			return domain.Artifact{}, err
		}
	}
	if err := installArchiveFile(art.StorageRef, plan.SelectedContent); err != nil {
		return domain.Artifact{}, err
	}
	return art, nil
}
