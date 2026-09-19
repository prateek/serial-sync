package artifact

import (
	"bytes"
	"context"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var externalToolMu sync.Mutex

func TestBuildSimpleEPUBProducesXMLWellFormedEPUB3(t *testing.T) {
	t.Parallel()

	content, err := buildSimpleEPUB("Main Series", "Author Name", "urn:uuid:11111111-1111-1111-1111-111111111111", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), []epubChapter{{
		FileName: "chapter-001.xhtml",
		Title:    "Chapter 1",
		BodyHTML: "<!doctype html><html><head><meta charset=\"utf-8\"></head><body><p>Hello<br>world</p><img src=\"https://example.com/cover.jpg\" alt=\"Cover\"></body></html>",
	}})
	if err != nil {
		t.Fatalf("buildSimpleEPUB: %v", err)
	}

	files := unzipEntries(t, content)
	chapter := string(files["OEBPS/chapter-001.xhtml"])
	if strings.Contains(strings.ToLower(chapter), "<!doctype") {
		t.Fatalf("chapter should not keep an HTML doctype: %s", chapter)
	}
	assertXMLWellFormed(t, "chapter", files["OEBPS/chapter-001.xhtml"])
	assertXMLWellFormed(t, "nav", files["OEBPS/nav.xhtml"])

	opf := string(files["OEBPS/content.opf"])
	if strings.Contains(string(files["OEBPS/chapter-001.xhtml"]), `<img`) {
		t.Fatalf("chapter should not preserve remote image embeds:\n%s", string(files["OEBPS/chapter-001.xhtml"]))
	}
	if !strings.Contains(string(files["OEBPS/chapter-001.xhtml"]), `<a href="https://example.com/cover.jpg">Cover</a>`) {
		t.Fatalf("chapter should keep a link to the remote image:\n%s", string(files["OEBPS/chapter-001.xhtml"]))
	}
	if strings.Contains(opf, `properties="remote-resources"`) {
		t.Fatalf("OPF should not mark outbound links as remote resources:\n%s", opf)
	}
	if got := strings.Count(opf, `property="dcterms:modified"`); got != 1 {
		t.Fatalf("dcterms:modified count = %d, want 1\n%s", got, opf)
	}
	if !strings.Contains(opf, ">2026-05-06T12:34:56Z<") {
		t.Fatalf("OPF does not contain stable modified timestamp:\n%s", opf)
	}
	if !strings.Contains(opf, `unique-identifier="bookid"`) || !strings.Contains(opf, `id="bookid"`) {
		t.Fatalf("OPF unique identifier does not point at dc:identifier:\n%s", opf)
	}
}

