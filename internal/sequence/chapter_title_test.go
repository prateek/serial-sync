package sequence

import (
	"fmt"
	"testing"
	"time"
)

func chapterTitles(series string, titles ...string) []string {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	releases := make([]IndexedRelease, len(titles))
	for i, title := range titles {
		releases[i] = IndexedRelease{ReleaseID: fmt.Sprintf("r%03d", i), PublishedAt: start.Add(time.Duration(i) * time.Hour), Title: title, Sequence: Detect(title)}
	}
	indexes := SeriesIndexes(releases, false)
	got := make([]string, len(titles))
	for i, release := range releases {
		release.Sequence.SeriesIndex = indexes[release.ReleaseID]
		got[i] = ChapterTitle(release.Title, series, release.Sequence)
	}
	return got
}

func assertTitles(t *testing.T, got []string, want ...string) {
	t.Helper()
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("titles = %q, want %q", got, want)
	}
}

func TestChapterTitleNamesTheBookWhenNumberingRestarts(t *testing.T) {
	assertTitles(t, chapterTitles("The Sixth School",
		"The Sixth School. Book One. Chapter 120.",
		"The Sixth School. Book Two. Chapter 001.",
		"The Sixth School. Book Two. Chapter 071.",
	), "Book One, Chapter 120", "Book Two, Chapter 1", "Book Two, Chapter 71")
}

func TestChapterTitleOmitsTheBookWhenNumberingIsGlobal(t *testing.T) {
	assertTitles(t, chapterTitles("Rise of the Living Forge",
		"Rise of the Living Forge - Chapter 429",
		"Rise of the Living Forge - Book 5 Chapter 430",
	), "Chapter 429", "Chapter 430")
}

func TestChapterTitleKeepsTheSubtitleAfterTheChapterMarker(t *testing.T) {
	assertTitles(t, chapterTitles("Harbor",
		"Harbor - Chapter 12: The Fall",
		"Chapter 13 – Low Tide",
		"Harbor Chapter 14.5 - Aside",
	), "Chapter 12: The Fall", "Chapter 13: Low Tide", "Chapter 14.5: Aside")
}

func TestChapterTitleDropsTheSeriesNameFromUnnumberedReleases(t *testing.T) {
	assertTitles(t, chapterTitles("Rise of the Living Forge",
		"Rise of the Living Forge - Chapter 448",
		"Rise of the Living Forge - Chapters 449-450",
		"Rise of the Living Forge: Interlude",
		"Rise of the Living Forge",
		"Short Story #1 - Horror",
	), "Chapter 448", "Chapters 449-450", "Interlude", "Rise of the Living Forge", "Short Story #1 - Horror")
}

func TestChapterTitleReadsBookShorthand(t *testing.T) {
	assertTitles(t, chapterTitles("Harbor",
		"Harbor 1.40",
		"Harbor 2.1 - Return",
		"Harbor B2C2",
	), "Book One, Chapter 40", "Book Two, Chapter 1: Return", "Book Two, Chapter 2")
}

func TestChapterTitleNamesChapterRanges(t *testing.T) {
	assertTitles(t, chapterTitles("Rise of the Living Forge",
		"Rise of the Living Forge - Chapter 447-448",
		"Rise of the Living Forge - Chapter 479 -480",
		"Rise of the Living Forge - Chapter 627 & 628",
		"Rise of the Living Forge - Chapter 697 - 80 achieved!",
	), "Chapters 447-448", "Chapters 479-480", "Chapters 627-628", "Chapter 697: 80 achieved!")
	assertTitles(t, chapterTitles("The Masseuse",
		"The Masseuse (Ch. 1-3: Wagers)",
		"The Masseuse (Ch. 4-7: Escalations)",
	), "Chapters 1-3: Wagers", "Chapters 4-7: Escalations")
}

func TestChapterTitleKeepsBracketedNotesBesideTheChapter(t *testing.T) {
	assertTitles(t, chapterTitles("Advent of Eternity",
		"Advent of Eternity - Chapter 46 (Interlude 1)",
		"Advent of Eternity - Chapter 47 [Public]",
	), "Chapter 46 (Interlude 1)", "Chapter 47 [Public]")
}

func TestChapterTitleNamesPartsWithoutAChapter(t *testing.T) {
	assertTitles(t, chapterTitles("A Hospital Stay",
		"A Hospital Stay [Part 1]",
		"A Hospital Stay [part 2]",
	), "Part 1", "Part 2")
}

func TestChapterTitleKeepsDottedNumbersWhenTheIndexFollowsReleaseOrder(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	titles := []string{"Untitled Dungeon Story 1.2", "Untitled Dungeon Story 1.3 & 1.4"}
	releases := make([]IndexedRelease, len(titles))
	for i, title := range titles {
		releases[i] = IndexedRelease{ReleaseID: fmt.Sprint(i), PublishedAt: start.Add(time.Duration(i) * time.Hour), Title: title, Sequence: Detect(title)}
	}
	indexes := SeriesIndexes(releases, true)
	var got []string
	for _, release := range releases {
		release.Sequence.SeriesIndex = indexes[release.ReleaseID]
		got = append(got, ChapterTitle(release.Title, "Rinoz Side Stories", release.Sequence))
	}
	assertTitles(t, got, "Chapter 1.2", "Chapters 1.3-1.4")
}
