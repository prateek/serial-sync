package artifact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/domain"
)

func TestVolumeRejectsChangedMemberCapture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chapter.epub")
	if err := os.WriteFile(path, []byte("changed after selection"), 0600); err != nil {
		t.Fatal(err)
	}
	chapter := domain.PublishCandidate{Release: domain.Release{Title: "Chapter 1"}, Artifact: domain.Artifact{StorageRef: path, SHA256: "selected-reading-copy-hash"}}
	_, err := New(t.TempDir()).BuildVolume(context.Background(), domain.VolumeEdition{}, "Test volume", []domain.PublishCandidate{chapter})
	if err == nil || !strings.Contains(err.Error(), "archived reading copy does not match its recorded hash") {
		t.Fatalf("expected captured-byte mismatch, got %v", err)
	}
}
