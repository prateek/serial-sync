package artifact

import (
	"testing"

	"github.com/prateek/serial-sync/internal/domain"
)

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
			got := DetectSequence(tc.title)
			if got.Book != tc.book || got.Chapter != tc.chapter || got.ChapterLabel != tc.label || got.Part != tc.part || got.MatchedText == "" {
				t.Fatalf("%q: %+v", tc.title, got)
			}
			if (tc.label != "" || tc.part != "") && !got.KeepSingle {
				t.Fatal("non-integral sequence entered fixed-range volume")
			}
		})
	}
	for _, title := range []string{"A 2026 update", "Update 12", "Harbor", "Release v2.0 notes"} {
		if got := DetectSequence(title); got.HasChapter() || got.Part != "" {
			t.Fatalf("invented sequence in %q: %+v", title, got)
		}
	}
}

func TestExtendedSequenceFilenamesRemainDistinct(t *testing.T) {
	names := map[string]string{}
	for _, title := range []string{"Chapter 61", "Chapter 61.5", "Chapter 615", "Chapter 24D", "Chapter 24", "Chapter -186", "Chapter 186", "[Part K8]", "[Part K9]", "Chapter 4 [Part 1]", "Chapter 4 [Part 2]", "Chapter 5 [Part 1]"} {
		seq := DetectSequence(title)
		name := canonicalFileName(domain.StoryTrack{TrackName: "Harbor"}, domain.Release{}, domain.NormalizedRelease{Title: title}, "chapter.html", "text/html", &seq)
		if earlier, ok := names[name]; ok {
			t.Fatalf("%q and %q collide as %s", earlier, title, name)
		}
		names[name] = title
	}
}

func TestSelectedFilenamePartSurvivesAnAlreadyNumberedTitle(t *testing.T) {
	for _, title := range []string{"Harbor Chapter 4", "Harbor Book 2 Chapter 4"} {
		got := DetectSequence(title, "Harbor Chapter 4 [Part 1].epub")
		if got.Chapter != 4 || got.Part != "1" || !got.KeepSingle {
			t.Fatalf("attachment part lost: %+v", got)
		}
	}
}
