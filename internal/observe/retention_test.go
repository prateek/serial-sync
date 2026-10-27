package observe

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

func TestArchivedHistoryAndMissingPayloadAreExplicit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "run_archive.jsonl.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	event := LogEvent{Timestamp: time.Now().UTC(), Kind: KindPublishFailed, Level: "error", Message: "synthetic", PayloadRef: filepath.Join(root, "missing.json")}
	if err := json.NewEncoder(gz).Encode(event); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	record, err := ReadRun(root, "run_archive", nil)
	if err != nil {
		t.Fatal(err)
	}
	if record.History != "archived" || len(record.Events) != 1 || len(record.MissingPayloads) != 1 {
		t.Fatalf("archive not explicit: %+v", record)
	}
	if record.Forensics().KindCounts[KindPublishFailed] != 1 {
		t.Fatal("archive classification changed")
	}
	for _, data := range [][]byte{nil, []byte("broken archive")} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRun(root, "run_archive", nil); err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("malformed archive error lacks path: %v", err)
		}
	}
}

func TestRepeatedCleanupBoundsDiagnosticFiles(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")

	os.MkdirAll(logs, 0700)
	now := time.Now().UTC()
	var runs []domain.RunRecord
	var maxFiles int
	for day := 0; day < 60; day++ {
		finished := now.Add(time.Duration(day) * 24 * time.Hour)
		id := fmt.Sprintf("run_%03d", day)
		runs = append(runs, domain.RunRecord{ID: id, FinishedAt: &finished, Status: domain.RunStatusSucceeded})
		os.WriteFile(filepath.Join(logs, id+".jsonl"), []byte("synthetic"), 0600)
		dir := filepath.Join(logs, "event-payloads", id)
		os.MkdirAll(dir, 0700)
		for i := 0; i < 20; i++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", i)), []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := Cleanup(logs, finished.Add(-10*24*time.Hour), runs, true); err != nil {
			t.Fatal(err)
		}
		count, nodes := 0, 0
		err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			nodes++
			if !d.IsDir() {
				count++
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if count > maxFiles {
			maxFiles = count
		}
		if nodes > 246 {
			t.Fatalf("day %d retained %d filesystem entries", day, nodes)
		}
		if count > 232 {
			t.Fatalf("day %d retained %d files", day, count)
		}
	}
	if maxFiles != 232 {
		t.Fatalf("fixture did not exercise full window: %d", maxFiles)
	}
}

func TestCleanupRejectsSymlinkedDiagnosticDirectories(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	capture := filepath.Join(root, "capture")
	if err := os.MkdirAll(logs, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(capture, 0700); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(capture, "run_old")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protected, "content.json"), []byte("capture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(capture, filepath.Join(logs, "event-payloads")); err != nil {
		t.Fatal(err)
	}
	finished := time.Now().Add(-24 * time.Hour)
	_, err := Cleanup(logs, time.Now(), []domain.RunRecord{{ID: "run_old", FinishedAt: &finished}}, true)
	if err == nil {
		t.Fatal("symlinked diagnostic root accepted")
	}
	if data, err := os.ReadFile(filepath.Join(protected, "content.json")); err != nil || string(data) != "capture" {
		t.Fatalf("capture changed: %s %v", data, err)
	}
}

func TestCleanupExpiresOrphansAndInterruptedTemps(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(root, "logs")

	dirs := []string{logs, filepath.Join(logs, "event-payloads", "run_orphan")}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	before := time.Now().Add(-10 * 24 * time.Hour)
	stale := []string{filepath.Join(logs, "run_orphan.log"), filepath.Join(logs, "run_orphan.jsonl"), filepath.Join(logs, ".retention-old")}
	for _, path := range stale {
		if err := os.WriteFile(path, []byte("orphan"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	orphanDir := filepath.Join(logs, "event-payloads", "run_orphan")
	if err := os.Chtimes(orphanDir, old, old); err != nil {
		t.Fatal(err)
	}
	stale = append(stale, orphanDir)
	recent := filepath.Join(logs, "run_new.jsonl")
	if err := os.WriteFile(recent, nil, 0600); err != nil {
		t.Fatal(err)
	}
	running := filepath.Join(logs, "run_known.log")
	if err := os.WriteFile(running, nil, 0600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(running, old, old)
	runs := []domain.RunRecord{{ID: "run_known", Status: domain.RunStatusRunning}}
	preview, err := Cleanup(logs, before, runs, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Paths) != len(stale) {
		t.Fatalf("orphan selection: %+v", preview)
	}
	if _, err := Cleanup(logs, before, runs, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range stale {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("orphan retained: %s %v", path, err)
		}
	}
	for _, path := range []string{recent, running} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unexpired history lost: %s %v", path, err)
		}
	}
}

func TestUnreadablePayloadDoesNotHideRunHistory(t *testing.T) {
	root := t.TempDir()
	ref := filepath.Join(root, "loop")
	if err := os.Symlink("loop", ref); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(LogEvent{Timestamp: time.Now().UTC(), Kind: KindPublishFailed, PayloadRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "run_test.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	record, err := ReadRun(root, "run_test", nil)
	if err != nil || len(record.Events) != 1 || len(record.MissingPayloads) != 1 {
		t.Fatalf("unreadable payload hid events: %+v %v", record, err)
	}
}

type startFailureStore struct{ recordingStore }

func (s *startFailureStore) StartRun(context.Context, domain.RunRecord) error {
	return errors.New("synthetic catalog busy")
}

func TestRejectedRunCreatesNoOrphanDiagnostics(t *testing.T) {
	root := t.TempDir()
	if _, err := Start(context.Background(), &startFailureStore{}, "test", "", false, Options{LogRoot: root}); err == nil {
		t.Fatal("catalog failure ignored")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected run left files: %v %v", entries, err)
	}
}
