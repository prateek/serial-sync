package discovery_test

import (
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/discovery"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestRepostEvidenceIgnoresAttachmentProvenance(t *testing.T) {
	for _, mediaID := range []string{"", "original-media"} {
		t.Run("earlier-media="+mediaID, func(t *testing.T) {
			first := domain.NormalizedRelease{
				Provider: "patreon", ProviderReleaseID: "1", Title: "Harbor Chapter 2",
				PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Attachments: []domain.Attachment{{ProviderMediaID: mediaID, FileName: "chapter.epub", MIMEType: "application/epub+zip", SHA256: "same-content", DownloadURL: "https://fixture.invalid/old", LocalPath: "/old/capture"}},
			}
			second := first
			second.ProviderReleaseID = "2"
			second.PublishedAt = first.PublishedAt.Add(time.Hour)
			second.Attachments = append([]domain.Attachment(nil), first.Attachments...)
			second.Attachments[0].ProviderMediaID = "replacement-media"
			second.Attachments[0].DownloadURL = "https://fixture.invalid/new"
			second.Attachments[0].LocalPath = "/new/capture"
			candidates := discovery.Detect("fictional", []domain.NormalizedRelease{first, second}, &config.Config{}, nil, second.PublishedAt)
			repost := false
			for _, candidate := range candidates {
				for _, evidence := range candidate.Evidence {
					repost = repost || evidence.Kind == "repost" && evidence.Value == "1"
				}
			}
			if !repost {
				t.Fatalf("attachment provenance suppressed repost evidence: %+v", candidates)
			}
			if first.Attachments[0].ProviderMediaID != mediaID || second.Attachments[0].ProviderMediaID != "replacement-media" {
				t.Fatal("discovery changed source attachment provenance")
			}
			second.Attachments[0].SHA256 = "corrected-content"
			candidates = discovery.Detect("fictional", []domain.NormalizedRelease{first, second}, &config.Config{}, nil, second.PublishedAt)
			for _, candidate := range candidates {
				for _, evidence := range candidate.Evidence {
					if evidence.Kind == "repost" {
						t.Fatal("changed attachment bytes classified as a repost")
					}
				}
			}
		})
	}
}
