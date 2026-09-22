package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

// Direct tests for the artifact module's reading-copy description and metadata
// steps: no store, provider or EPUB checker is involved.

// The describe-only preview and the materializing Plan must agree on the
// reading copy's file name and type for the same inputs. The PDF row needs
// Calibre and runs only in the container.
func TestReadingCopyDescribeAgreesWithMaterialize(t *testing.T) {
	track := domain.StoryTrack{TrackKey: "harbor", TrackName: "Harbor", CanonicalAuthor: "Test Author"}
	release := domain.Release{ID: "rel_1", SourceID: "fictional", ProviderReleaseID: "r1", Title: "Harbor Chapter 1"}
	seq := domain.Sequence{Chapter: 1, Position: 1}
	epubBytes := func(t *testing.T) []byte {
		t.Helper()
		content, err := buildSimpleEPUB("Harbor", "Test Author", "urn:uuid:11111111-1111-1111-1111-111111111111", time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), []epubChapter{{FileName: "chapter-001.xhtml", Title: "Harbor Chapter 1", BodyHTML: "<p>body</p>"}})
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	write := func(t *testing.T, name string, data []byte) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	textPost := domain.NormalizedRelease{ProviderReleaseID: "r1", Title: "Harbor Chapter 1", TextPlain: "body"}
	epubAttachment := func(path string) domain.NormalizedRelease {
		return domain.NormalizedRelease{ProviderReleaseID: "r1", Title: "Harbor Chapter 1", TextPlain: "body", Attachments: []domain.Attachment{{FileName: "chapter.epub", MIMEType: "application/epub+zip", LocalPath: path}}}
	}
	pdfAttachment := func(path string) domain.NormalizedRelease {
		return domain.NormalizedRelease{ProviderReleaseID: "r1", Title: "Harbor Chapter 1", TextPlain: "body", Attachments: []domain.Attachment{{FileName: "chapter.pdf", MIMEType: "application/pdf", LocalPath: path}}}
	}
	base := domain.TrackDecision{SeriesID: "harbor", TrackKey: "harbor", TrackName: "Harbor", CanonicalAuthor: "Test Author", Matched: true, Sequence: &seq}
	strategy := func(decision domain.TrackDecision, format domain.OutputFormat, contentStrategy domain.ContentStrategy, preface domain.PrefaceMode) domain.TrackDecision {
		decision.OutputFormat = format
		decision.ContentStrategy = contentStrategy
		decision.PrefaceMode = preface
		return decision
	}

	for _, tc := range []struct {
		name         string
		normalized   domain.NormalizedRelease
		decision     domain.TrackDecision
		needsCalibre bool
	}{
		{name: "text_post preserve", normalized: textPost, decision: strategy(base, domain.OutputFormatPreserve, domain.ContentStrategyTextPost, "")},
		{name: "text_post epub", normalized: textPost, decision: strategy(base, domain.OutputFormatEPUB, domain.ContentStrategyTextPost, "")},
		{name: "epub attachment preserve", normalized: epubAttachment(""), decision: strategy(base, domain.OutputFormatPreserve, domain.ContentStrategyAttachmentOnly, "")},
		{name: "epub attachment epub", normalized: epubAttachment(""), decision: strategy(base, domain.OutputFormatEPUB, domain.ContentStrategyAttachmentOnly, "")},
		{name: "epub attachment epub with preface", normalized: epubAttachment(""), decision: strategy(base, domain.OutputFormatEPUB, domain.ContentStrategyAttachmentOnly, domain.PrefaceModePrependPost)},
		{name: "pdf attachment epub", normalized: pdfAttachment("../../testdata/fixtures/reader/chapter.pdf"), decision: strategy(base, domain.OutputFormatEPUB, domain.ContentStrategyAttachmentOnly, ""), needsCalibre: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needsCalibre {
				if _, err := exec.LookPath("ebook-convert"); err != nil {
					t.Skip("Calibre-backed conversion runs in the container")
				}
			}
			normalized := tc.normalized
			for i := range normalized.Attachments {
				if normalized.Attachments[i].LocalPath == "" {
					data := epubBytes(t)
					name := "chapter.epub"
					normalized.Attachments[i].LocalPath = write(t, name, data)
				}
			}
			described := Describe(track, release, normalized, tc.decision)
			m := New(t.TempDir())
			planned, err := m.Plan(context.Background(), domain.Source{ID: "fictional"}, track, release, normalized, tc.decision, nil)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if planned.Filename != described.FileName {
				t.Fatalf("describe/materialize name mismatch: describe=%q plan=%q", described.FileName, planned.Filename)
			}
			if planned.MIMEType != described.MIMEType {
				t.Fatalf("describe/materialize type mismatch: describe=%q plan=%q", described.MIMEType, planned.MIMEType)
			}
		})
	}
}

