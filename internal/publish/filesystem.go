package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/domain"
)

type FilesystemTarget struct {
	ID   string
	Path string
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
	targetMetadataPath := targetArtifactPath + ".metadata.json"
	if err := copyFile(candidate.Artifact.StorageRef, targetArtifactPath); err != nil {
		return domain.PublishRecord{}, err
	}
	if candidate.Artifact.MetadataRef != "" {
		if err := copyFile(candidate.Artifact.MetadataRef, targetMetadataPath); err != nil {
			return domain.PublishRecord{}, err
		}
	}
	publishHash := hashPublish(target.ID, candidate.Artifact.SHA256, targetArtifactPath)
	return domain.PublishRecord{
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

func hashPublish(targetID, artifactSHA, targetPath string) string {
	sum := sha256.Sum256([]byte(targetID + "\x00" + artifactSHA + "\x00" + targetPath))
	return hex.EncodeToString(sum[:])
}

func copyFile(src, dst string) error {
	from, err := os.Open(src)
	if err != nil {
		return err
	}
	defer from.Close()
	to, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = to.Close() }()
	if _, err := io.Copy(to, from); err != nil {
		return err
	}
	if err := to.Sync(); err != nil {
		return err
	}
	return nil
}
