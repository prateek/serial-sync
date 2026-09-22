package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

func TestPortableMetadataPreservesStoryAndNavigation(t *testing.T) {
	for _, version := range []string{"2", "3"} {
		t.Run(version, func(t *testing.T) {
			original := buildEPUB2Fixture(t)
			if version == "3" {
				var err error
				original, err = buildSimpleEPUB("Original title", "Original author", "urn:test:portable", time.Unix(0, 0).UTC(), []epubChapter{{FileName: "one.xhtml", Title: "First", BodyHTML: "<p id=\"anchor\">Unchanged story.</p>"}, {FileName: "two.xhtml", Title: "Second", BodyHTML: "<p>More story.</p>"}})
				if err != nil {
					t.Fatal(err)
				}
			}
			asset := testMetadataAsset(t)
			metadata := publicationMetadata{Title: "Post title", Author: "Post author", Series: "Series", Position: 53, PreserveEmbedded: true, Publication: &domain.PublicationMetadata{Description: "A curated description.", DescriptionOverride: true, Language: "en", Cover: asset, Authors: []domain.AuthorProfile{{ID: "author", Name: "Original author", Biography: "Writes serials.", Portrait: asset, URL: "https://example.com/author"}}, Links: []string{"https://example.com/chapter"}}}
			content, err := withPublicationMetadata(original, metadata)
			if err != nil {
				t.Fatal(err)
			}
			before, beforePkg, packagePath, err := unpackEPUB(original)
			if err != nil {
				t.Fatal(err)
			}
			after, afterPkg, _, err := unpackEPUB(content)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range beforePkg.Manifest.Items {
				if strings.Contains(item.Properties, "nav") || item.MediaType == "application/x-dtbncx+xml" {
					continue
				}
				path, err := resolveManifestHref(filepath.Dir(packagePath), item.Href)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before[path], after[path]) {
					t.Fatalf("original resource changed: %s", path)
				}
			}
			if afterPkg.Metadata.Title != beforePkg.Metadata.Title || afterPkg.Metadata.Creator != beforePkg.Metadata.Creator {
				t.Fatal("original title or creator replaced")
			}
			if len(afterPkg.Spine.Itemrefs) != len(beforePkg.Spine.Itemrefs)+1 {
				t.Fatal("About was not appended exactly once")
			}
			if !strings.Contains(string(after[packagePath]), "A curated description.") {
				t.Fatal("description not embedded")
			}
			if err := validateEPUBArchive(content); err != nil {
				t.Fatal(err)
			}
			again, err := withPublicationMetadata(original, metadata)
			if err != nil || !bytes.Equal(content, again) {
				t.Fatalf("identical inputs not deterministic: %v", err)
			}
		})
	}
}

