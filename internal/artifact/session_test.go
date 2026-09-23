package artifact

import (
	"bytes"
	"reflect"
	"testing"
	"time"
)

// These tests prove the deepening rather than the behaviour: the pipeline stages'
// pinned output bytes (pin_table_test.go) are the behavioural proof that
// openEPUBPackage and (*epubPackage).write are byte-compatible replacements for the
// two unzip paths they replace.

func TestOpenEPUBPackageRejectsBrokenArchives(t *testing.T) {
	validContainer := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)

	tests := []struct {
		name    string
		content func(t *testing.T) []byte
		wantErr string
	}{
		{
			name:    "not a zip",
			content: func(t *testing.T) []byte { return []byte("not a zip file") },
			wantErr: "zip: not a valid zip file",
		},
		{
			name: "zip with no META-INF/container.xml",
			content: func(t *testing.T) []byte {
				content, err := writeEPUBArchive(map[string][]byte{"mimetype": []byte("application/epub+zip")})
				if err != nil {
					t.Fatal(err)
				}
				return content
			},
			wantErr: "epub missing META-INF/container.xml",
		},
		{
			name: "container that is not XML",
			content: func(t *testing.T) []byte {
				content, err := writeEPUBArchive(map[string][]byte{
					"mimetype":               []byte("application/epub+zip"),
					"META-INF/container.xml": []byte("<<<not xml>>>"),
				})
				if err != nil {
					t.Fatal(err)
				}
				return content
			},
			wantErr: "XML syntax error on line 1: expected element name after <",
		},
		{
			name: "container with no rootfile",
			content: func(t *testing.T) []byte {
				content, err := writeEPUBArchive(map[string][]byte{
					"mimetype": []byte("application/epub+zip"),
					"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles></rootfiles></container>`),
				})
				if err != nil {
					t.Fatal(err)
				}
				return content
			},
			wantErr: "epub container missing rootfile",
		},
		{
			name: "rootfile pointing at an absent package document",
			content: func(t *testing.T) []byte {
				content, err := writeEPUBArchive(map[string][]byte{
					"mimetype":               []byte("application/epub+zip"),
					"META-INF/container.xml": validContainer,
				})
				if err != nil {
					t.Fatal(err)
				}
				return content
			},
			wantErr: `epub missing package document "OEBPS/content.opf"`,
		},
		{
			name: "package document that is not XML",
			content: func(t *testing.T) []byte {
				content, err := writeEPUBArchive(map[string][]byte{
					"mimetype":               []byte("application/epub+zip"),
					"META-INF/container.xml": validContainer,
					"OEBPS/content.opf":      []byte("<<<not xml>>>"),
				})
				if err != nil {
					t.Fatal(err)
				}
				return content
			},
			wantErr: "XML syntax error on line 1: expected element name after <",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := openEPUBPackage(test.content(t))
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("openEPUBPackage error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestEPUBPackageWriteEncodings(t *testing.T) {
	// buildSimpleEPUB, not buildEPUB2Fixture: a fixture with a raw passthrough element
	// (buildEPUB2Fixture's <guide>) preserves the literal inter-element whitespace of
	// whichever document it was last parsed from, so an indented and a compact
	// re-serialization of the same guide legitimately reparse to different whitespace
	// text nodes. That is real, pre-existing behavior of the raw-XML passthrough, not
	// something this encoding pins.
	original, err := buildSimpleEPUB("Write Encodings", "Author", "urn:test:write-encodings", time.Unix(0, 0).UTC(), []epubChapter{{FileName: "chapter.xhtml", Title: "Chapter", BodyHTML: "<p>Story.</p>"}})
	if err != nil {
		t.Fatal(err)
	}

	indentedSession, err := openEPUBPackage(original)
	if err != nil {
		t.Fatal(err)
	}
	indented, err := indentedSession.write(packageIndented)
	if err != nil {
		t.Fatal(err)
	}

	compactSession, err := openEPUBPackage(original)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := compactSession.write(packageCompact)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(indented, compact) {
		t.Fatal("packageIndented and packageCompact produced identical bytes")
	}

	after1, err := openEPUBPackage(indented)
	if err != nil {
		t.Fatal(err)
	}
	after2, err := openEPUBPackage(compact)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after1.Package, after2.Package) {
		t.Fatalf("re-opened packages differ:\nindented: %+v\ncompact: %+v", after1.Package, after2.Package)
	}

	for name, want := range after1.Files {
		if name == after1.PackagePath {
			continue
		}
		got, ok := after2.Files[name]
		if !ok || !bytes.Equal(want, got) {
			t.Fatalf("non-package entry %q differs between encodings", name)
		}
	}
}

func TestEPUBPackageEntryResolvesHrefs(t *testing.T) {
	root, err := openEPUBPackage(buildEPUB2Fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if root.Dir != "" {
		t.Fatalf("root package Dir = %q, want \"\"", root.Dir)
	}
	if entry, err := root.entry("chapter.xhtml"); err != nil || entry != "chapter.xhtml" {
		t.Fatalf("root entry(\"chapter.xhtml\") = %q, %v, want \"chapter.xhtml\", nil", entry, err)
	}

	nested, err := openEPUBPackage(buildPercentEncodedHrefFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if nested.Dir != "OEBPS" {
		t.Fatalf("nested package Dir = %q, want \"OEBPS\"", nested.Dir)
	}
	if entry, err := nested.entry("nav.xhtml"); err != nil || entry != "OEBPS/nav.xhtml" {
		t.Fatalf("nested entry(\"nav.xhtml\") = %q, %v, want \"OEBPS/nav.xhtml\", nil", entry, err)
	}

	// A percent-encoded href resolves the same way at the archive root ("" Dir) as it
	// does nested under OEBPS/: decode the escape, then resolve against the package
	// document's own directory. The root half is the one the design depends on,
	// because the callers converted here used to pass "." and reach path.Join.
	rootEntry, err := root.entry("Text/Chapter%201.xhtml")
	if err != nil || rootEntry != "Text/Chapter 1.xhtml" {
		t.Fatalf("root entry(\"Text/Chapter%%201.xhtml\") = %q, %v, want \"Text/Chapter 1.xhtml\", nil", rootEntry, err)
	}
	nestedEntry, err := nested.entry("Text/Chapter%201.xhtml")
	if err != nil || nestedEntry != "OEBPS/"+rootEntry {
		t.Fatalf("nested entry(\"Text/Chapter%%201.xhtml\") = %q, %v, want %q, nil", nestedEntry, err, "OEBPS/"+rootEntry)
	}

	// entry does not reject an href that escapes the archive; it keeps the "../" so
	// that callers can. BuildVolume is the one that refuses it.
	if entry, err := root.entry("../evil.xhtml"); err != nil || entry != "../evil.xhtml" {
		t.Fatalf("root entry(\"../evil.xhtml\") = %q, %v, want \"../evil.xhtml\", nil", entry, err)
	}
}

func TestEPUBPackageWriteIsStructurallyValidated(t *testing.T) {
	wantErr := `manifest item "missing" missing resource "missing.xhtml"`
	for _, encoding := range []packageEncoding{packageIndented, packageCompact} {
		session, err := openEPUBPackage(buildEPUB2Fixture(t))
		if err != nil {
			t.Fatal(err)
		}
		session.Package.Manifest.Items = append(session.Package.Manifest.Items, opfItem{ID: "missing", Href: "missing.xhtml", MediaType: "application/xhtml+xml"})
		if _, err := session.write(encoding); err == nil || err.Error() != wantErr {
			t.Fatalf("write(%v) error = %v, want %q", encoding, err, wantErr)
		}
	}
}
