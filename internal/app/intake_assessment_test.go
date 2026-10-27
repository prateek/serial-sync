package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func TestRebuildPreviewAssessesDamagedArchivedCopies(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			s, upstream := newReaderService(t)
			upstream.docs["alpha"] = upstream.docs["alpha"][:1]
			if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
				t.Fatal(err)
			}
			files := canonicalReadingPaths(t, s)
			if len(files) != 1 {
				t.Fatalf("archived copies: %v", files)
			}
			original := mustReadFile(t, files[0])
			published := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", canonicalReadingFilename(t, s))
			originalPublished := mustReadFile(t, published)
			oldTime := time.Unix(1_600_000_000, 0)
			if err := os.Chtimes(published, oldTime, oldTime); err != nil {
				t.Fatal(err)
			}
			beforePublished := intakeTreeSnapshot(t, s.Config.Publishers[0].Path)
			if damage == "missing" {
				if err := os.Remove(files[0]); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(files[0], []byte("corrupt copy"), 0600); err != nil {
				t.Fatal(err)
			}
			s.Providers = provider.NewRegistry()
			before := intakeTreeSnapshot(t, s.Roots.StateDir)
			preview, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
			if err != nil {
				t.Fatal(err)
			}
			if after := intakeTreeSnapshot(t, s.Roots.StateDir); !reflect.DeepEqual(before, after) {
				t.Fatal("preview mutated state or published files")
			}
			if len(preview.Plans) != 1 || preview.Plans[0].Action != "rebuild" {
				t.Fatalf("preview must plan archive repair: %+v", preview.Plans)
			}
			if len(preview.Publish.Items) != 1 || preview.Publish.Items[0].Action != "replace" || preview.Publish.Items[0].TargetRef != published {
				t.Fatalf("preview must conservatively plan possible published replacement: %+v", preview.Publish.Items)
			}
			if len(preview.Notices) != 1 || !strings.Contains(preview.Notices[0], "may reset saved reading positions") {
				t.Fatalf("potential replacement must carry the reading-position warning: %v", preview.Notices)
			}
			result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild")
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Plans) != 1 || result.Plans[0].Action != "update" {
				t.Fatalf("execution must repair archive: %+v", result.Plans)
			}
			if result.Publish.Published != 0 || result.Publish.Skipped != 1 || len(result.Publish.Items) != 1 || result.Publish.Items[0].Action != "skipped" || result.Publish.Items[0].TargetRef != published {
				t.Fatalf("same-byte HTML repair must skip delivery: %+v", result.Publish)
			}
			if result.Publish.Items[0].ArtifactID != preview.Publish.Items[0].ArtifactID {
				t.Fatal("archive repair changed the delivered identity")
			}
			if !bytes.Equal(originalPublished, mustReadFile(t, published)) || !reflect.DeepEqual(beforePublished, intakeTreeSnapshot(t, s.Config.Publishers[0].Path)) {
				t.Fatal("archive repair changed the intact published file")
			}
			t.Logf("archive=%s preview_delivery=%s notices=%d executed_delivery=%s published_bytes_and_mtime_unchanged=true", damage, preview.Publish.Items[0].Action, len(preview.Notices), result.Publish.Items[0].Action)
			if !bytes.Equal(original, mustReadFile(t, files[0])) {
				t.Fatal("repair changed archived bytes")
			}
			again, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
			if err != nil || len(again.Plans) != 1 || again.Plans[0].Action != "unchanged" {
				t.Fatalf("repaired archive is not current: %+v %v", again.Plans, err)
			}
			if len(again.Publish.Items) != 1 || again.Publish.Items[0].Action != "unchanged" || again.Publish.Items[0].TargetRef != published || len(again.Notices) != 0 {
				t.Fatalf("delivery is not a fixed point: %+v", again)
			}
		})
	}
}

type intakeFileSnapshot struct {
	Content  string
	Mode     fs.FileMode
	Modified time.Time
}

