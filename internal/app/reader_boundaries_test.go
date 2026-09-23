package app_test

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/provider"
)

func TestFinalVolumeLeavesLaterChaptersAsSingles(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 50, FinalChapter: 2}

	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	third := docs[0]
	third.Normalized.ProviderReleaseID, third.Normalized.Title = "a3", "Alpha Saga Chapter 3"
	upstream.docs["alpha"] = append(docs, third)
	result, err := s.RunOnce(context.Background(), "", "", "run")
	if err != nil {
		t.Fatal(err)
	}
	files := findFiles(t, s.Config.Publishers[0].Path, ".epub")
	var names []string
	for _, path := range files {
		names = append(names, filepath.Base(path))
	}
	if want := []string{"alpha-saga-ch0003.epub", "alpha-saga-vol01.epub"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("final range must leave chapter 3 available separately: %v", names)
	}
	volume := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-vol01.epub")
	nav := string(epubNavigation(t, volume))
	if strings.Contains(nav, "Chapter 3") || !strings.Contains(nav, "Chapter 1") || !strings.Contains(nav, "Chapter 2") {
		t.Fatalf("wrong final-volume contents: %s", nav)
	}
	if len(result.Publish.Volumes) != 1 || !reflect.DeepEqual(result.Publish.Volumes[0].Present, []int{1, 2}) {
		t.Fatalf("wrong final-volume membership: %+v", result.Publish.Volumes)
	}
}

func TestAmbiguousBookChapterStillBlocksMixedFallbackVolume(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}
	s.Config.Series[0].Books = []config.BookConfig{{ID: "one", Number: 1}, {ID: "two", Number: 2, LastChapter: 1, SeriesPositionStart: 2}}

	docs := append([]provider.ReleaseDocument(nil), upstream.docs["alpha"][:2]...)
	docs[0].Normalized.Title = "Alpha Saga Book 1 Chapter 2"
	first := docs[1]
	first.Normalized.ProviderReleaseID, first.Normalized.Title = "a3", "Alpha Saga Chapter 1"
	upstream.docs["alpha"] = append(docs, first)
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, path := range findFiles(t, s.Config.Publishers[0].Path, ".epub") {
		names = append(names, filepath.Base(path))
	}
	want := []string{"alpha-saga-bk01-ch0002.epub", "alpha-saga-ch0001.epub", "alpha-saga-ch0002.epub"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("mixed book mappings must remain singles: %v", names)
	}
}

func TestOpenBookCannotReuseALaterBooksPosition(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 50}
	s.Config.Series[0].Books = []config.BookConfig{{ID: "one", Number: 1}, {ID: "two", Number: 2, LastChapter: 1, SeriesPositionStart: 2}}

	upstream.docs["alpha"] = upstream.docs["alpha"][:2]
	upstream.docs["alpha"][0].Normalized.Title = "Alpha Saga Book 1 Chapter 2"
	upstream.docs["alpha"][1].Normalized.Title = "Alpha Saga Book 2 Chapter 1"
	if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-bk01-ch0002.epub")
	// The open chapter stays a single; its reader index is the author's
	// book.chapter, never Book Two's scalar position.
	if opf := string(epubEntry(t, path, ".opf")); !strings.Contains(opf, `name="calibre:series_index" content="1.2"`) {
		t.Fatalf("open chapter lost its book.chapter index: %s", opf)
	}
	bookTwo := filepath.Join(s.Config.Publishers[0].Path, "alpha", "alpha-saga", "alpha-saga-bk02.epub")
	if opf := string(epubEntry(t, bookTwo, ".opf")); !strings.Contains(opf, `name="calibre:series_index" content="2.1"`) {
		t.Fatalf("Book Two does not sort with its first chapter: %s", opf)
	}
}

func TestInheritedSourceStillRejectsLegacyVolumePublisherBeforeWork(t *testing.T) {
	s, _ := newReaderService(t)
	s.Config.Series[0].Source = "alpha"
	for i := range s.Config.Series[0].Inputs {
		s.Config.Series[0].Inputs[i].Source = ""
	}
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: "volume", ChaptersPerVolume: 2}

	s.Config.Publishers = []config.PublisherConfig{{ID: "legacy", Kind: "exec", Command: []string{"false"}, Enabled: true}}
	if _, err := s.RunOnce(context.Background(), "alpha", "", "run"); err == nil || !strings.Contains(err.Error(), "protocol_version = 2") {
		t.Fatalf("preflight: %v", err)
	}
	releases, err := s.Repo.ListReleases(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Fatal("publisher preflight ran after catalog mutation")
	}
}
