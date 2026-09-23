package sequence

import (
	"fmt"
	"testing"
	"time"
)

func indexSeries(titles ...string) []string {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	releases := make([]IndexedRelease, len(titles))
	for i, title := range titles {
		releases[i] = IndexedRelease{ReleaseID: fmt.Sprintf("r%03d", i), PublishedAt: start.Add(time.Duration(i) * time.Hour), Title: title, Sequence: Detect(title)}
	}
	indexes := SeriesIndexes(releases, false)
	got := make([]string, len(titles))
	for i := range titles {
		got[i] = indexes[releases[i].ReleaseID]
	}
	return got
}

func assertIndexes(t *testing.T, got []string, want ...string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("indexes = %q, want %q", got, want)
	}
}

func TestSeriesIndexKeepsGlobalChapterNumbers(t *testing.T) {
	got := indexSeries(
		"Chapter 1368 - Guess Who?",
		"Chapter 1369 - A Successful Test",
		"Chapter 1369 - Are They Really?",
		"Chapter -186 Dire Lightning Storm Kong",
		"Chapter 1371 - Onward",
		"Return of the Runebound Professor - Chapter 1372 & THE START OF BOOK 5",
		"Chapter 1373",
	)
	// A repeated number and an unusable one share the preceding label; the
	// reader orders ties by publish date.
	assertIndexes(t, got, "1368", "1369", "1369", "1369", "1371", "1372", "1373")
}

func TestSeriesIndexUsesBookChapterWhenNumberingRestarts(t *testing.T) {
	got := indexSeries(
		"Book of the Dead 1-11 (edited)",
		"BoD Chapter 12 - Journey",
		"Chapter 13 - The Shattered World",
		"BoD B2 Chapter 1 - The Study of Death",
		"BoD B2 C2 - Growth",
		"B2C3 - Fear of the Dark",
		"BoD Vol2 Epilogue",
		"B3 Prelude",
		"B3C1 - Rumblings",
		"B5 Chapter 25 - Return",
		"B5 Epilogue",
		"B6 Prelude",
	)
	assertIndexes(t, got, "1.0", "1.12", "1.13", "2.1", "2.2", "2.3", "2.3", "3.0", "3.1", "5.25", "5.25", "6.0")
}

func TestSeriesIndexKeepsHalfChaptersAndInterludesBesideTheirChapter(t *testing.T) {
	got := indexSeries(
		"Book 3 - Chapter 5",
		"Book 3 - Chapter 5.5",
		"Book 3 - Interlude - Aberfa",
		"Book 3 - Chapter 6",
		"Book 4 - Chapter 1",
	)
	assertIndexes(t, got, "3.5", "3.5", "3.5", "3.6", "4.1")

	got = indexSeries("Part 3 - Chapter 11", "Part 3 - Chapter 11.5", "Part 3 - Chapter 12")
	assertIndexes(t, got, "11", "11.5", "12")
}

func TestSeriesIndexReadsDottedBookChapterTitles(t *testing.T) {
	got := indexSeries("Andy 3.9 - Grit", "Andy 3.10 - Ambush", "Aura Overload - 3.11 Tempting Fate", "Andy 4.1 - Friend Brew")
	assertIndexes(t, got, "3.9", "3.10", "3.11", "4.1")

	got = indexSeries("Speedrunner 0.0", "Speedrunner 0.1", "Speedrunner 0.99", "Speedrunner 1.05")
	assertIndexes(t, got, "0.0", "0.1", "0.99", "1.05")
}

func TestSeriesIndexFollowsChapterNumbersAcrossInterleavedReleaseStreams(t *testing.T) {
	got := indexSeries(
		"Rise of the Living Forge - Chapter 529",
		"Rise of the Living Forge Chapters 543 - 544",
		"Rise of the Living Forge - Chapter 530",
		"Nightmare Realm Summoner - Chapters 393 & 394",
	)
	assertIndexes(t, got, "529", "543", "530", "393")
}

func TestSeriesIndexNumbersAnthologiesAndPartsInReleaseOrder(t *testing.T) {
	got := indexSeries("Big Man on Campus", "Extra Credit", "New Year, New Eve [Part 1]", "The Sublet II")
	assertIndexes(t, got, "1", "2", "3", "4")

	got = indexSeries("A Hospital Stay [Part 1]", "A Hospital Stay [part 2]", "A Hospital Stay [Part K8]")
	assertIndexes(t, got, "1", "2", "2")
}

func TestSeriesIndexOfEarlierReleasesIgnoresLaterOnes(t *testing.T) {
	before := indexSeries("Chapter 1", "Interlude", "Chapter 2")
	after := indexSeries("Chapter 1", "Interlude", "Chapter 2", "Chapter 3", "Bonus")
	assertIndexes(t, after[:3], before...)
}

func TestSeriesIndexFollowsReleaseOrderWhenConfigured(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	var releases []IndexedRelease
	for i, title := range []string{"DBoC - Chapter 1", "The Devil you know - 1.1", "DYK 1.2 Registration", "Untitled Dungeon Story 1.3 & 1.4"} {
		releases = append(releases, IndexedRelease{ReleaseID: fmt.Sprint(i), PublishedAt: start.Add(time.Duration(i) * time.Hour), Title: title, Sequence: Detect(title)})
	}
	indexes := SeriesIndexes(releases, true)
	assertIndexes(t, []string{indexes["0"], indexes["1"], indexes["2"], indexes["3"]}, "1", "2", "3", "4")
}
