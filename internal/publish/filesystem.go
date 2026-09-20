package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/domain"
)

type FilesystemTarget struct {
	ID          string
	Path        string
	OwnedHashes []string
}

func PublishFilesystem(ctx context.Context, target FilesystemTarget, candidate domain.PublishCandidate) (domain.PublishRecord, error) {
	select {
	case <-ctx.Done():
		return domain.PublishRecord{}, ctx.Err()
	default:
	}
	targetDir := filepath.Join(target.Path, candidate.Source.ID, candidate.Track.TrackKey)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return domain.PublishRecord{}, err
	}
	targetArtifactPath := filepath.Join(targetDir, candidate.Artifact.Filename)
	current, err := CheckFilesystemDestination(targetArtifactPath, target.OwnedHashes)
	if err != nil {
		return domain.PublishRecord{}, err
	}
	if current != candidate.Artifact.SHA256 {
		if err := copyFile(candidate.Artifact.StorageRef, targetArtifactPath, candidate.Artifact.SHA256); err != nil {
			return domain.PublishRecord{}, err
		}
	}
	publishHash := hashPublish(target.ID, candidate.Artifact.SHA256, targetArtifactPath)
	return domain.PublishRecord{
		Filename:    candidate.Artifact.Filename,
		ID:          "pub_" + uuid.NewString(),
		ArtifactID:  candidate.Artifact.ID,
		TargetID:    target.ID,
		TargetKind:  "filesystem",
		TargetRef:   targetArtifactPath,
		PublishHash: publishHash,
		PublishedAt: time.Now().UTC(),
		Status:      domain.PublishStatusPublished,
	}, nil
}

func PublishHash(targetID, artifactSHA, targetPath string) string {
	return hashPublish(targetID, artifactSHA, targetPath)
}

func CheckFilesystemDestination(path string, ownedHashes []string) (string, error) {
	current, err := FileHash(path)
	if err != nil {
		return "", err
	}
	if current != "" && !slices.Contains(ownedHashes, current) {
		return "", fmt.Errorf("destination ownership conflict: %s", path)
	}
	return current, nil
}

func hashPublish(targetID, artifactSHA, targetPath string) string {
	sum := sha256.Sum256([]byte(targetID + "\x00" + artifactSHA + "\x00" + targetPath))
	return hex.EncodeToString(sum[:])
}

func FileHash(path string) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("destination is not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyFile(src, dst, expectedHash string) error {
	from, err := os.Open(src)
	if err != nil {
		return err
	}
	defer from.Close()
	to, err := os.CreateTemp(filepath.Dir(dst), ".serial-sync-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = to.Close(); _ = os.Remove(to.Name()) }()
	if _, err := io.Copy(to, from); err != nil {
		return err
	}
	if err := to.Sync(); err != nil {
		return err
	}
	if err := to.Close(); err != nil {
		return err
	}
	actual, err := FileHash(to.Name())
	if err != nil {
		return err
	}
	if actual != expectedHash {
		return fmt.Errorf("artifact hash mismatch for %s", src)
	}
	if err := os.Chmod(to.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(to.Name(), dst)
}
