package artifact

import (
	"archive/zip"
	"bytes"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/domain"
)

func TestApplyOutputProfileWrapsEPUBWithPreface(t *testing.T) {
	t.Parallel()

	original, err := buildSimpleEPUB("The Sixth School", "BlaQQuill", []epubChapter{{
		FileName: "chapter-001.xhtml",
		Title:    "Chapter 58",
		BodyHTML: "<p>chapter body</p>",
	}})
	if err != nil {
		t.Fatalf("buildSimpleEPUB: %v", err)
	}

	content, fileName, mimeType, err := applyOutputProfile(
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

func TestApplyOutputProfileBuildsEPUBFromHTML(t *testing.T) {
	t.Parallel()

	content, fileName, mimeType, err := applyOutputProfile(
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

	files := unzipEntries(t, content)
	if _, ok := files["OEBPS/content.opf"]; !ok {
		t.Fatalf("generated epub missing content.opf")
	}
	if _, ok := files["OEBPS/chapter-001.xhtml"]; !ok {
		t.Fatalf("generated epub missing chapter xhtml")
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