func TestGeneratedSimpleEPUBPassesEPUBCheck(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	content, err := buildSimpleEPUB("Main Series", "Author Name", "urn:uuid:11111111-1111-1111-1111-111111111111", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), []epubChapter{{
		FileName: "chapter-001.xhtml",
		Title:    "Chapter 1",
		BodyHTML: `<p align="center">Hello world</p><li><p>orphan item</p></li><ul><p>wrapped item</p>loose item<img src="https://example.com/cover.jpg" alt="Cover"/></ul><map name="m"><area shape="rect" coords="0,0,1,1" href="#note"/></map><p id="note">note</p>`,
	}})
	if err != nil {
		t.Fatalf("buildSimpleEPUB: %v", err)
	}
	chapter := string(unzipEntries(t, content)["OEBPS/chapter-001.xhtml"])
	if strings.Contains(chapter, "align=") || strings.Contains(chapter, "<li>orphan item") || strings.Contains(chapter, "<area") {
		t.Fatalf("chapter should not keep EPUBCheck-invalid attributes or orphan list items:\n%s", chapter)
	}
	if strings.Contains(chapter, "<p><p>") || !strings.Contains(chapter, "<div><p>orphan item</p></div>") {
		t.Fatalf("chapter should not render invalid nested paragraphs for orphan list items:\n%s", chapter)
	}
	if strings.Contains(chapter, "<ul><p>") || strings.Contains(chapter, "<ul><a ") || !strings.Contains(chapter, `<ul><li><p>wrapped item</p></li><li>loose item</li><li><a href="https://example.com/cover.jpg">Cover</a></li></ul>`) {
		t.Fatalf("chapter should wrap invalid list children in list items:\n%s", chapter)
	}
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestGeneratedSimpleEPUBWithRemoteImagePassesEPUBCheck(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	content, err := buildSimpleEPUB("Main Series", "Author Name", "urn:uuid:11111111-1111-1111-1111-111111111111", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), []epubChapter{{
		FileName: "chapter-001.xhtml",
		Title:    "Chapter 1",
		BodyHTML: `<p><img src="https://example.com/cover.jpg" alt="Cover"></p>`,
	}})
	if err != nil {
		t.Fatalf("buildSimpleEPUB: %v", err)
	}
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestGeneratedSimpleEPUBWithLinkedRemoteImagePassesEPUBCheck(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	content, err := buildSimpleEPUB("Main Series", "Author Name", "urn:uuid:11111111-1111-1111-1111-111111111111", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), []epubChapter{{
		FileName: "chapter-001.xhtml",
		Title:    "Chapter 1",
		BodyHTML: `<p><a href="https://example.com/post"><img src="https://example.com/cover.jpg" alt="Cover"></a></p>`,
	}})
	if err != nil {
		t.Fatalf("buildSimpleEPUB: %v", err)
	}
	chapter := string(unzipEntries(t, content)["OEBPS/chapter-001.xhtml"])
	if strings.Contains(chapter, "<a href=\"https://example.com/post\"><a ") {
		t.Fatalf("chapter should not render nested links for linked remote images:\n%s", chapter)
	}
	if !strings.Contains(chapter, `<a href="https://example.com/post">Cover</a>`) {
		t.Fatalf("chapter should preserve the original link with remote image alt text:\n%s", chapter)
	}
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestGeneratedSimpleEPUBDropsInlineStyleURLs(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	content, err := buildSimpleEPUB("Main Series", "Author Name", "urn:uuid:11111111-1111-1111-1111-111111111111", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), []epubChapter{{
		FileName: "chapter-001.xhtml",
		Title:    "Chapter 1",
		BodyHTML: `<p style="background-image:url(https://example.com/a.png); color: red">Hello</p>`,
	}})
	if err != nil {
		t.Fatalf("buildSimpleEPUB: %v", err)
	}
	chapter := string(unzipEntries(t, content)["OEBPS/chapter-001.xhtml"])
	if strings.Contains(chapter, `style=`) || strings.Contains(chapter, `https://example.com/a.png`) {
		t.Fatalf("chapter should drop inline styles that can hide remote resources:\n%s", chapter)
	}
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestGeneratedSimpleEPUBDropsRootRelativeLinks(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	content, err := buildSimpleEPUB("Main Series", "Author Name", "urn:uuid:11111111-1111-1111-1111-111111111111", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), []epubChapter{{
		FileName: "chapter-001.xhtml",
		Title:    "Chapter 1",
		BodyHTML: `<p><a href="/posts/1">Patreon post</a> <a href="https://example.com/posts/1">source post</a></p>`,
	}})
	if err != nil {
		t.Fatalf("buildSimpleEPUB: %v", err)
	}
	files := unzipEntries(t, content)
	chapter := string(files["OEBPS/chapter-001.xhtml"])
	if strings.Contains(chapter, `href="/posts/1"`) {
		t.Fatalf("chapter should not keep root-relative links:\n%s", chapter)
	}
	if !strings.Contains(chapter, `<a>Patreon post</a>`) || !strings.Contains(chapter, `<a href="https://example.com/posts/1">source post</a>`) {
		t.Fatalf("chapter should flatten unsafe links and keep absolute links:\n%s", chapter)
	}
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestGeneratedSimpleEPUBPreservesSameDocumentLinks(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	content, err := buildSimpleEPUB("Main Series", "Author Name", "urn:uuid:11111111-1111-1111-1111-111111111111", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), []epubChapter{{
		FileName: "chapter-001.xhtml",
		Title:    "Chapter 1",
		BodyHTML: `<p><a href="#note">note</a> <a href="chapter-001.xhtml#note">same file</a> <a href="#missing">missing</a></p><aside id="note"><p>Footnote</p></aside>`,
	}})
	if err != nil {
		t.Fatalf("buildSimpleEPUB: %v", err)
	}
	chapter := string(unzipEntries(t, content)["OEBPS/chapter-001.xhtml"])
	if !strings.Contains(chapter, `href="#note"`) || !strings.Contains(chapter, `href="chapter-001.xhtml#note"`) {
		t.Fatalf("chapter should preserve same-document links:\n%s", chapter)
	}
	if strings.Contains(chapter, `href="#missing"`) {
		t.Fatalf("chapter should not preserve missing fragment links:\n%s", chapter)
	}
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestGeneratedSimpleEPUBFlattensUnsupportedEmbeddedMarkup(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	content, err := buildSimpleEPUB("Main Series", "Author Name", "urn:uuid:11111111-1111-1111-1111-111111111111", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), []epubChapter{{
		FileName: "chapter-001.xhtml",
		Title:    "Chapter 1",
		BodyHTML: `<p>Before</p><svg xmlns="http://www.w3.org/2000/svg"><title>Chart</title><circle cx="1" cy="1" r="1"/></svg><math xmlns="http://www.w3.org/1998/Math/MathML"><mi>x</mi></math>`,
	}})
	if err != nil {
		t.Fatalf("buildSimpleEPUB: %v", err)
	}
	chapter := string(unzipEntries(t, content)["OEBPS/chapter-001.xhtml"])
	for _, forbidden := range []string{"<svg", "<circle", "<math", "<mi"} {
		if strings.Contains(chapter, forbidden) {
			t.Fatalf("chapter should not keep unsupported embedded markup %q:\n%s", forbidden, chapter)
		}
	}
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestWrapEPUBWithPrefaceKeepsEPUB2IdentifierAndTOC(t *testing.T) {
	t.Parallel()

	original := buildEPUB2Fixture(t)
	wrapped, err := wrapEPUBWithPreface(original, "Wrapped Book", "Author Name", "urn:uuid:22222222-2222-2222-2222-222222222222", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), "<details><summary>Preface</summary><p><mark>note</mark> <time>today</time><wbr/></p></details>")
	if err != nil {
		t.Fatalf("wrapEPUBWithPreface: %v", err)
	}

	files := unzipEntries(t, wrapped)
	preface, ok := files["serial-sync-preface.xhtml"]
	if !ok {
		t.Fatalf("preface entry missing; entries=%v", archiveKeys(files))
	}
	assertXMLWellFormed(t, "preface", preface)

	opf := string(files["content.opf"])
	if !strings.Contains(opf, `unique-identifier="uuid_id"`) || !strings.Contains(opf, `id="uuid_id"`) {
		t.Fatalf("EPUB 2 unique identifier was not preserved:\n%s", opf)
	}
	if !strings.Contains(opf, `<spine toc="ncx"`) {
		t.Fatalf("EPUB 2 spine toc was not repaired:\n%s", opf)
	}
	if !strings.Contains(opf, `<meta name="cover" content="cover-image"`) {
		t.Fatalf("EPUB 2 cover metadata was not preserved:\n%s", opf)
	}
	if !strings.Contains(opf, `opf:scheme="UUID"`) {
		t.Fatalf("EPUB 2 identifier attributes were not preserved:\n%s", opf)
	}
	if !strings.Contains(opf, `<guide`) || !strings.Contains(opf, `type="text" title="Chapter" href="chapter.xhtml"`) {
		t.Fatalf("EPUB 2 guide was not preserved:\n%s", opf)
	}
}

