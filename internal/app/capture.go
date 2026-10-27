package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/prateek/serial-sync/internal/domain"
)

func inspectAttachments(normalized domain.NormalizedRelease) (domain.NormalizedRelease, error) {
	normalized.Attachments = append([]domain.Attachment(nil), normalized.Attachments...)
	for i := range normalized.Attachments {
		attachment := &normalized.Attachments[i]
		if attachment.LocalPath == "" {
			continue
		}
		data, err := os.ReadFile(attachment.LocalPath)
		if err != nil {
			return normalized, err
		}
		hash := hashBytes(data)
		if attachment.SHA256 != "" && attachment.SHA256 != hash {
			return normalized, fmt.Errorf("stored attachment hash mismatch: %s", attachment.LocalPath)
		}
		attachment.SHA256 = hash
	}
	return normalized, nil
}

// captureAttachments requires assessed digests and rechecks the bytes before writing captures.
func (s *Service) captureAttachments(sourceID string, normalized domain.NormalizedRelease, dryRun bool) (domain.NormalizedRelease, error) {
	if dryRun {
		return normalized, nil
	}
	normalized.Attachments = append([]domain.Attachment(nil), normalized.Attachments...)
	for i := range normalized.Attachments {
		attachment := &normalized.Attachments[i]
		if attachment.LocalPath == "" {
			continue
		}
		data, err := os.ReadFile(attachment.LocalPath)
		if err != nil {
			return normalized, err
		}
		hash := hashBytes(data)
		if hash != attachment.SHA256 {
			return normalized, fmt.Errorf("stored attachment hash mismatch: %s", attachment.LocalPath)
		}

		path := filepath.Join(s.Config.Runtime.ArtifactRoot, "inputs", sourceID, normalized.ProviderReleaseID, hash, filepath.Base(attachment.FileName))
		if path != attachment.LocalPath {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return normalized, err
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return normalized, err
			}
		}
		attachment.LocalPath = path
	}
	return normalized, nil
}
