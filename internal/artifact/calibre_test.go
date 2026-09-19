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
)

func TestConvertPDFToEPUBProducesStableEPUBCheckValidOutput(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("ebook-convert"); err != nil {
		t.Fatalf("ebook-convert is required for PDF to EPUB integration validation: %v", err)
	}
	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}

	modified := time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC)
	identifier := "urn:uuid:11111111-1111-1111-1111-111111111111"
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	externalToolMu.Lock()
	first, firstErr := convertPDFToEPUB(ctx, minimalPDF(), "PDF Series", "Author Name", identifier, modified)
	second, secondErr := convertPDFToEPUB(ctx, minimalPDF(), "PDF Series", "Author Name", identifier, modified)
	externalToolMu.Unlock()
	if firstErr != nil {
		t.Fatalf("convertPDFToEPUB first: %v", firstErr)
	}
	if secondErr != nil {
		t.Fatalf("convertPDFToEPUB second: %v", secondErr)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("PDF conversion should be stable for identical input")
	}
	files := unzipEntries(t, first)
	opf := string(files["content.opf"])
	if !strings.Contains(opf, identifier) {
		t.Fatalf("converted OPF missing stable identifier:\n%s", opf)
	}
	if !strings.Contains(opf, "2026-05-06T12:34:56Z") {
		t.Fatalf("converted OPF missing stable modified timestamp:\n%s", opf)
	}
	if strings.Contains(string(files["index.html"]), "serial-sync-calibre") || strings.Contains(string(files["index.html"]), "input.pdf") {
		t.Fatalf("converted XHTML should not keep Calibre temp paths:\n%s", string(files["index.html"]))
	}
	path := filepath.Join(t.TempDir(), "converted.epub")
	if err := os.WriteFile(path, first, 0o644); err != nil {
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