func TestWrappedEPUB2PassesEPUBCheck(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	original := buildEPUB2Fixture(t)
	wrapped, err := wrapEPUBWithPreface(original, "Wrapped Book", "Author Name", "urn:uuid:22222222-2222-2222-2222-222222222222", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), "<p>preface</p>")
	if err != nil {
		t.Fatalf("wrapEPUBWithPreface: %v", err)
	}
	path := filepath.Join(t.TempDir(), "wrapped.epub")
	if err := os.WriteFile(path, wrapped, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestWrapEPUBWithPrefaceKeepsSelectedIdentifier(t *testing.T) {
	t.Parallel()

	original := buildMultiIdentifierEPUB3Fixture(t)
	wrapped, err := wrapEPUBWithPreface(original, "Wrapped Book", "Author Name", "urn:uuid:22222222-2222-2222-2222-222222222222", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), "<p>preface</p>")
	if err != nil {
		t.Fatalf("wrapEPUBWithPreface: %v", err)
	}

	opf := string(unzipEntries(t, wrapped)["content.opf"])
	if !strings.Contains(opf, `unique-identifier="bookid"`) || !strings.Contains(opf, `<dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>`) {
		t.Fatalf("wrapped OPF should preserve identifier selected by package unique-identifier:\n%s", opf)
	}
	if !strings.Contains(opf, `<dc:identifier id="isbn">urn:isbn:9780000000000</dc:identifier>`) {
		t.Fatalf("wrapped OPF should preserve non-selected identifiers:\n%s", opf)
	}
}

