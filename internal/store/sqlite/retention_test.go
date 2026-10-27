package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/store/sqlite"
)

func TestCleanupSnapshotExcludesNewWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	first, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	started := make(chan struct{})
	done := make(chan error, 1)
	err = first.WithDiagnosticSnapshot(context.Background(), true, func(_ []domain.RunRecord, _ []domain.PendingPublish) error {
		go func() {
			close(started)
			done <- second.StartRun(context.Background(), domain.RunRecord{ID: "run_new", StartedAt: time.Now(), Status: domain.RunStatusRunning})
		}()
		<-started
		select {
		case err := <-done:
			t.Fatalf("writer passed cleanup reservation: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("writer did not resume after cleanup")
	}
	if err := first.WithDiagnosticSnapshot(context.Background(), true, func(runs []domain.RunRecord, _ []domain.PendingPublish) error {
		if len(runs) != 1 || runs[0].Status != domain.RunStatusRunning {
			t.Fatalf("snapshot lost running run: %+v", runs)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
