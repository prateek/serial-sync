package app

import (
	"testing"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestPublicationIdentitySourceInheritance(t *testing.T) {
	for _, input := range []struct {
		name, series, book string
		want               domain.PublicationIdentitySource
	}{
		{"default", "", "", ""},
		{"explicit default", "embedded", "", ""},
		{"series inherited", "release", "", domain.PublicationIdentityRelease},
		{"book opts in", "embedded", "release", domain.PublicationIdentityRelease},
		{"book restores embedded", "release", "embedded", ""},
	} {
		t.Run(input.name, func(t *testing.T) {
			service := &Service{Config: &config.Config{Series: []config.SeriesConfig{{
				ID: "harbor", Metadata: config.PublicationConfig{IdentitySource: input.series},
				Books: []config.BookConfig{{ID: "first", Metadata: config.PublicationConfig{IdentitySource: input.book}}},
			}}}}
			selected, err := service.publicationMetadata("author", domain.NormalizedRelease{}, domain.TrackDecision{SeriesID: "harbor", BookID: "first", OutputFormat: domain.OutputFormatEPUB})
			if err != nil || selected.IdentitySource != input.want {
				t.Fatalf("selected identity source = %+v, %v; want %q", selected, err, input.want)
			}
		})
	}
}
