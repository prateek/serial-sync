package artifact

import (
	"context"
	"testing"

	"github.com/prateek/serial-sync/internal/domain"
)

func TestSelectedAttachmentRequiresCapturedReference(t *testing.T) {
	attachment := domain.Attachment{FileName: "chapter.pdf", SHA256: "captured-hash", LocalPath: "/capture/chapter.pdf"}
	for _, tc := range []struct {
		name        string
		attachments []domain.Attachment
		ref         domain.ContentReference
		want        string
	}{
		{"valid", []domain.Attachment{attachment}, domain.ContentReference{Kind: "attachment", FileName: "chapter.pdf", SHA256: "captured-hash"}, ""},
		{"absent", nil, domain.ContentReference{Kind: "attachment", FileName: "chapter.pdf"}, "selected attachment no longer matches the captured release"},
		{"index mismatch", []domain.Attachment{attachment}, domain.ContentReference{Kind: "attachment", AttachmentIndex: 1, FileName: "chapter.pdf"}, "selected attachment no longer matches the captured release"},
		{"filename mismatch", []domain.Attachment{attachment}, domain.ContentReference{Kind: "attachment", FileName: "other.pdf"}, "selected attachment no longer matches the captured release"},
		{"digest mismatch", []domain.Attachment{attachment}, domain.ContentReference{Kind: "attachment", FileName: "chapter.pdf", SHA256: "other-hash"}, "selected attachment no longer matches the captured release"},
		{"missing local path", []domain.Attachment{{FileName: "chapter.pdf"}}, domain.ContentReference{Kind: "attachment", FileName: "chapter.pdf"}, "selected attachment \"chapter.pdf\" is missing a local_path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelectedAttachment(domain.NormalizedRelease{Attachments: tc.attachments}, domain.TrackDecision{SelectedContent: &tc.ref})
			if tc.want != "" {
				if err == nil || err.Error() != tc.want {
					t.Fatalf("attachment error = %v, want %q", err, tc.want)
				}
			} else if err != nil || got != attachment {
				t.Fatalf("selected attachment = %+v, %v", got, err)
			}
		})
	}
}

func TestPlanRequiresSelectedAttachmentLocalPath(t *testing.T) {
	normalized := domain.NormalizedRelease{Attachments: []domain.Attachment{{FileName: "chapter.pdf", DownloadURL: "https://example.invalid/chapter.pdf"}}}
	decision := domain.TrackDecision{ContentStrategy: domain.ContentStrategyAttachmentOnly, SelectedContent: &domain.ContentReference{Kind: "attachment", FileName: "chapter.pdf"}}
	_, err := New(t.TempDir()).Plan(context.Background(), domain.Source{}, domain.StoryTrack{}, domain.Release{}, normalized, decision, nil)
	if err == nil || err.Error() != "selected attachment \"chapter.pdf\" is missing a local_path" {
		t.Fatalf("materialization did not enforce captured selection: %v", err)
	}
}
