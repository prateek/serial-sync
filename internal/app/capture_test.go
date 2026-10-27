package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestCaptureRejectsInputChangedAfterAssessment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chapter.pdf")
	if err := os.WriteFile(path, []byte("assessed bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	normalized, err := inspectAttachments(domain.NormalizedRelease{ProviderReleaseID: "one", Attachments: []domain.Attachment{{FileName: "chapter.pdf", LocalPath: path}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "captures")
	s := &Service{Config: &config.Config{Runtime: config.RuntimeConfig{ArtifactRoot: root}}}
	if _, err := s.captureAttachments("source", normalized, false); err == nil || !strings.Contains(err.Error(), "stored attachment hash mismatch") {
		t.Fatalf("capture accepted changed input: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("rejected input created capture output: %v", err)
	}
}
