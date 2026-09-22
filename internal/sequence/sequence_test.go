package sequence

import (
	"testing"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestExplicitSequenceOverrideCanResolveExtendedChapter(t *testing.T) {
	cfg := &config.Config{Series: []config.SeriesConfig{{ID: "harbor", SequenceOverrides: []config.SequenceOverride{{Source: "fictional", ReleaseID: "1", Chapter: 62}}}}}
	got := ApplyDetected(cfg, "fictional", "1", domain.TrackDecision{SeriesID: "harbor"}, Detect("Chapter 61.5"))
	if got.Sequence.Chapter != 62 || got.Sequence.ChapterLabel != "" || got.Sequence.KeepSingle {
		t.Fatalf("explicit override kept ambiguous sequence: %+v", got.Sequence)
	}
}

func TestBookLabelsDoNotInventCoverageOrResolveUnknownBooks(t *testing.T) {
	series := config.SeriesConfig{ID: "harbor", Books: []config.BookConfig{{ID: "arrival", Number: 1}}}
	got := Resolve(series, domain.Sequence{Chapter: 2}, "arrival")
	if got.BookID != "arrival" || series.Books[0].LastChapter != 0 {
		t.Fatalf("book identity lost or coverage invented: %+v", got)
	}
	got = Resolve(series, domain.Sequence{Book: 3, Chapter: 2}, "")
	if got.BookID != "book:3" || got.Position != 0 || got.Reason == "" {
		t.Fatalf("unknown book treated as resolved: %+v", got)
	}
}

func TestPartHasNoScalarPositionEvenInsideADeclaredBook(t *testing.T) {
	series := config.SeriesConfig{Books: []config.BookConfig{{ID: "arrival", Number: 1, FirstChapter: 1, LastChapter: 10}}}
	got := Resolve(series, Detect("Chapter 4 [Part 1]"), "arrival")
	if got.Position != 0 || !got.KeepSingle || got.Part != "1" {
		t.Fatalf("part flattened to scalar position: %+v", got)
	}
}