func intakeTreeSnapshot(t *testing.T, root string) map[string]intakeFileSnapshot {
	t.Helper()
	files := map[string]intakeFileSnapshot{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// SQLite's shared-memory read locks are not durable catalog state.
		if strings.HasSuffix(path, "-shm") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		snapshot := intakeFileSnapshot{Mode: info.Mode(), Modified: info.ModTime()}
		if !entry.IsDir() {
			snapshot.Content = string(mustReadFile(t, path))
		}
		files[path] = snapshot
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestRebuildPreviewAndExecutionRejectDamagedCapturedInputs(t *testing.T) {
	for _, tc := range []struct{ name, strategy, damage, want string }{
		{"missing selected attachment", "attachment_only", "missing", "no such file"},
		{"corrupt selected attachment", "attachment_only", "corrupt", "hash mismatch"},
		{"selected attachment without local path", "attachment_only", "no_path", "missing a local_path"},
		{"missing unselected attachment", "text_post", "missing", "no such file"},
		{"corrupt unselected attachment", "text_post", "corrupt", "hash mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, upstream := newReaderService(t)
			upstream.docs["alpha"] = upstream.docs["alpha"][:1]
			s.Config.Series[0].Inputs[0].ContentStrategy = tc.strategy
			path := filepath.Join(t.TempDir(), "chapter.pdf")
			if err := os.WriteFile(path, []byte("captured original PDF"), 0600); err != nil {
				t.Fatal(err)
			}
			upstream.docs["alpha"][0].Normalized.Attachments = []domain.Attachment{{FileName: "chapter.pdf", MIMEType: "application/pdf", LocalPath: path, DownloadURL: "https://example.invalid/chapter.pdf"}}
			if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
				t.Fatal(err)
			}
			release, err := s.Repo.GetReleaseByProviderID(context.Background(), "alpha", "a1")
			if err != nil {
				t.Fatal(err)
			}
			var normalized domain.NormalizedRelease
			if err := json.Unmarshal(mustReadFile(t, release.NormalizedPayloadRef), &normalized); err != nil {
				t.Fatal(err)
			}
			captured := normalized.Attachments[0].LocalPath
			switch tc.damage {
			case "missing":
				if err := os.Remove(captured); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(captured, []byte("corrupted capture"), 0600); err != nil {
					t.Fatal(err)
				}
			case "no_path":
				s.Config.Series[0].Title = "Renamed Saga"
				normalized.Attachments[0].LocalPath = ""
				data, err := json.Marshal(normalized)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(release.NormalizedPayloadRef, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			s.Providers = provider.NewRegistry()
			before := intakeTreeSnapshot(t, s.Roots.StateDir)
			preview, previewErr := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
			if previewErr == nil || !strings.Contains(previewErr.Error(), tc.want) {
				t.Fatalf("preview must reject input: %+v %v", preview, previewErr)
			}
			if after := intakeTreeSnapshot(t, s.Roots.StateDir); !reflect.DeepEqual(before, after) {
				t.Fatal("blocked preview mutated state")
			}
			result, runErr := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild")
			if runErr == nil || !strings.Contains(runErr.Error(), tc.want) {
				t.Fatalf("execution must reject input: %+v %v", result, runErr)
			}
			if !reflect.DeepEqual(preview.Blocked, result.Blocked) {
				t.Fatalf("different input blockers: preview=%v execution=%v", preview.Blocked, result.Blocked)
			}
		})
	}
}

func TestRebuildPreviewDoesNotCaptureOrConvertPDF(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	s.Config.Series[0].Inputs[0].ContentStrategy = "attachment_only"
	path := filepath.Join(t.TempDir(), "chapter.pdf")
	if err := os.WriteFile(path, mustReadFile(t, "../../testdata/fixtures/reader/chapter.pdf"), 0600); err != nil {
		t.Fatal(err)
	}
	upstream.docs["alpha"][0].Normalized.Attachments = []domain.Attachment{{FileName: "chapter.pdf", MIMEType: "application/pdf", LocalPath: path}}
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	s.Config.Series[0].Output.Format = "epub"
	s.Providers = provider.NewRegistry()
	trapDir := t.TempDir()
	marker := filepath.Join(trapDir, "converter-called")
	t.Setenv("INTAKE_CONVERTER_MARKER", marker)
	for _, tool := range []string{"pdftohtml", "epubcheck", "serial-sync-epubcheck"} {
		if err := os.WriteFile(filepath.Join(trapDir, tool), []byte("#!/bin/sh\necho called > \"$INTAKE_CONVERTER_MARKER\"\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", trapDir)
	before := intakeTreeSnapshot(t, s.Roots.StateDir)
	preview, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Plans) != 1 || preview.Plans[0].Action != "rebuild" || !strings.HasSuffix(preview.Plans[0].Filename, ".epub") {
		t.Fatalf("preview omitted PDF conversion: %+v", preview.Plans)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("preview invoked converter or validator: %v", err)
	}
	if after := intakeTreeSnapshot(t, s.Roots.StateDir); !reflect.DeepEqual(before, after) {
		t.Fatal("conversion preview mutated state")
	}
}