func TestMetadataCaptureKeepsSelectedImageAfterCuratedFileChanges(t *testing.T) {
	m := New(t.TempDir())
	asset := testMetadataAsset(t)
	metadata := &domain.PublicationMetadata{Cover: asset}
	snapshot, captures, err := m.snapshotPublication(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Materialize(context.Background(), domain.Source{ID: "source"}, domain.StoryTrack{TrackKey: "track"}, domain.Release{ProviderReleaseID: "post"}, domain.ArtifactPlan{Filename: "test.txt", SelectedContent: []byte("test"), SHA256: "test", MetadataAssets: captures}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset.Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.snapshotPublication(snapshot); err != nil {
		t.Fatalf("retained capture depends on mutable original: %v", err)
	}
}

func testMetadataAsset(t *testing.T) *domain.MetadataAsset {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data.Bytes())
	return &domain.MetadataAsset{Path: path, SHA256: hex.EncodeToString(hash[:]), MediaType: "image/png", SourceURL: "https://example.com/cover.png"}
}

func TestVolumeKeepsInternalChaptersAndOneFinalAbout(t *testing.T) {
	metadata := &domain.PublicationMetadata{IdentitySource: domain.PublicationIdentityRelease, Authors: []domain.AuthorProfile{{ID: "author", Name: "Author", Biography: "Biography."}}}
	original, err := buildSimpleEPUB("Two chapters", "Author", "urn:test:multi-chapter", time.Unix(0, 0).UTC(), []epubChapter{
		{FileName: "first.xhtml", Title: "First internal chapter", BodyHTML: "<p id=\"anchor\">First story.</p>"},
		{FileName: "second.xhtml", Title: "Second internal chapter", BodyHTML: "<p>Second story.</p>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	member, err := withPublicationMetadata(original, publicationMetadata{Title: "Two chapters", Author: "Author", Publication: metadata})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "member.epub")
	if err := os.WriteFile(file, member, 0600); err != nil {
		t.Fatal(err)
	}
	m := New(t.TempDir())
	volume := domain.VolumeEdition{ID: "volume", SeriesID: "series", GroupID: "book:one", Publication: metadata, Members: []domain.VolumeMember{{ReleaseID: "release", Position: 1}}, Artifact: domain.Artifact{Filename: "volume.epub"}}
	art, err := m.BuildVolume(context.Background(), volume, "Volume", []domain.PublishCandidate{{Release: domain.Release{ID: "release", Title: "Two chapters"}, Track: domain.StoryTrack{TrackName: "Series", CanonicalAuthor: "Author"}, Artifact: domain.Artifact{StorageRef: file}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(art.StorageRef)
	if err != nil {
		t.Fatal(err)
	}
	files, pkg, _, err := unpackEPUB(data)
	if err != nil {
		t.Fatal(err)
	}
	nav := string(files["OEBPS/nav.xhtml"])
	for _, title := range []string{"First internal chapter", "Second internal chapter", ">About<"} {
		if strings.Count(nav, title) != 1 {
			t.Fatalf("navigation lost or duplicated %q: %s", title, nav)
		}
	}
	if len(pkg.Spine.Itemrefs) != 4 {
		t.Fatalf("unexpected volume spine: %d", len(pkg.Spine.Itemrefs))
	}
	if pkg.Metadata.Title != "Volume" {
		t.Fatalf("release identity policy replaced intentional volume title: %q", pkg.Metadata.Title)
	}
	if !bytes.Contains(files["OEBPS/members/0001/OEBPS/first.xhtml"], []byte("id=\"anchor\"")) {
		t.Fatal("original story anchor lost")
	}
	if _, ok := files["OEBPS/members/0001/OEBPS/serial-sync/about.xhtml"]; ok {
		t.Fatal("member About survived final assembly")
	}
}

func TestAttachmentPublicationTitles(t *testing.T) {
	epub, err := buildSimpleEPUB("Author supplied title", "Author supplied name", "urn:test:original-title", time.Unix(0, 0).UTC(), []epubChapter{{FileName: "one.xhtml", Title: "One", BodyHTML: "<p>Story.</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		name, mime, title, author string
		content                   []byte
	}{
		{"chapter-53.pdf", "application/pdf", "Harbor Chapter 53", "Ada", minimalPDF()},
		{"chapter-53.html", "text/html", "Harbor Chapter 53", "Ada", []byte("<p>Chapter text.</p>")},
		{"chapter-53.epub", "application/epub+zip", "Author supplied title", "Author supplied name", epub},
	} {
		t.Run(input.name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, input.name)
			if err := os.WriteFile(file, input.content, 0600); err != nil {
				t.Fatal(err)
			}
			source := domain.Source{ID: "fictional"}
			track := domain.StoryTrack{TrackKey: "harbor", TrackName: "Harbor", CanonicalAuthor: "Ada"}
			release := domain.Release{ProviderReleaseID: "53", Title: "Harbor Chapter 53"}
			normalized := domain.NormalizedRelease{ProviderReleaseID: "53", Title: release.Title, Attachments: []domain.Attachment{{FileName: input.name, LocalPath: file, MIMEType: input.mime}}}
			decision := domain.TrackDecision{ContentStrategy: domain.ContentStrategyAttachmentOnly, OutputFormat: domain.OutputFormatEPUB, Publication: &domain.PublicationMetadata{}}
			m := New(filepath.Join(root, "artifacts"))
			plan, err := m.Plan(source, track, release, normalized, decision, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Materialize(context.Background(), source, track, release, plan); err != nil {
				t.Fatal(err)
			}
			_, pkg, _, err := unpackEPUB(plan.SelectedContent)
			if err != nil {
				t.Fatal(err)
			}
			if pkg.Metadata.Title != input.title || pkg.Metadata.Creator != input.author {
				t.Fatalf("attachment metadata = %q / %q, want %q / %q", pkg.Metadata.Title, pkg.Metadata.Creator, input.title, input.author)
			}
		})
	}
}

func TestVolumePreservesNavigationGroupsAndFragmentLinks(t *testing.T) {
	original, err := buildSimpleEPUB("Two chapters", "Ada", "urn:test:grouped-navigation", time.Unix(0, 0).UTC(), []epubChapter{{FileName: "one.xhtml", Title: "One", BodyHTML: "<p>Story.</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	files, pkg, packagePath, err := unpackEPUB(original)
	if err != nil {
		t.Fatal(err)
	}
	files["OEBPS/nav.xhtml"] = []byte(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><span>Part One</span><ol><li><a href="#first">First chapter</a></li><li><a href="#second">Second chapter</a></li></ol></li></ol></nav><section id="first"><h1>First chapter</h1><p>First story.</p></section><section id="second"><h1>Second chapter</h1><p>Second story.</p></section></body></html>`)
	for _, item := range pkg.Manifest.Items {
		if item.Href == "nav.xhtml" {
			pkg.Spine.Itemrefs = []opfItemref{{IDRef: item.ID}}
		}
	}
	files[packagePath] = mustXML(pkg)
	original, err = writeEPUBArchive(files)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateEPUBArchive(original); err != nil {
		t.Fatalf("source EPUB: %v", err)
	}
	file := filepath.Join(t.TempDir(), "member.epub")
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	volume := domain.VolumeEdition{ID: "volume", SeriesID: "series", GroupID: "book:one", Members: []domain.VolumeMember{{ReleaseID: "release", Position: 1}}, Artifact: domain.Artifact{Filename: "volume.epub"}}
	art, err := New(t.TempDir()).BuildVolume(context.Background(), volume, "Volume", []domain.PublishCandidate{{Release: domain.Release{ID: "release", Title: "Two chapters"}, Track: domain.StoryTrack{TrackName: "Series", CanonicalAuthor: "Ada"}, Artifact: domain.Artifact{StorageRef: file}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(art.StorageRef)
	if err != nil {
		t.Fatal(err)
	}
	assembled, _, _, err := unpackEPUB(data)
	if err != nil {
		t.Fatal(err)
	}
	var nav struct {
		Group struct {
			Label    string `xml:"span"`
			Chapters []struct {
				Title string `xml:",chardata"`
				Href  string `xml:"href,attr"`
			} `xml:"ol>li>a"`
		} `xml:"body>nav>ol>li>ol>li"`
	}
	if err := xml.Unmarshal(assembled["OEBPS/nav.xhtml"], &nav); err != nil {
		t.Fatal(err)
	}
	if nav.Group.Label != "Part One" || len(nav.Group.Chapters) != 2 {
		t.Fatalf("lost grouped member navigation: %s", assembled["OEBPS/nav.xhtml"])
	}
	for i, fragment := range []string{"first", "second"} {
		if nav.Group.Chapters[i].Href != "members/0001/OEBPS/nav.xhtml#"+fragment || nav.Group.Chapters[i].Title != []string{"First chapter", "Second chapter"}[i] {
			t.Fatalf("chapter %d lost its title or relocated target: %+v", i, nav.Group.Chapters[i])
		}
	}
}