func TestWrapEPUBWithPrefaceDropsDuplicatePrefaceIDs(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	original := buildMultiIdentifierEPUB3Fixture(t)
	wrapped, err := wrapEPUBWithPreface(original, "Wrapped Book", "Author Name", "urn:uuid:22222222-2222-2222-2222-222222222222", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), `<p id="note">first</p><p id="note">second</p>`)
	if err != nil {
		t.Fatalf("wrapEPUBWithPreface: %v", err)
	}
	preface := string(unzipEntries(t, wrapped)["serial-sync-preface.xhtml"])
	if got := strings.Count(preface, `id="note"`); got != 1 {
		t.Fatalf("preface id count = %d, want 1:\n%s", got, preface)
	}
	path := filepath.Join(t.TempDir(), "wrapped-duplicate-id.epub")
	if err := os.WriteFile(path, wrapped, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestWrapEPUBWithPrefaceAvoidsManifestIDCollision(t *testing.T) {
	t.Parallel()

	original := buildPrefaceCollisionFixture(t)
	wrapped, err := wrapEPUBWithPreface(original, "Wrapped Book", "Author Name", "urn:uuid:22222222-2222-2222-2222-222222222222", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), "<p>preface</p>")
	if err != nil {
		t.Fatalf("wrapEPUBWithPreface: %v", err)
	}

	opf := string(unzipEntries(t, wrapped)["content.opf"])
	if !strings.Contains(opf, `id="serial-sync-preface-2"`) || !strings.Contains(opf, `<itemref idref="serial-sync-preface-2"`) {
		t.Fatalf("wrapped OPF should add a collision-free preface id:\n%s", opf)
	}
}

func TestWrapEPUBWithPrefaceDropsInvalidOPFAttributes(t *testing.T) {
	t.Parallel()

	original := buildOPFExtraAttributeFixture(t)
	wrapped, err := wrapEPUBWithPreface(original, "Wrapped Book", "Author Name", "urn:uuid:22222222-2222-2222-2222-222222222222", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), "<p>preface</p>")
	if err != nil {
		t.Fatalf("wrapEPUBWithPreface: %v", err)
	}

	opf := string(unzipEntries(t, wrapped)["content.opf"])
	for _, invalid := range []string{
		`required-namespace="http://example.com/ns"`,
		`required-modules="module-a module-b"`,
		`fallback-style="style"`,
		`custom-spine="kept"`,
		`custom-itemref="kept"`,
	} {
		if strings.Contains(opf, invalid) {
			t.Fatalf("wrapped OPF should drop EPUBCheck-invalid attribute %q:\n%s", invalid, opf)
		}
	}
}

func TestValidateEPUBArchiveResolvesPercentEncodedManifestHrefs(t *testing.T) {
	t.Parallel()

	content := buildPercentEncodedHrefFixture(t)
	if err := validateEPUBArchive(content); err != nil {
		t.Fatalf("validateEPUBArchive: %v", err)
	}
}

func TestValidateEPUBArchiveSelectsPackageIdentifier(t *testing.T) {
	t.Parallel()

	content := buildMultiIdentifierEPUB3Fixture(t)
	if err := validateEPUBArchive(content); err != nil {
		t.Fatalf("validateEPUBArchive: %v", err)
	}
}

func TestValidateEPUBArchiveAllowsRefinedDCTermsModified(t *testing.T) {
	t.Parallel()

	content := buildRefinedModifiedEPUB3Fixture(t)
	if err := validateEPUBArchive(content); err != nil {
		t.Fatalf("validateEPUBArchive: %v", err)
	}
}

func TestValidateEPUBArchiveRequiresManifestResources(t *testing.T) {
	t.Parallel()

	content := buildMissingManifestResourceFixture(t)
	err := validateEPUBArchive(content)
	if err == nil || !strings.Contains(err.Error(), "missing resource") {
		t.Fatalf("validateEPUBArchive error = %v, want missing resource", err)
	}
}

func TestValidateEPUBArchiveRejectsDuplicateManifestIDs(t *testing.T) {
	t.Parallel()

	content := buildDuplicateManifestIDFixture(t)
	err := validateEPUBArchive(content)
	if err == nil || !strings.Contains(err.Error(), "appears more than once") {
		t.Fatalf("validateEPUBArchive error = %v, want duplicate manifest id", err)
	}
}

func TestValidateEPUBArchiveRequiresMetadataRefinesTargets(t *testing.T) {
	t.Parallel()

	content := buildMissingMetadataRefinesFixture(t)
	err := validateEPUBArchive(content)
	if err == nil || !strings.Contains(err.Error(), "refines missing target") {
		t.Fatalf("validateEPUBArchive error = %v, want missing refines target", err)
	}
}

func TestValidateEPUBArchiveRequiresCollectionTypeTarget(t *testing.T) {
	t.Parallel()

	content := buildInvalidCollectionTypeFixture(t)
	err := validateEPUBArchive(content)
	if err == nil || !strings.Contains(err.Error(), "collection-type") {
		t.Fatalf("validateEPUBArchive error = %v, want collection-type target error", err)
	}
}

