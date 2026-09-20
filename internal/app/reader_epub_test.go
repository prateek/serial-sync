package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
)

func TestVolumePreservesEscapedAttachmentResources(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub"}
	s.Config.Rules = config.CompileSeriesRules(s.Config.Series)
	upstream.docs["alpha"] = upstream.docs["alpha"][:1]
	upstream.docs["alpha"][0].Normalized.TextHTML = "<p>A chapter with an escaped resource name.</p>"
	if _, err := s.RunOnce(context.Background(), "", "", "generate chapter"); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(findFiles(t, s.Config.Publishers[0].Path, ".epub")[0])
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, file := range reader.File {
		input, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		header := zip.FileHeader{Name: file.Name, Method: file.Method}
		if header.Name == "OEBPS/chapter-001.xhtml" {
			header.Name = "OEBPS/chapter 001.xhtml"
		}
		if strings.HasSuffix(header.Name, ".opf") || strings.HasSuffix(header.Name, "nav.xhtml") {
			data = bytes.ReplaceAll(data, []byte("chapter-001.xhtml"), []byte("chapter%20001.xhtml"))
		}
		output, err := writer.CreateHeader(&header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := output.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	attachment := filepath.Join(t.TempDir(), "chapter.epub")
	if err := os.WriteFile(attachment, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	target, source := newReaderService(t)
	target.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub"}
	target.Config.Series[0].Inputs[0].ContentStrategy = "attachment_only"
	target.Config.Series[0].Inputs[0].AttachmentGlob = []string{"*.epub"}
	target.Config.Rules = config.CompileSeriesRules(target.Config.Series)
	source.docs["alpha"] = source.docs["alpha"][:1]
	source.docs["alpha"][0].Normalized.Attachments = []domain.Attachment{{FileName: "chapter.epub", MIMEType: "application/epub+zip", LocalPath: attachment}}
	if _, err := target.RunOnce(context.Background(), "", "", "validate and publish attachment"); err != nil {
		t.Fatal(err)
	}
	target.Config.Series[0].Output.Bundling = "volume"
	target.Config.Series[0].Output.ChaptersPerVolume = 1
	target.Config.Rules = config.CompileSeriesRules(target.Config.Series)
	if result, err := target.Publish(context.Background(), "", "", false, "bundle attachment"); err != nil {
		t.Fatalf("bundle attachment: %+v %v", result, err)
	}
	files := findFiles(t, target.Config.Publishers[0].Path, ".epub")
	if len(files) != 1 || filepath.Base(files[0]) != "alpha-saga-vol01.epub" {
		t.Fatalf("attachment was not replaced by a volume: %v", files)
	}
	if body := string(epubEntry(t, files[0], "chapter 001.xhtml")); !strings.Contains(body, "A chapter with an escaped resource name.") {
		t.Fatalf("volume lost the chapter content: %s", body)
	}
	if nav := string(epubEntry(t, files[0], "nav.xhtml")); !strings.Contains(nav, "chapter%20001.xhtml") {
		t.Fatalf("volume navigation lost the escaped resource link: %s", nav)
	}
}
