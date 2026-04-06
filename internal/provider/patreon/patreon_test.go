package patreon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePostUsesSanitizedFixtureAttachmentPath(t *testing.T) {
	t.Parallel()

	fixtureDir := t.TempDir()
	raw := []byte(`{
	  "data": {
	    "id": "123",
	    "type": "post",
	    "attributes": {
	      "title": "Test Chapter",
	      "post_type": "text_only",
	      "current_user_can_view": true,
	      "url": "https://www.patreon.com/posts/test-chapter-123",
	      "content": "<p>Hello</p>",
	      "published_at": "2026-04-01T00:00:00Z"
	    },
	    "relationships": {
	      "attachments_media": {
	        "data": [{"id": "m1"}]
	      }
	    }
	  },
	  "included": [{
	    "id": "m1",
	    "type": "media",
	    "attributes": {
	      "file_name": "book 1/chapter:1.epub",
	      "mimetype": "application/epub+zip",
	      "download_url": "https://example.invalid/chapter.epub"
	    }
	  }]
	}`)
	localPath := fixtureAttachmentPath(fixtureDir, "123", "book 1/chapter:1.epub")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, []byte("epub bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	norm, err := parsePost(raw, fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(norm.Attachments), 1; got != want {
		t.Fatalf("len(norm.Attachments) = %d, want %d", got, want)
	}
	if got, want := norm.Attachments[0].LocalPath, localPath; got != want {
		t.Fatalf("attachment LocalPath = %q, want %q", got, want)
	}
}
