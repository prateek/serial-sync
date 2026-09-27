package artifact

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

func TestPDFAttachmentBecomesAStableEPUBCheckValidReadingCopy(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("pdftohtml"); err != nil {
		t.Fatalf("pdftohtml (poppler-utils) is required for PDF to EPUB integration validation: %v", err)
	}
	if _, err := exec.LookPath(epubcheckCommand); err != nil {
		t.Fatalf("%s is required for EPUB integration validation: %v", epubcheckCommand, err)
	}

	seq := domain.Sequence{Chapter: 1, Position: 1}
	track := domain.StoryTrack{TrackKey: "pdf-series", TrackName: "PDF Series", CanonicalAuthor: "Author Name"}
	release := domain.Release{ID: "rel_pdf", ProviderReleaseID: "r1", Title: "PDF Series Chapter 1"}
	normalized := domain.NormalizedRelease{ProviderReleaseID: "r1", Title: release.Title, TextHTML: "<p>Author note.</p>"}
	decision := domain.TrackDecision{
		SeriesID: "pdf-series", TrackKey: "pdf-series", TrackName: "PDF Series", Matched: true, Sequence: &seq,
		OutputFormat: domain.OutputFormatEPUB, ContentStrategy: domain.ContentStrategyAttachmentOnly, PrefaceMode: domain.PrefaceModePrependPost,
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	first := applyOutputProfile(ctx, track, release, normalized, decision, minimalPDF(), "chapter.pdf", "application/pdf", true)
	second := applyOutputProfile(ctx, track, release, normalized, decision, minimalPDF(), "chapter.pdf", "application/pdf", true)
	if first.Err != nil || second.Err != nil {
		t.Fatalf("applyOutputProfile: %v / %v", first.Err, second.Err)
	}
	if !bytes.Equal(first.Content, second.Content) {
		t.Fatalf("PDF conversion should be stable for identical input")
	}
	files := unzipEntries(t, first.Content)
	chapter := string(files["OEBPS/chapter-001.xhtml"])
	if !strings.Contains(chapter, "<p>Hello from serial-sync</p>") {
		t.Fatalf("converted chapter missing the PDF text:\n%s", chapter)
	}
	if !strings.Contains(string(files["OEBPS/preface.xhtml"]), "Author note.") {
		t.Fatalf("converted EPUB missing the post preface")
	}
	path := filepath.Join(t.TempDir(), "converted.epub")
	if err := os.WriteFile(path, first.Content, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func minimalPDF() []byte {
	var out strings.Builder
	offsets := []int{}
	write := func(value string) {
		out.WriteString(value)
	}
	write("%PDF-1.4\n")
	for _, object := range []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n",
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 144] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>\nendobj\n",
		"4 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n",
		pdfStreamObject("BT /F1 18 Tf 72 72 Td (Hello from serial-sync) Tj ET"),
	} {
		offsets = append(offsets, out.Len())
		write(object)
	}
	xref := out.Len()
	write("xref\n0 6\n")
	write("0000000000 65535 f \n")
	for _, offset := range offsets {
		write(fmt.Sprintf("%010d 00000 n \n", offset))
	}
	write("trailer\n<< /Size 6 /Root 1 0 R >>\n")
	write(fmt.Sprintf("startxref\n%d\n%%EOF\n", xref))
	return []byte(out.String())
}

func pdfStreamObject(stream string) string {
	return fmt.Sprintf("5 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(stream), stream)
}
