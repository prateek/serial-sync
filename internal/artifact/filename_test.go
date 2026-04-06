package artifact

import (
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

func TestCanonicalFileNameUsesSortableBookAndChapter(t *testing.T) {
	t.Parallel()

	name := canonicalFileName(
		domain.StoryTrack{TrackName: "The Sixth School"},
		domain.Release{ProviderReleaseID: "154707035"},
		domain.NormalizedRelease{Title: "The Sixth School. Book Two. Chapter 058."},
		"Book Two Chapter 058.epub",
		"application/epub+zip",
	)

	if want := "the-sixth-school-bk02-ch00058-r154707035.epub"; name != want {
		t.Fatalf("unexpected canonical filename: got %q want %q", name, want)
	}
}

func TestCanonicalFileNameParsesWordNumberChapter(t *testing.T) {
	t.Parallel()

	name := canonicalFileName(
		domain.StoryTrack{TrackName: "The Sixth School Editing Marathon"},
		domain.Release{ProviderReleaseID: "149921323"},
		domain.NormalizedRelease{Title: "Editing Marathon: Chapter Eighty"},
		"080 Chapter Eighty.pdf",
		"application/pdf",
	)

	if want := "the-sixth-school-editing-marathon-ch00080-r149921323.pdf"; name != want {
		t.Fatalf("unexpected canonical filename: got %q want %q", name, want)
	}
}

func TestCanonicalFileNameFallsBackToDateAndTitle(t *testing.T) {
	t.Parallel()

	name := canonicalFileName(
		domain.StoryTrack{TrackName: "Announcements"},
		domain.Release{
			ProviderReleaseID: "note-42",
			PublishedAt:       time.Date(2026, time.April, 5, 15, 30, 0, 0, time.UTC),
		},
		domain.NormalizedRelease{Title: "Schedule update for April"},
		"",
		"text/html",
	)

	if want := "announcements-2026-04-05-schedule-update-for-april-rnote-42.html"; name != want {
		t.Fatalf("unexpected canonical filename: got %q want %q", name, want)
	}
}
