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
func TestSequenceKeepsNonIntegralChapterIdentity(t *testing.T) {
	cases := []struct {
		title         string
		book, chapter int
		label, part   string
	}{
		{"Name 3.60", 0, 0, "3.60", ""}, {"B6C57", 6, 57, "", ""}, {"[Part K8]", 0, 0, "", "K8"},
		{"Chapter 4 [Part 1]", 0, 4, "", "1"}, {"B2C4 [Part K8]", 2, 4, "", "K8"},
		{"Ch. 24D", 0, 0, "24D", ""}, {"61.5", 0, 0, "61.5", ""}, {"Chapter -186", 0, 0, "-186", ""},
		{"Chaptger 204", 0, 204, "", ""}, {"Chapeter 480", 0, 480, "", ""}, {"737 - Title", 0, 737, "", ""},
		{"chapter-50.epub", 0, 50, "", ""}, {"Harbor 3.60.epub", 0, 0, "3.60", ""},
		{"Chapter twenty three", 0, 23, "", ""}, {"Book 2 Chapter 10", 2, 10, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := Detect(tc.title)
			if got.Book != tc.book || got.Chapter != tc.chapter || got.ChapterLabel != tc.label || got.Part != tc.part || got.MatchedText == "" {
				t.Fatalf("%q: %+v", tc.title, got)
			}
			if (tc.label != "" || tc.part != "") && !got.KeepSingle {
				t.Fatal("non-integral sequence entered fixed-range volume")
			}
		})
	}
	for _, title := range []string{"A 2026 update", "Update 12", "Harbor", "Release v2.0 notes"} {
		if got := Detect(title); got.HasChapter() || got.Part != "" {
			t.Fatalf("invented sequence in %q: %+v", title, got)
		}
	}
}

func TestSelectedFilenamePartSurvivesAnAlreadyNumberedTitle(t *testing.T) {
	for _, title := range []string{"Harbor Chapter 4", "Harbor Book 2 Chapter 4"} {
		got := Detect(title, "Harbor Chapter 4 [Part 1].epub")
		if got.Chapter != 4 || got.Part != "1" || !got.KeepSingle {
			t.Fatalf("attachment part lost: %+v", got)
		}
	}
}
