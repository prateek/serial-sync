package artifact

import (
	"bytes"
	"encoding/xml"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

func TestPublicationIdentityPolicy(t *testing.T) {
	for _, version := range []string{"2", "3"} {
		for _, embeddedTitle := range []string{"Unknown", "Book Two", "Book Two Chapter 033"} {
			t.Run(version+"/"+embeddedTitle, func(t *testing.T) {
				original := buildEPUB2Fixture(t)
				if version == "3" {
					var err error
					original, err = buildSimpleEPUB(embeddedTitle, "Old Author", "urn:test:identity", time.Unix(0, 0).UTC(), []epubChapter{{FileName: "story.xhtml", Title: "Chapter 32", BodyHTML: "<p>Unchanged story.</p>"}})
					if err != nil {
						t.Fatal(err)
					}
				}
				session, err := openEPUBPackage(original)
				if err != nil {
					t.Fatal(err)
				}
				files, pkg, packagePath := session.Files, session.Package, session.PackagePath
				attr := func(name, value string) xml.Attr { return xml.Attr{Name: xml.Name{Local: name}, Value: value} }
				pkg.Metadata.DCElements = []opfDCElement{
					{Name: "title", Value: embeddedTitle, Attrs: []xml.Attr{attr("id", "old-title")}},
					{Name: "creator", Value: "Old Author", Attrs: []xml.Attr{attr("id", "old-author"), {Name: xml.Name{Space: "http://www.idpf.org/2007/opf", Local: "file-as"}, Value: "Author, Old"}}},
					{Name: "creator", Value: "Second Old Author"},
					{Name: "language", Value: "en"},
					{Name: "contributor", Value: "Retained Translator", Attrs: []xml.Attr{attr("id", "translator")}},
					{Name: "rights", Value: "Retained copyright"},
				}
				pkg.Metadata.Meta = append(pkg.Metadata.Meta, opfMeta{Name: "calibre:author_sort", Content: "Author, Old"}, opfMeta{Name: "calibre:title_sort", Content: embeddedTitle})
				if version == "3" {
					pkg.Metadata.Meta = append(pkg.Metadata.Meta,
						opfMeta{Property: "alternate-script", Refines: "#old-sort", Value: "Old alternate name"},
						opfMeta{Property: "file-as", Refines: "#old-author", Value: "Author, Old", Attrs: []xml.Attr{attr("id", "old-sort")}},
						opfMeta{Property: "title-type", Refines: "#old-title", Value: "main"},
						opfMeta{Property: "role", Refines: "#translator", Value: "trl"},
						opfMeta{Property: "alternate-script", Refines: "#old-record", Value: "Old record refinement"},
					)
					for _, target := range []string{"old-author", "translator"} {
						id := "translator-record"
						if target == "old-author" {
							id = "old-record"
						}
						start := xml.StartElement{Name: xml.Name{Space: "http://www.idpf.org/2007/opf", Local: "link"}, Attr: []xml.Attr{attr("id", id), attr("refines", path.Base(packagePath)+"#"+target), attr("rel", "voicing"), attr("media-type", "audio/mpeg"), attr("href", "https://example.com/"+target+".mp3")}}
						pkg.Metadata.Raw = append(pkg.Metadata.Raw, opfRawElement{Tokens: []xml.Token{start, start.End()}})
					}
				}
				files[packagePath] = mustXML(pkg)
				original, err = writeEPUBArchive(files)
				if err != nil {
					t.Fatal(err)
				}
				beforeSession, err := openEPUBPackage(original)
				if err != nil {
					t.Fatal(err)
				}
				before := beforeSession.Package
				for _, policy := range []domain.PublicationIdentitySource{"", domain.PublicationIdentityEmbedded, domain.PublicationIdentityRelease} {
					metadata := publicationMetadata{Title: "Harbor Book Two Chapter 032", Author: "Canonical Author", PreserveEmbedded: true, Publication: &domain.PublicationMetadata{IdentitySource: policy}}
					result, err := withPublicationMetadata(original, metadata)
					if err != nil {
						t.Fatal(err)
					}
					afterSession, err := openEPUBPackage(result)
					if err != nil {
						t.Fatal(err)
					}
					afterFiles, after := afterSession.Files, afterSession.Package
					for _, item := range before.Manifest.Items {
						if strings.Contains(item.Properties, "nav") || item.MediaType == "application/x-dtbncx+xml" {
							continue
						}
						entry, err := resolveManifestHref(path.Dir(packagePath), item.Href)
						if err != nil || !bytes.Equal(files[entry], afterFiles[entry]) {
							t.Fatalf("story resource changed: %s, %v", entry, err)
						}
					}
					if policy != domain.PublicationIdentityRelease {
						if !reflect.DeepEqual(before.Metadata.DCElements, after.Metadata.DCElements) {
							t.Fatal("default/embedded policy changed original Dublin Core values or attributes")
						}
						continue
					}
					titles, creators := 0, 0
					for _, element := range after.Metadata.DCElements {
						switch element.Name {
						case "title":
							titles++
							if element.Value != metadata.Title || len(element.Attrs) != 0 {
								t.Fatalf("stale title: %+v", element)
							}
						case "creator":
							creators++
							if element.Value != metadata.Author || len(element.Attrs) != 0 {
								t.Fatalf("stale creator: %+v", element)
							}
						case "contributor", "rights":
							found := false
							for _, original := range before.Metadata.DCElements {
								found = found || reflect.DeepEqual(element, original)
							}
							if !found {
								t.Fatalf("unrelated metadata changed: %+v", element)
							}
						}
					}
					if titles != 1 || creators != 1 {
						t.Fatalf("identity entries = %d titles / %d creators", titles, creators)
					}
					translatorRefinement := false
					for _, meta := range after.Metadata.Meta {
						translatorRefinement = translatorRefinement || meta.Refines == "#translator" && meta.Property == "role" && meta.Value == "trl"
						if strings.HasPrefix(meta.Refines, "#old-") || strings.HasPrefix(meta.Name, "calibre:author_") || meta.Name == "calibre:title_sort" {
							t.Fatalf("stale identity refinement: %+v", meta)
						}
					}
					if version == "3" && !translatorRefinement {
						t.Fatal("unrelated contributor refinement lost")
					}
					if version == "3" && (bytes.Contains(afterFiles[packagePath], []byte(`id="old-record"`)) || !bytes.Contains(afterFiles[packagePath], []byte(`id="translator-record"`))) {
						t.Fatal("identity record link survived or unrelated record link was removed")
					}
					if embeddedTitle == "Unknown" {
						if err := validateEPUBArchive(result); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
		}
	}
}