func TestValidateEPUBArchiveRequiresValidDCTermsModified(t *testing.T) {
	t.Parallel()

	content := buildInvalidModifiedEPUB3Fixture(t)
	err := validateEPUBArchive(content)
	if err == nil || !strings.Contains(err.Error(), "dcterms:modified") {
		t.Fatalf("validateEPUBArchive error = %v, want invalid dcterms:modified error", err)
	}
}

func TestValidateEPUBArchiveRejectsXHTMLContentModelViolations(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	content := buildInvalidXHTMLContentModelFixture(t)
	err := validateEPUBArchive(content)
	if err == nil || !strings.Contains(err.Error(), "epubcheck validation failed") {
		t.Fatalf("validateEPUBArchive error = %v, want EPUBCheck content-model error", err)
	}
}

func TestValidateEPUBArchiveRejectsUndeclaredRemoteResources(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	content := buildUndeclaredRemoteResourceEPUB3Fixture(t)
	err := validateEPUBArchive(content)
	if err == nil || !strings.Contains(err.Error(), "epubcheck validation failed") {
		t.Fatalf("validateEPUBArchive error = %v, want EPUBCheck remote-resource error", err)
	}
}

func TestWrapEPUBWithPrefacePreservesOPFAttributes(t *testing.T) {
	t.Parallel()

	original := buildOPFPreservationFixture(t)
	wrapped, err := wrapEPUBWithPreface(original, "Wrapped Book", "Author Name", "urn:uuid:22222222-2222-2222-2222-222222222222", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), "<p>preface</p>")
	if err != nil {
		t.Fatalf("wrapEPUBWithPreface: %v", err)
	}

	files := unzipEntries(t, wrapped)
	opf := string(files["content.opf"])
	for _, want := range []string{
		`prefix="rendition: http://www.idpf.org/vocab/rendition/# custom: https://example.com/vocab#"`,
		`fallback="chapter"`,
		`page-progression-direction="rtl"`,
		`linear="no"`,
		`properties="page-spread-left"`,
		`properties="nav custom:landmark"`,
		`property="rendition:layout"`,
		`>pre-paginated<`,
	} {
		if !strings.Contains(opf, want) {
			t.Fatalf("wrapped OPF missing %q:\n%s", want, opf)
		}
	}
	preface := string(files["serial-sync-preface.xhtml"])
	if !strings.Contains(preface, `content="width=1024, height=768"`) {
		t.Fatalf("preface should reuse the fixed-layout viewport:\n%s", preface)
	}
	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	path := filepath.Join(t.TempDir(), "wrapped-preserved.epub")
	if err := os.WriteFile(path, wrapped, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestWrapEPUBWithPrefacePreservesDublinCoreMetadata(t *testing.T) {
	t.Parallel()

	original := buildDublinCoreMetadataFixture(t)
	wrapped, err := wrapEPUBWithPreface(original, "Wrapped Book", "Fallback Author", "urn:uuid:22222222-2222-2222-2222-222222222222", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), "<p>preface</p>")
	if err != nil {
		t.Fatalf("wrapEPUBWithPreface: %v", err)
	}
	opf := string(unzipEntries(t, wrapped)["content.opf"])
	for _, want := range []string{
		`<dc:creator id="primary">Primary Author</dc:creator>`,
		`<dc:creator>Second Author</dc:creator>`,
		`<dc:publisher>Publisher Name</dc:publisher>`,
		`<dc:subject>Subject One</dc:subject>`,
		`<dc:identifier id="isbn">urn:isbn:9780000000000</dc:identifier>`,
		`<dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>`,
		`property="schema:accessibilityFeature">tableOfContents</meta>`,
		`property="media:duration">0:00:01.000</meta>`,
		`property="custom:rating">5</meta>`,
		`id="collection-a" property="belongs-to-collection">Shared Series</meta>`,
		`id="collection-b" property="belongs-to-collection">Shared Series</meta>`,
		`refines="#collection-b" property="collection-type">series</meta>`,
		`refines="#collection-b" property="group-position">2</meta>`,
		`refines="#primary" property="file-as">Author, Primary</meta>`,
		`refines="#record" property="custom:rating">5</meta>`,
		`<link xmlns="http://www.idpf.org/2007/opf" id="record" rel="record" href="record.xml" media-type="application/xml"></link>`,
	} {
		if !strings.Contains(opf, want) {
			t.Fatalf("wrapped OPF missing %q:\n%s", want, opf)
		}
	}
}

