package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"image"
	"image/png"
	"os"
	"path"
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
			metadata := publicationMetadata{Title: "Post title", Author: "Post author", Series: "Series", SeriesIndex: "53", PreserveEmbedded: true, Publication: &domain.PublicationMetadata{Description: "A curated description.", DescriptionOverride: true, Language: "en", Cover: asset, Authors: []domain.AuthorProfile{{ID: "author", Name: "Original author", Biography: "Writes serials.", Portrait: asset, URL: "https://example.com/author"}}, Links: []string{"https://example.com/chapter"}}}
			content, err := withPublicationMetadata(original, metadata)
			if err != nil {
				t.Fatal(err)
			}
			beforeSession, err := openEPUBPackage(original)
			if err != nil {
				t.Fatal(err)
			}
			before, beforePkg, packagePath := beforeSession.Files, beforeSession.Package, beforeSession.PackagePath
			afterSession, err := openEPUBPackage(content)
			if err != nil {
				t.Fatal(err)
			}
			after, afterPkg := afterSession.Files, afterSession.Package
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
			if len(afterPkg.Spine.Itemrefs) != len(beforePkg.Spine.Itemrefs) {
				t.Fatal("standalone publication appended back matter")
			}
			if !strings.Contains(string(after[packagePath]), "A curated description.") {
				t.Fatal("description not embedded")
			}
			if !strings.Contains(string(after[packagePath]), "Writes serials.") {
				t.Fatal("author biography not retained in metadata")
			}
			if err := validateEPUBArchive(content); err != nil {
				t.Fatal(err)
			}
			again, err := withPublicationMetadata(original, metadata)
			if err != nil || !bytes.Equal(content, again) {
				t.Fatalf("identical inputs not deterministic: %v", err)
			}
			again, err = withPublicationMetadata(content, metadata)
			if err != nil || !bytes.Equal(content, again) {
				var againFiles map[string][]byte
				if againSession, err := openEPUBPackage(again); err == nil {
					againFiles = againSession.Files
				}
				for name, before := range after {
					if !bytes.Equal(before, againFiles[name]) {
						t.Logf("changed %s\nbefore: %s\nafter: %s", name, before, againFiles[name])
					}
				}
				t.Fatalf("reprocessing changed publication: %v", err)
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
	member, err := withPublicationMetadata(original, publicationMetadata{Title: "Two chapters", Author: "Author", Publication: metadata, IncludeAbout: true})
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
	session, err := openEPUBPackage(data)
	if err != nil {
		t.Fatal(err)
	}
	files, pkg := session.Files, session.Package
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
		{"chapter-53.pdf", "application/pdf", "Chapter 53", "Ada", minimalPDF()},
		{"chapter-53.html", "text/html", "Chapter 53", "Ada", []byte("<p>Chapter text.</p>")},
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
			plan, err := m.Plan(context.Background(), source, track, release, normalized, decision, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Materialize(context.Background(), source, track, release, plan); err != nil {
				t.Fatal(err)
			}
			session, err := openEPUBPackage(plan.SelectedContent)
			if err != nil {
				t.Fatal(err)
			}
			pkg := session.Package
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
	session, err := openEPUBPackage(original)
	if err != nil {
		t.Fatal(err)
	}
	files, pkg := session.Files, session.Package
	files["OEBPS/nav.xhtml"] = []byte(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><span>Part One</span><ol><li><a href="#first">First chapter</a></li><li><a href="#second">Second chapter</a></li></ol></li></ol></nav><section id="first"><h1>First chapter</h1><p>First story.</p></section><section id="second"><h1>Second chapter</h1><p>Second story.</p></section></body></html>`)
	for _, item := range pkg.Manifest.Items {
		if item.Href == "nav.xhtml" {
			pkg.Spine.Itemrefs = []opfItemref{{IDRef: item.ID}}
		}
	}
	files[session.PackagePath] = mustXML(pkg)
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
	assembledSession, err := openEPUBPackage(data)
	if err != nil {
		t.Fatal(err)
	}
	assembled := assembledSession.Files
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

func TestPublicationRemovesOnlyGeneratedBackMatter(t *testing.T) {
	for _, version := range []string{"2", "3"} {
		for _, includeAbout := range []bool{false, true} {
			t.Run(fmt.Sprintf("epub%s/about=%t", version, includeAbout), func(t *testing.T) {
				original := buildEPUB2Fixture(t)
				if version == "3" {
					var err error
					original, err = buildSimpleEPUB("Story", "Author", "urn:test:backmatter", time.Unix(0, 0), []epubChapter{
						{FileName: "chapter.xhtml", Title: "Chapter", BodyHTML: "<p>Story ending.</p>"},
						{FileName: "about.xhtml", Title: "Author's afterword", BodyHTML: "<p>Original author back matter.</p>"},
					})
					if err != nil {
						t.Fatal(err)
					}
				}
				original, err := wrapEPUBWithPreface(original, "Story", "Author", "urn:test:backmatter", time.Unix(0, 0), "<p>Configured post preface.</p>")
				if err != nil {
					t.Fatal(err)
				}
				beforeSession, err := openEPUBPackage(original)
				if err != nil {
					t.Fatal(err)
				}
				before, beforePkg, packagePath := beforeSession.Files, beforeSession.Package, beforeSession.PackagePath
				session, err := openEPUBPackage(original)
				if err != nil {
					t.Fatal(err)
				}
				files := session.Files
				for i := 0; i < 2; i++ {
					if err := session.appendAboutPage([]publicationAuthor{{Name: "Old author", Biography: "Old biography."}}, nil, ""); err != nil {
						t.Fatal(err)
					}
				}
				for _, item := range session.Package.Manifest.Items {
					isNCX := item.MediaType == "application/x-dtbncx+xml"
					if !isNCX && !strings.Contains(item.Properties, "nav") {
						continue
					}
					entry, err := resolveManifestHref(path.Dir(packagePath), item.Href)
					if err != nil {
						t.Fatal(err)
					}
					files[entry], err = appendAboutNavigation(files[entry], "serial-sync/about.xhtml", "duplicate-about", isNCX)
					if err != nil {
						t.Fatal(err)
					}
				}
				legacy, err := session.write(packageIndented)
				if err != nil {
					t.Fatal(err)
				}
				metadata := publicationMetadata{PreserveEmbedded: true, Series: "Series", SeriesIndex: "2", IncludeAbout: includeAbout, Publication: &domain.PublicationMetadata{
					Description: "Book synopsis.", SourceURL: "https://example.com/post", Cover: testMetadataAsset(t), CoverOverride: true,
					Authors: []domain.AuthorProfile{{ID: "author", Name: "Author", Biography: "First paragraph.\n\nSecond paragraph.", URL: "https://example.com/author", Portrait: testMetadataAsset(t)}},
					Links:   []string{"https://example.com/post", "https://example.com/author", "https://example.org/reading", "javascript:alert(1)"},
				}}
				result, err := withPublicationMetadata(legacy, metadata)
				if err != nil {
					t.Fatal(err)
				}
				resultSession, err := openEPUBPackage(result)
				if err != nil {
					t.Fatal(err)
				}
				after, resultPkg := resultSession.Files, resultSession.Package
				wantSpine := len(beforePkg.Spine.Itemrefs)
				if includeAbout {
					wantSpine++
				}
				if len(resultPkg.Spine.Itemrefs) != wantSpine {
					t.Fatalf("spine = %d, want %d", len(resultPkg.Spine.Itemrefs), wantSpine)
				}
				for _, item := range beforePkg.Manifest.Items {
					entry, err := resolveManifestHref(path.Dir(packagePath), item.Href)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(item.Properties, "nav") || item.MediaType == "application/x-dtbncx+xml" {
						count := bytes.Count(after[entry], []byte(">About<"))
						want := 0
						if includeAbout {
							want = 1
						}
						if count != want {
							t.Fatalf("About navigation count = %d, want %d: %s", count, want, after[entry])
						}
					} else if !bytes.Equal(before[entry], after[entry]) {
						t.Fatalf("original resource changed: %s", entry)
					}
				}
				if !bytes.Contains(after[packagePath], []byte("<dc:source>https://example.com/post</dc:source>")) {
					t.Fatal("source metadata missing")
				}
				if bytes.Contains(after[packagePath], []byte("javascript:")) {
					t.Fatal("unsafe URL embedded")
				}
				for entry, content := range after {
					if !strings.HasSuffix(entry, ".xhtml") {
						continue
					}
					if bytes.Contains(content, []byte("Old biography.")) {
						t.Fatalf("old page survived: %s", entry)
					}
					if includeAbout && strings.Contains(entry, "serial-sync/about.xhtml") {
						for _, want := range []string{"<p>First paragraph.</p>", "<p>Second paragraph.</p>", "Original publication", "author page", "Related reading on example.org"} {
							if !bytes.Contains(content, []byte(want)) {
								t.Fatalf("About missing %q: %s", want, content)
							}
						}
						if bytes.Contains(content, []byte(">https://")) || bytes.Contains(content, []byte("Book synopsis.")) {
							t.Fatal("About contains raw URL label or repeats synopsis")
						}
					}
				}
				if err := validateEPUBArchive(result); err != nil {
					t.Fatal(err)
				}
				again, err := withPublicationMetadata(result, metadata)
				if err != nil || !bytes.Equal(result, again) {
					t.Fatalf("repeat decoration changed EPUB: %v", err)
				}
			})
		}
	}
}

func TestWrappedChapterRemovesGeneratedAbout(t *testing.T) {
	original, err := buildSimpleEPUB("Chapter", "Author", "urn:test:wrapped-about", time.Unix(0, 0), []epubChapter{{FileName: "chapter.xhtml", Title: "Chapter", BodyHTML: "<p>Story ending.</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := &domain.PublicationMetadata{Authors: []domain.AuthorProfile{{ID: "author", Name: "Author", Biography: "Generated biography."}}}
	original, err = withPublicationMetadata(original, publicationMetadata{Publication: metadata, IncludeAbout: true, PreserveEmbedded: true})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "chapter.epub")
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	normalized := domain.NormalizedRelease{Title: "Chapter", TextPlain: "Configured post preface.", Attachments: []domain.Attachment{{FileName: "chapter.epub", MIMEType: "application/epub+zip", LocalPath: file}}}
	decision := domain.TrackDecision{OutputFormat: domain.OutputFormatEPUB, ContentStrategy: domain.ContentStrategyAttachmentOnly, PrefaceMode: domain.PrefaceModePrependPost, Publication: metadata}
	plan, err := New(t.TempDir()).Plan(context.Background(), domain.Source{ID: "source"}, domain.StoryTrack{TrackKey: "story"}, domain.Release{Title: "Chapter"}, normalized, decision, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := openEPUBPackage(plan.SelectedContent)
	if err != nil {
		t.Fatal(err)
	}
	files, pkg := session.Files, session.Package
	if len(pkg.Spine.Itemrefs) != 2 {
		t.Fatalf("wrapped chapter has %d reading sections, want preface + chapter", len(pkg.Spine.Itemrefs))
	}
	if _, ok := files["OEBPS/serial-sync/about.xhtml"]; ok {
		t.Fatal("generated page survived wrapping")
	}
	if !bytes.Contains(files["OEBPS/serial-sync-preface.xhtml"], []byte("Configured post preface.")) {
		t.Fatal("configured preface lost")
	}
	if !bytes.Contains(files["OEBPS/chapter.xhtml"], []byte("Story ending.")) {
		t.Fatal("story lost")
	}
	if err := validateEPUBArchive(plan.SelectedContent); err != nil {
		t.Fatal(err)
	}
}
