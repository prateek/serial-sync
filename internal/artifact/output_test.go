package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

func TestApplyOutputProfileWrapsEPUBWithPreface(t *testing.T) {
	t.Parallel()

	original, err := buildSimpleEPUB("The Sixth School", "BlaQQuill", "urn:uuid:44444444-4444-4444-4444-444444444444", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), []epubChapter{{
		FileName: "chapter-001.xhtml",
		Title:    "Chapter 58",
		BodyHTML: "<p>chapter body</p>",
	}})
	if err != nil {
		t.Fatalf("buildSimpleEPUB: %v", err)
	}

	content, fileName, mimeType, validateEPUBCheck, err := applyOutputProfile(
		context.Background(),
		domain.StoryTrack{TrackName: "The Sixth School", CanonicalAuthor: "BlaQQuill"},
		domain.Release{Title: "Book Two Chapter 058"},
		domain.NormalizedRelease{
			Title:       "The Sixth School. Book Two. Chapter 058.",
			CreatorName: "BlaQQuill",
			TextHTML:    "<p>Author note before the chapter.</p>",
		},
		domain.TrackDecision{
			OutputFormat: domain.OutputFormatPreserve,
			PrefaceMode:  domain.PrefaceModePrependPost,
		},
		original,
		"chapter-058.epub",
		"application/epub+zip",
		true,
	)
	if err != nil {
		t.Fatalf("applyOutputProfile: %v", err)
	}
	if got, want := fileName, "chapter-058.epub"; got != want {
		t.Fatalf("fileName = %q, want %q", got, want)
	}
	if got, want := mimeType, "application/epub+zip"; got != want {
		t.Fatalf("mimeType = %q, want %q", got, want)
	}
	if !validateEPUBCheck {
		t.Fatalf("wrapped EPUB output should require EPUBCheck validation")
	}

	files := unzipEntries(t, content)
	preface, ok := files["OEBPS/serial-sync-preface.xhtml"]
	if !ok {
		t.Fatalf("preface entry missing; entries=%v", archiveKeys(files))
	}
	if !strings.Contains(string(preface), "Author note before the chapter.") {
		t.Fatalf("preface does not contain rendered post body: %q", string(preface))
	}
	if !strings.Contains(string(files["OEBPS/content.opf"]), "serial-sync-preface") {
		t.Fatalf("opf does not reference injected preface")
	}
}

