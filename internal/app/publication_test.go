package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"

	"github.com/prateek/serial-sync/internal/app"
	"github.com/prateek/serial-sync/internal/config"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/provider"
)

func TestMetadataChangesNeedRebuildAndDoNotRepublishUnchangedEditions(t *testing.T) {
	for _, bundling := range []string{"none", "volume"} {
		t.Run(bundling, func(t *testing.T) {
			s, _ := newReaderService(t)
			s.Config.Series[0].Output = config.SeriesOutputConfig{Format: "epub", Bundling: bundling, ChaptersPerVolume: 2}
			s.Config.Series[0].Metadata.Description = "First description"
			if _, err := s.RunOnce(context.Background(), "", "", "run"); err != nil {
				t.Fatal(err)
			}
			paths := findFiles(t, s.Config.Publishers[0].Path, ".epub")
			before := map[string][]byte{}
			for _, path := range paths {
				before[path] = mustReadFile(t, path)
			}
			s.Config.Series[0].Metadata.Description = "Revised description"
			if result, err := s.RunOnce(context.Background(), "", "", "metadata refresh"); err != nil || result.Publish.Published != 0 {
				t.Fatalf("metadata refresh republished: %+v %v", result, err)
			}
			for path, data := range before {
				if !bytes.Equal(data, mustReadFile(t, path)) {
					t.Fatal("ordinary metadata refresh changed a reading copy")
				}
			}
			preview, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview")
			if err != nil {
				t.Fatal(err)
			}
			changed := false
			for _, item := range preview.Plans {
				changed = changed || item.Action == "rebuild"
			}
			if !changed {
				t.Fatal("metadata-only edit missing from preview")
			}
			for path, data := range before {
				if !bytes.Equal(data, mustReadFile(t, path)) {
					t.Fatal("preview changed a reading copy")
				}
			}
			if _, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "rebuild"); err != nil {
				t.Fatal(err)
			}
			for _, path := range findFiles(t, s.Config.Publishers[0].Path, ".epub") {
				if !bytes.Contains(epubEntry(t, path, ".opf"), []byte("Revised description")) {
					t.Fatalf("rebuild omitted metadata: %s", path)
				}
			}
			if result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "unchanged"); err != nil || result.Publish.Published != 0 {
				t.Fatalf("unchanged rebuild republished: %+v %v", result, err)
			}
		})
	}
}

func TestReleaseIdentityPolicyRequiresRebuildAndStaysStable(t *testing.T) {
	s, upstream := newReaderService(t)
	s.Config.Series[0].Output.Format = "epub"
	s.Config.Series[0].Inputs[0].ContentStrategy = "attachment_only"
	upstream.docs["alpha"] = upstream.docs["alpha"][:2]
	for i := range upstream.docs["alpha"] {
		file := filepath.Join(t.TempDir(), "chapter.epub")
		writePublicationIdentityFixture(t, file)
		upstream.docs["alpha"][i].Normalized.Attachments = []domain.Attachment{{FileName: "chapter.epub", MIMEType: "application/epub+zip", LocalPath: file}}
	}
	if _, err := s.RunOnce(context.Background(), "", "", "initial"); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, file := range findFiles(t, s.Config.Publishers[0].Path, ".epub") {
		before[file] = mustReadFile(t, file)
		if !bytes.Contains(epubEntry(t, file, ".opf"), []byte(">Unknown</dc:title>")) {
			t.Fatal("fixture did not preserve embedded default identity")
		}
	}
	if len(before) != 2 {
		t.Fatalf("initial published chapters = %d, want 2", len(before))
	}
	s.Config.Series[0].Metadata.IdentitySource = "embedded"
	if result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "explicit default"); err != nil || result.Publish.Published != 0 {
		t.Fatalf("explicit embedded policy changed default editions: %+v %v", result, err)
	}
	s.Config.Series[0].Metadata.IdentitySource = "release"
	if result, err := s.RunOnce(context.Background(), "", "", "normal refresh"); err != nil || result.Publish.Published != 0 {
		t.Fatalf("normal refresh adopted identity override: %+v %v", result, err)
	}
	s.Providers = provider.NewRegistry()
	preview, err := s.Rebuild(context.Background(), app.RebuildOptions{DryRun: true}, "preview identity repair")
	if err != nil {
		t.Fatal(err)
	}
	rebuilds := 0
	for _, plan := range preview.Plans {
		if plan.Action == "rebuild" {
			rebuilds++
		}
	}
	if rebuilds != 2 {
		t.Fatalf("identity repair preview rebuilt %d chapters, want 2", rebuilds)
	}
	for file, content := range before {
		if !bytes.Equal(content, mustReadFile(t, file)) {
			t.Fatal("refresh or preview changed a reading copy")
		}
	}
	if result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "apply identity repair"); err != nil || result.Publish.Published != 2 {
		t.Fatalf("rebuild did not publish two corrected chapters: %+v %v", result, err)
	}
	titles := map[string]bool{}
	for file := range before {
		var pkg struct {
			Metadata struct {
				Title   string `xml:"title"`
				Creator string `xml:"creator"`
			} `xml:"metadata"`
		}
		if err := xml.Unmarshal(epubEntry(t, file, ".opf"), &pkg); err != nil {
			t.Fatal(err)
		}
		titles[pkg.Metadata.Title] = true
		if pkg.Metadata.Creator != "Alpha Author" {
			t.Fatalf("wrong canonical author: %q", pkg.Metadata.Creator)
		}
		if !bytes.Contains(epubEntry(t, file, "chapter.xhtml"), []byte("Unchanged chapter body.")) {
			t.Fatal("chapter content changed")
		}
	}
	if !titles["Alpha Saga - Chapter 1"] || !titles["Alpha Saga - Chapter 2"] || len(titles) != 2 {
		t.Fatalf("release titles collapsed: %v", titles)
	}
	candidates, err := s.Repo.ListPublishCandidates(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		var saved struct {
			Decision domain.TrackDecision `json:"decision"`
		}
		if err := json.Unmarshal(mustReadFile(t, candidate.Artifact.MetadataRef), &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Decision.Publication == nil || saved.Decision.Publication.IdentitySource != domain.PublicationIdentityRelease {
			t.Fatal("artifact recipe did not retain the selected identity policy")
		}
	}
	if result, err := s.Rebuild(context.Background(), app.RebuildOptions{}, "repeat repair"); err != nil || result.Publish.Published != 0 {
		t.Fatalf("unchanged identity repair republished: %+v %v", result, err)
	}
}

func writePublicationIdentityFixture(t *testing.T, file string) {
	t.Helper()
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	files := []struct{ name, content string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"content.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:test:identity-rebuild</dc:identifier><dc:title>Unknown</dc:title><dc:creator>Wrong Author</dc:creator><dc:language>en</dc:language><meta property="dcterms:modified">2026-04-01T00:00:00Z</meta></metadata><manifest><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest><spine><itemref idref="chapter"/></spine></package>`},
		{"chapter.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>Unchanged chapter body.</p></body></html>`},
		{"nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`},
	}
	for _, entry := range files {
		writer, err := z.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}