func TestArchivedSequenceReadsTheSidecar(t *testing.T) {
	root := t.TempDir()
	seq := domain.Sequence{Chapter: 4, Position: 4, BookID: "arrival"}
	meta := map[string]any{
		"output_version": 2,
		"decision":       domain.TrackDecision{SeriesID: "harbor", Sequence: &seq, TrackKey: "harbor", Matched: true},
		"track":          domain.StoryTrack{TrackKey: "harbor"},
		"release":        domain.Release{ID: "rel_1"},
	}
	ref := filepath.Join(root, "meta.json")
	payload, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(ref, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	stored := domain.Artifact{ID: "art_1", MetadataRef: ref}
	if got := ArchivedSequence(stored); got == nil || got.Chapter != 4 || got.BookID != "arrival" {
		t.Fatalf("sidecar sequence lost: %+v", got)
	}
	if !IsCurrent(stored, domain.Release{ID: "rel_1"}, domain.StoryTrack{TrackKey: "harbor"}, domain.TrackDecision{SeriesID: "harbor", TrackKey: "harbor", Sequence: &seq, Matched: true}) {
		t.Fatalf("current artifact not recognized: %+v", stored)
	}
	if IsCurrent(stored, domain.Release{ID: "rel_2"}, domain.StoryTrack{TrackKey: "harbor"}, domain.TrackDecision{SeriesID: "harbor", TrackKey: "harbor", Matched: true}) {
		t.Fatalf("changed release still current")
	}
	stored2 := stored
	stored2.MetadataRef = filepath.Join(root, "absent.json")
	if !IsLegacy(stored2) || ArchivedSequence(stored2) != nil {
		t.Fatalf("missing sidecar not treated as legacy")
	}
}

func TestWithPublicationMetadataRewritesSeriesPosition(t *testing.T) {
	content, err := minimalEPUB()
	if err != nil {
		t.Fatal(err)
	}
	meta := publicationMetadata{
		Title:       "Harbor — Volume 1 (Chapters 1–2)",
		Author:      "Test Author",
		Series:      "Harbor",
		Position:    42,
		PublishedAt: time.Now().UTC(),
	}
	out, err := withPublicationMetadata(content, meta)
	if err != nil {
		t.Fatal(err)
	}
	opf := readArchiveEntry(out, ".opf")
	if !strings.Contains(opf, "Harbor") || !strings.Contains(opf, `calibre:series`) || !strings.Contains(opf, "42") || !strings.Contains(opf, "Test Author") {
		t.Fatalf("metadata not embedded: %s", opf)
	}
}

func minimalEPUB() ([]byte, error) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	files := map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OEBPS/content.opf":      `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:test:1</dc:identifier><dc:title>Harbor</dc:title><dc:language>en</dc:language><meta property="dcterms:modified">2026-01-01T00:00:00Z</meta></metadata><manifest><item id="c1" href="chapter.xhtml" media-type="application/xhtml+xml"/><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest><spine><itemref idref="c1"/></spine></package>`,
		"OEBPS/chapter.xhtml":    `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>Chapter</p></body></html>`,
		"OEBPS/nav.xhtml":        `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/epub/vocab/structure/"><head><title>Harbor</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`,
	}
	for _, name := range mapKeys(files) {
		header := zip.FileHeader{Name: name}
		output, err := writer.CreateHeader(&header)
		if err != nil {
			return nil, err
		}
		if _, err := output.Write([]byte(files[name])); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return archive.Bytes(), nil
}

func readArchiveEntry(content []byte, suffix string) string {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return ""
	}

	for _, file := range reader.File {
		if !strings.HasSuffix(file.Name, suffix) {
			continue
		}
		r, err := file.Open()
		if err != nil {
			return ""
		}
		data, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			return ""
		}
		return string(data)
	}
	return ""
}

func mapKeys(values map[string]string) []string {
	var keys []string
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