func TestApplyOutputProfileWrapsEPUB2PrefacePassesEPUBCheck(t *testing.T) {
	t.Parallel()

	original := buildEPUB2Fixture(t)
	content, fileName, mimeType, validateEPUBCheck, err := applyOutputProfile(
		context.Background(),
		domain.StoryTrack{TrackName: "Wrapped Book", CanonicalAuthor: "Author Name"},
		domain.Release{
			SourceID:          "source",
			ProviderReleaseID: "12345",
			Title:             "Chapter 1",
		},
		domain.NormalizedRelease{
			Title:       "Chapter 1",
			CreatorName: "Author Name",
			TextHTML:    `<details data-x="1" aria-label="Note"><summary>Author note</summary><p><mark>Before the chapter.</mark></p></details>`,
		},
		domain.TrackDecision{
			OutputFormat: domain.OutputFormatPreserve,
			PrefaceMode:  domain.PrefaceModePrependPost,
		},
		original,
		"chapter-1.epub",
		"application/epub+zip",
		true,
	)
	if err != nil {
		t.Fatalf("applyOutputProfile: %v", err)
	}
	if got, want := fileName, "chapter-1.epub"; got != want {
		t.Fatalf("fileName = %q, want %q", got, want)
	}
	if got, want := mimeType, "application/epub+zip"; got != want {
		t.Fatalf("mimeType = %q, want %q", got, want)
	}
	if !validateEPUBCheck {
		t.Fatalf("wrapped EPUB output should require EPUBCheck validation")
	}
	path := filepath.Join(t.TempDir(), "wrapped-epub2.epub")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestApplyOutputProfileBuildsEPUBFromHTML(t *testing.T) {
	t.Parallel()

	content, fileName, mimeType, validateEPUBCheck, err := applyOutputProfile(
		context.Background(),
		domain.StoryTrack{TrackName: "Main Series", CanonicalAuthor: "Author Name"},
		domain.Release{Title: "Chapter 1"},
		domain.NormalizedRelease{
			Title:       "Chapter 1",
			CreatorName: "Author Name",
		},
		domain.TrackDecision{
			OutputFormat: domain.OutputFormatEPUB,
		},
		[]byte("<!doctype html><html><body><p>hello world</p></body></html>"),
		"chapter-1.html",
		"text/html",
		false,
	)
	if err != nil {
		t.Fatalf("applyOutputProfile: %v", err)
	}
	if got, want := fileName, "chapter-1.epub"; got != want {
		t.Fatalf("fileName = %q, want %q", got, want)
	}
	if got, want := mimeType, "application/epub+zip"; got != want {
		t.Fatalf("mimeType = %q, want %q", got, want)
	}
	if !validateEPUBCheck {
		t.Fatalf("generated EPUB output should require EPUBCheck validation")
	}

	files := unzipEntries(t, content)
	if _, ok := files["OEBPS/content.opf"]; !ok {
		t.Fatalf("generated epub missing content.opf")
	}
	if _, ok := files["OEBPS/chapter-001.xhtml"]; !ok {
		t.Fatalf("generated epub missing chapter xhtml")
	}
}

func TestApplyOutputProfileAcceptsMultiIdentifierEPUBWithoutPreface(t *testing.T) {
	t.Parallel()

	original := buildMultiIdentifierEPUB3Fixture(t)
	content, fileName, mimeType, validateEPUBCheck, err := applyOutputProfile(
		context.Background(),
		domain.StoryTrack{TrackName: "Main Series", CanonicalAuthor: "Author Name"},
		domain.Release{
			SourceID:          "source",
			ProviderReleaseID: "12345",
			Title:             "Chapter 1",
		},
		domain.NormalizedRelease{
			Title:       "Chapter 1",
			CreatorName: "Author Name",
		},
		domain.TrackDecision{
			OutputFormat: domain.OutputFormatEPUB,
		},
		original,
		"chapter-1.epub",
		"application/epub+zip",
		false,
	)
	if err != nil {
		t.Fatalf("applyOutputProfile: %v", err)
	}
	if !bytes.Equal(content, original) {
		t.Fatalf("no-preface EPUB path should preserve the original archive")
	}
	if got, want := fileName, "chapter-1.epub"; got != want {
		t.Fatalf("fileName = %q, want %q", got, want)
	}
	if got, want := mimeType, "application/epub+zip"; got != want {
		t.Fatalf("mimeType = %q, want %q", got, want)
	}
	if validateEPUBCheck {
		t.Fatalf("no-preface source EPUB pass-through should not require EPUBCheck validation")
	}
}

func TestMaterializeRejectsEPUBCheckInvalidOutput(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	root := t.TempDir()
	materializer := New(root)
	plan := domain.ArtifactPlan{
		ArtifactKind:      "epub",
		Filename:          "chapter.epub",
		MIMEType:          "application/epub+zip",
		SHA256:            "invalid",
		ValidateEPUBCheck: true,
		SelectedContent:   buildInvalidXHTMLContentModelFixture(t),
	}
	_, err := materializer.Materialize(
		context.Background(),
		domain.Source{ID: "source"},
		domain.StoryTrack{ID: "track", TrackKey: "series"},
		domain.Release{ID: "release", ProviderReleaseID: "post"},
		plan,
	)
	if err == nil || !strings.Contains(err.Error(), "epubcheck validation failed") {
		t.Fatalf("Materialize error = %v, want EPUBCheck failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "source", "series", "post", "chapter.epub")); !os.IsNotExist(statErr) {
		t.Fatalf("invalid EPUB should not be written, stat error = %v", statErr)
	}
}

func TestMaterializePreservesEPUBWhenEPUBCheckFlagIsFalse(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	materializer := New(root)
	content := buildInvalidXHTMLContentModelFixture(t)
	plan := domain.ArtifactPlan{
		ArtifactKind:    "epub",
		Filename:        "chapter.epub",
		MIMEType:        "application/epub+zip",
		SHA256:          "preserve",
		SelectedContent: content,
	}
	artifact, err := materializer.Materialize(
		context.Background(),
		domain.Source{ID: "source"},
		domain.StoryTrack{ID: "track", TrackKey: "series"},
		domain.Release{ID: "release", ProviderReleaseID: "post"},
		plan,
	)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	written, err := os.ReadFile(artifact.StorageRef)
	if err != nil {
		t.Fatalf("read written artifact: %v", err)
	}
	if !bytes.Equal(written, content) {
		t.Fatalf("preserve EPUB materialization should write original bytes")
	}
}

func TestEPUBIdentifierPrefersProviderIdentity(t *testing.T) {
	t.Parallel()

	first := epubIdentifierForRelease(domain.Release{
		ID:                "rel_local_one",
		SourceID:          "actus",
		ProviderReleaseID: "12345",
		URL:               "https://example.com/posts/12345",
	}, "Chapter 1")
	second := epubIdentifierForRelease(domain.Release{
		ID:                "rel_local_two",
		SourceID:          "actus",
		ProviderReleaseID: "12345",
		URL:               "https://example.com/posts/12345",
	}, "Chapter 1")
	if first != second {
		t.Fatalf("identifier should not depend on local release ID when provider identity is present: %q != %q", first, second)
	}

	otherSource := epubIdentifierForRelease(domain.Release{
		ID:                "rel_local_one",
		SourceID:          "other",
		ProviderReleaseID: "12345",
		URL:               "https://example.com/posts/12345",
	}, "Chapter 1")
	if first == otherSource {
		t.Fatalf("identifier should include source identity when provider IDs match across sources: %q", first)
	}
}

func TestEPUBIdentifierUsesLocalIDBeforeTitleFallback(t *testing.T) {
	t.Parallel()

	first := epubIdentifierForRelease(domain.Release{
		ID:    "rel_local_one",
		Title: "Chapter 1",
	}, "Chapter 1")
	second := epubIdentifierForRelease(domain.Release{
		ID:    "rel_local_two",
		Title: "Chapter 1",
	}, "Chapter 1")
	if first == second {
		t.Fatalf("identifier should use local release ID before title fallback when provider identity is absent: %q", first)
	}
}

func unzipEntries(t *testing.T, content []byte) map[string][]byte {
	t.Helper()

	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	files := make(map[string][]byte, len(reader.File))
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		files[file.Name] = data
	}
	return files
}

func archiveKeys(values map[string][]byte) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