func TestWrapEPUBWithPrefaceRepairsEmbeddedSVGManifestProperty(t *testing.T) {
	t.Parallel()

	original := buildInlineSVGEPUB3Fixture(t)
	wrapped, err := wrapEPUBWithPreface(original, "Wrapped Book", "Author Name", "urn:uuid:22222222-2222-2222-2222-222222222222", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), "<p>preface</p>")
	if err != nil {
		t.Fatalf("wrapEPUBWithPreface: %v", err)
	}
	opf := string(unzipEntries(t, wrapped)["content.opf"])
	if !strings.Contains(opf, `properties="svg"`) {
		t.Fatalf("wrapped OPF should mark XHTML items with embedded SVG:\n%s", opf)
	}
	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	path := filepath.Join(t.TempDir(), "wrapped-svg.epub")
	if err := os.WriteFile(path, wrapped, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func TestWrapEPUBWithPrefaceSanitizesCalibreEPUB3Metadata(t *testing.T) {
	t.Parallel()

	original := buildCalibreEPUB3Fixture(t)
	wrapped, err := wrapEPUBWithPreface(original, "Wrapped Book", "Author Name", "urn:uuid:22222222-2222-2222-2222-222222222222", time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC), "<p>preface</p>")
	if err != nil {
		t.Fatalf("wrapEPUBWithPreface: %v", err)
	}

	opf := string(unzipEntries(t, wrapped)["content.opf"])
	if strings.Contains(opf, "calibre:") {
		t.Fatalf("wrapped OPF should not keep undeclared calibre-prefixed properties:\n%s", opf)
	}
	for _, want := range []string{
		`refines="#title" property="title-type">main</meta>`,
		`refines="#creator" property="role">aut</meta>`,
	} {
		if !strings.Contains(opf, want) {
			t.Fatalf("wrapped OPF should keep valid refined metadata %q:\n%s", want, opf)
		}
	}
	for _, forbidden := range []string{
		`<meta property="role">`,
		`<meta property="title-type">`,
		`<meta property="file-as">`,
	} {
		if strings.Contains(opf, forbidden) {
			t.Fatalf("wrapped OPF should not keep unrefined Calibre metadata %q:\n%s", forbidden, opf)
		}
	}
	if strings.Contains(opf, `properties="svg"`) || strings.Contains(opf, `properties="svg `) || strings.Contains(opf, ` svg"`) {
		t.Fatalf("wrapped OPF should not keep stale svg manifest properties:\n%s", opf)
	}
	if got := strings.Count(opf, `property="dcterms:modified"`); got != 1 {
		t.Fatalf("dcterms:modified count = %d, want 1\n%s", got, opf)
	}
	if _, err := exec.LookPath("epubcheck"); err != nil {
		t.Fatalf("epubcheck is required for EPUB integration validation: %v", err)
	}
	path := filepath.Join(t.TempDir(), "wrapped-calibre.epub")
	if err := os.WriteFile(path, wrapped, 0o644); err != nil {
		t.Fatalf("write epub: %v", err)
	}
	assertEPUBCheckPasses(t, path)
}

func buildEPUB2Fixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<package xmlns="http://www.idpf.org/2007/opf" xmlns:opf="http://www.idpf.org/2007/opf" unique-identifier="uuid_id" version="2.0">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	    <dc:title>Original Book</dc:title>
	    <dc:creator>Original Author</dc:creator>
	    <dc:language>en</dc:language>
		    <dc:identifier id="uuid_id" opf:scheme="UUID">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
	    <meta name="cover" content="cover-image"/>
	  </metadata>
	  <manifest>
	    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
	    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
	    <item id="cover-image" href="cover.png" media-type="image/png"/>
	  </manifest>
	  <spine>
	    <itemref idref="chapter"/>
	  </spine>
	  <guide>
	    <reference type="text" title="Chapter" href="chapter.xhtml"/>
	  </guide>
	</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"toc.ncx":       []byte(`<?xml version="1.0" encoding="UTF-8"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head><meta name="dtb:uid" content="urn:uuid:33333333-3333-3333-3333-333333333333"/></head><docTitle><text>Original Book</text></docTitle><navMap><navPoint id="navPoint-1" playOrder="1"><navLabel><text>Chapter</text></navLabel><content src="chapter.xhtml"/></navPoint></navMap></ncx>`),
		"cover.png":     tinyPNG(),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildCalibreEPUB3Fixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="uuid_id" version="3.0" prefix="calibre: https://calibre-ebook.com">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title id="title">Original Book</dc:title>
    <meta refines="#title" property="title-type">main</meta>
    <dc:creator id="creator">Original Author</dc:creator>
    <meta refines="#creator" property="role">aut</meta>
    <dc:language>en</dc:language>
    <dc:identifier id="uuid_id">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
    <meta property="calibre:timestamp">2026-05-06T12:00:00Z</meta>
    <meta property="role">aut</meta>
    <meta property="file-as">Original Author</meta>
    <meta property="title-type">main</meta>
    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="titlepage" href="titlepage.xhtml" media-type="application/xhtml+xml" properties="svg calibre:title-page"/>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  </manifest>
  <spine>
    <itemref idref="titlepage"/>
  </spine>
