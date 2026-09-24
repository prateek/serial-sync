package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

// The library manager renames every dropped file and rewrites its metadata,
// so later runs must recognise each release from the catalog alone.
func TestDropPublisherHandsOffEachReleaseOnce(t *testing.T) {
	s, upstream := newReaderService(t)
	library := filepath.Join(s.Roots.StateDir, "library")
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatal(err)
	}
	s.Config.Publishers = []config.PublisherConfig{{ID: "library", Kind: "drop", Path: library, Enabled: true}}
	upstream.docs["alpha"] = upstream.docs["alpha"][:2]

	first, err := s.RunOnce(context.Background(), "", "", "run")
	if err != nil {
		t.Fatal(err)
	}
	dropped := findFiles(t, library, "")
	if first.Publish.Published != 2 || len(dropped) != 2 {
		t.Fatalf("first run dropped %d file(s) %v, published %d", len(dropped), dropped, first.Publish.Published)
	}
	for _, path := range dropped {
		if !strings.HasPrefix(path, filepath.Join(library, "alpha", "alpha-saga")+string(filepath.Separator)) {
			t.Fatalf("dropped outside the source/series layout: %s", path)
		}
	}

	imported := filepath.Join(library, "Alpha Author", "Alpha Saga")
	if err := os.MkdirAll(imported, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, path := range dropped {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		renamed := filepath.Join(imported, "chapter "+string(rune('1'+i))+filepath.Ext(path))
		if err := os.WriteFile(renamed, append(data, " rewritten metadata"...), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(library, "alpha")); err != nil {
		t.Fatal(err)
	}

	second, err := s.RunOnce(context.Background(), "", "", "run")
	if err != nil {
		t.Fatal(err)
	}
	if second.Publish.Published != 0 || second.Publish.Skipped != 2 {
		t.Fatalf("second run re-dropped imported files: %+v", second.Publish)
	}
	if files := findFiles(t, library, ""); len(files) != 2 {
		t.Fatalf("library holds %v after an unchanged run", files)
	}

	upstream.docs["alpha"][0].Normalized.TextHTML = "<p>One, revised</p>"
	upstream.docs["alpha"][0].Normalized.EditedAt = time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC)
	third, err := s.RunOnce(context.Background(), "", "", "run")
	if err != nil {
		t.Fatal(err)
	}
	held := 0
	for _, item := range third.Publish.Items {
		if item.Action == "held" {
			held++
		}
	}
	if third.Publish.Published != 0 || held != 1 {
		t.Fatalf("revision was not held: %+v", third.Publish)
	}
	if files := findFiles(t, library, ""); len(files) != 2 {
		t.Fatalf("revision dropped a second copy: %v", files)
	}
	forensics, err := s.ExplainRun(context.Background(), third.Publish.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if forensics.PublishHeld != 1 {
		t.Fatalf("held revision missing from run forensics: %+v", forensics)
	}

	upstream.docs["alpha"] = append(upstream.docs["alpha"], provider.ReleaseDocument{
		Normalized: domain.NormalizedRelease{Provider: "patreon", ProviderReleaseID: "a4", Title: "Alpha Saga - Chapter 3", PublishedAt: time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC), TextHTML: "<p>Three</p>"},
		RawJSON:    []byte(`{"id":"a4","title":"Alpha Saga - Chapter 3"}`),
	})
	fourth, err := s.RunOnce(context.Background(), "", "", "run")
	if err != nil {
		t.Fatal(err)
	}
	fresh := findFiles(t, filepath.Join(library, "alpha"), "")
	if fourth.Publish.Published != 1 || len(fresh) != 1 {
		t.Fatalf("new chapter not dropped exactly once: %v %+v", fresh, fourth.Publish)
	}
}

func TestDropPublisherNeedsAnExistingRoot(t *testing.T) {
	s, upstream := newReaderService(t)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	library := filepath.Join(s.Roots.StateDir, "unmounted")
	s.Config.Publishers = []config.PublisherConfig{{ID: "library", Kind: "drop", Path: library, Enabled: true}}

	_, err := s.RunOnce(context.Background(), "", "", "run")
	if err == nil || !strings.Contains(err.Error(), "publication incomplete") {
		t.Fatalf("RunOnce() error = %v, want incomplete publication", err)
	}
	if _, statErr := os.Stat(library); !os.IsNotExist(statErr) {
		t.Fatalf("drop publisher created its missing root: %v", statErr)
	}
}
