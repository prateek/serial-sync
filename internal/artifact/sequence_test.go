package artifact

import (
	"testing"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/sequence"
)

func TestExtendedSequenceFilenamesRemainDistinct(t *testing.T) {
	names := map[string]string{}
	for _, title := range []string{"Chapter 61", "Chapter 61.5", "Chapter 615", "Chapter 24D", "Chapter 24", "Chapter -186", "Chapter 186", "[Part K8]", "[Part K9]", "Chapter 4 [Part 1]", "Chapter 4 [Part 2]", "Chapter 5 [Part 1]"} {
		seq := sequence.Detect(title)
		name := canonicalFileName(domain.StoryTrack{TrackName: "Harbor"}, domain.Release{}, domain.NormalizedRelease{Title: title}, "chapter.html", "text/html", &seq)
		if earlier, ok := names[name]; ok {
			t.Fatalf("%q and %q collide as %s", earlier, title, name)
		}
		names[name] = title
	}
}