</package>`),
		"titlepage.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Title</title></head><body><p>title</p></body></html>`),
		"nav.xhtml":       []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="titlepage.xhtml">Title</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildMultiIdentifierEPUB3Fixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0" prefix="custom: https://example.com/vocab#">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Original Book</dc:title>
    <dc:creator>Original Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="isbn">urn:isbn:9780000000000</dc:identifier>
    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  </manifest>
  <spine>
    <itemref idref="chapter"/>
  </spine>
</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildPrefaceCollisionFixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Original Book</dc:title>
    <dc:creator>Original Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="serial-sync-preface" href="chapter.xhtml" media-type="application/xhtml+xml"/>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  </manifest>
  <spine>
    <itemref idref="serial-sync-preface"/>
  </spine>
</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildOPFExtraAttributeFixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0" prefix="custom: https://example.com/vocab#">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	    <dc:title>Original Book</dc:title>
	    <dc:creator>Original Author</dc:creator>
	    <dc:language>en</dc:language>
	    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
	    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
	  </metadata>
	  <manifest>
	    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml" required-namespace="http://example.com/ns" required-modules="module-a module-b" fallback-style="style"/>
	    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
	  </manifest>
	  <spine custom-spine="kept">
	    <itemref idref="chapter" custom-itemref="kept"/>
	  </spine>
	</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildPercentEncodedHrefFixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OEBPS/content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Original Book</dc:title>
    <dc:creator>Original Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="chapter" href="Text/Chapter%201.xhtml" media-type="application/xhtml+xml"/>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  </manifest>
  <spine>
    <itemref idref="chapter"/>
  </spine>
</package>`),
		"OEBPS/Text/Chapter 1.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"OEBPS/nav.xhtml":            []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="Text/Chapter%201.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildRefinedModifiedEPUB3Fixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Original Book</dc:title>
    <dc:creator>Original Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
    <meta property="dcterms:modified" refines="#chapter">2026-05-06T12:00:01Z</meta>
  </metadata>
  <manifest>
    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  </manifest>
  <spine>
    <itemref idref="chapter"/>
  </spine>
</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildMissingManifestResourceFixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Original Book</dc:title>
    <dc:creator>Original Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
    <item id="css" href="missing.css" media-type="text/css"/>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  </manifest>
  <spine>
    <itemref idref="chapter"/>
  </spine>
</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildDuplicateManifestIDFixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	    <dc:title>Original Book</dc:title>
	    <dc:creator>Original Author</dc:creator>
	    <dc:language>en</dc:language>
	    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
	    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
	  </metadata>
	  <manifest>
	    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
	    <item id="chapter" href="second.xhtml" media-type="application/xhtml+xml"/>
	    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
	  </manifest>
	  <spine>
	    <itemref idref="chapter"/>
	  </spine>
	</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"second.xhtml":  []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Second</title></head><body><p>second</p></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildMissingMetadataRefinesFixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0" prefix="custom: https://example.com/vocab#">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	    <dc:title>Original Book</dc:title>
	    <dc:creator>Original Author</dc:creator>
	    <dc:language>en</dc:language>
	    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
	    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
	    <meta refines="#missing-collection" property="group-position">2</meta>
	  </metadata>
	  <manifest>
	    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
	    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
	  </manifest>
	  <spine>
	    <itemref idref="chapter"/>
	  </spine>
	</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildInvalidCollectionTypeFixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	    <dc:title>Original Book</dc:title>
	    <dc:creator id="primary">Original Author</dc:creator>
	    <dc:language>en</dc:language>
	    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
	    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
	    <meta refines="#primary" property="collection-type">series</meta>
	  </metadata>
	  <manifest>
	    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
	    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
	  </manifest>
	  <spine>
	    <itemref idref="chapter"/>
	  </spine>
	</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildInvalidModifiedEPUB3Fixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	    <dc:title>Original Book</dc:title>
	    <dc:creator>Original Author</dc:creator>
	    <dc:language>en</dc:language>
	    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
	    <meta property="dcterms:modified">not-a-date</meta>
	  </metadata>
	  <manifest>
	    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
	    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
	  </manifest>
	  <spine>
	    <itemref idref="chapter"/>
	  </spine>
	</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildInvalidXHTMLContentModelFixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	    <dc:title>Original Book</dc:title>
	    <dc:creator>Original Author</dc:creator>
	    <dc:language>en</dc:language>
	    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
	    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
	  </metadata>
	  <manifest>
	    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
	    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
	  </manifest>
	  <spine>
	    <itemref idref="chapter"/>
	  </spine>
	</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><ul><p>bad child</p></ul></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildUndeclaredRemoteResourceEPUB3Fixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
	<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0">
	  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
	    <dc:title>Original Book</dc:title>
	    <dc:creator>Original Author</dc:creator>
	    <dc:language>en</dc:language>
	    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
	    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
	  </metadata>
	  <manifest>
	    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
	    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
	  </manifest>
	  <spine>
	    <itemref idref="chapter"/>
	  </spine>
	</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p><img src="https://example.com/cover.jpg" alt="Cover" /></p></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildOPFPreservationFixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0" prefix="rendition: http://www.idpf.org/vocab/rendition/# custom: https://example.com/vocab#">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Original Book</dc:title>
    <dc:creator>Original Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
    <meta property="rendition:layout">pre-paginated</meta>
    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
    <item id="alternate" href="alternate.xhtml" media-type="application/xhtml+xml" fallback="chapter"/>
	    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav custom:landmark"/>
  </manifest>
  <spine page-progression-direction="rtl">
    <itemref idref="chapter"/>
    <itemref idref="alternate" linear="no" properties="page-spread-left"/>
  </spine>
</package>`),
		"chapter.xhtml":   []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><meta name="viewport" content="width=1024, height=768"/><title>Chapter</title></head><body><p>chapter</p><p><a href="alternate.xhtml">Alternate</a></p></body></html>`),
		"alternate.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><meta name="viewport" content="width=1024, height=768"/><title>Alternate</title></head><body><p>alternate</p></body></html>`),
		"nav.xhtml":       []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildDublinCoreMetadataFixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0" prefix="custom: https://example.com/vocab#">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Original Book</dc:title>
    <dc:creator id="primary">Primary Author</dc:creator>
    <dc:creator>Second Author</dc:creator>
	    <dc:publisher>Publisher Name</dc:publisher>
	    <dc:subject>Subject One</dc:subject>
	    <dc:language>en</dc:language>
	    <dc:identifier id="isbn">urn:isbn:9780000000000</dc:identifier>
		    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
		    <meta property="schema:accessibilityFeature">tableOfContents</meta>
		    <meta property="media:duration">0:00:01.000</meta>
    <meta refines="#record" property="custom:rating">5</meta>
			    <meta id="collection-a" property="belongs-to-collection">Shared Series</meta>
			    <meta id="collection-b" property="belongs-to-collection">Shared Series</meta>
			    <meta refines="#collection-b" property="collection-type">series</meta>
			    <meta refines="#collection-b" property="group-position">2</meta>
		    <meta refines="#primary" property="file-as">Author, Primary</meta>
	    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
    <link id="record" rel="record" href="record.xml" media-type="application/xml"/>
	  </metadata>
  <manifest>
    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  </manifest>
  <spine>
    <itemref idref="chapter"/>
  </spine>
</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p>chapter</p></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
		"record.xml":    []byte(`<?xml version="1.0" encoding="UTF-8"?><record/>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func buildInlineSVGEPUB3Fixture(t *testing.T) []byte {
	t.Helper()

	content, err := writeEPUBArchive(map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"content.opf": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Original Book</dc:title>
    <dc:creator>Original Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="bookid">urn:uuid:33333333-3333-3333-3333-333333333333</dc:identifier>
    <meta property="dcterms:modified">2026-05-06T12:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  </manifest>
  <spine>
    <itemref idref="chapter"/>
  </spine>
</package>`),
		"chapter.xhtml": []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><title>Dot</title><circle cx="5" cy="5" r="4"/></svg></body></html>`),
		"nav.xhtml":     []byte(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Nav</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`),
	})
	if err != nil {
		t.Fatalf("writeEPUBArchive: %v", err)
	}
	return content
}

func tinyPNG() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
		0x42, 0x60, 0x82,
	}
}

func assertXMLWellFormed(t *testing.T, name string, content []byte) {
	t.Helper()

	decoder := xml.NewDecoder(bytes.NewReader(content))
	for {
		_, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				return
			}
			t.Fatalf("%s is not XML well-formed: %v\n%s", name, err, string(content))
		}
	}
}

func assertEPUBCheckPasses(t *testing.T, path string) {
	t.Helper()

	externalToolMu.Lock()
	defer externalToolMu.Unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "epubcheck", path).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("epubcheck timed out: %v\n%s", ctx.Err(), string(output))
	}
	if err != nil {
		t.Fatalf("epubcheck failed: %v\n%s", err, string(output))
	}
}
