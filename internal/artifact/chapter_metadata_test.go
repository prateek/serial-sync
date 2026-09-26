package artifact

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
	"golang.org/x/net/html"
)

type chapterPlanInput struct {
	series, releaseTitle string
	sequence             *domain.Sequence
	output               domain.OutputFormat
	identity             domain.PublicationIdentitySource
	attachment           []byte // an EPUB attachment; nil builds from the post body
	preface              bool
}

func planChapter(t *testing.T, input chapterPlanInput) *epubPackage {
	t.Helper()
	root := t.TempDir()
	track := domain.StoryTrack{TrackKey: "story", TrackName: input.series, CanonicalAuthor: "Ada"}
	release := domain.Release{SourceID: "source", ProviderReleaseID: "71", Title: input.releaseTitle}
	normalized := domain.NormalizedRelease{ProviderReleaseID: "71", Title: input.releaseTitle, TextHTML: "<p>Story body.</p>"}
	decision := domain.TrackDecision{ContentStrategy: domain.ContentStrategyTextPost, OutputFormat: input.output, Sequence: input.sequence, Publication: &domain.PublicationMetadata{IdentitySource: input.identity}}
	if input.attachment != nil {
		file := filepath.Join(root, "chapter.epub")
		if err := os.WriteFile(file, input.attachment, 0o600); err != nil {
			t.Fatal(err)
		}
		normalized.Attachments = []domain.Attachment{{FileName: "chapter.epub", MIMEType: "application/epub+zip", LocalPath: file}}
		normalized.TextHTML = ""
		if input.preface {
			normalized.TextHTML = "<p>Thanks for reading.</p>"
			decision.PrefaceMode = domain.PrefaceModePrependPost
		}
		decision.ContentStrategy = domain.ContentStrategyAttachmentOnly
	}
	m := New(filepath.Join(root, "artifacts"))
	plan, err := m.Plan(context.Background(), domain.Source{ID: "source"}, track, release, normalized, decision, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Materialize(context.Background(), domain.Source{ID: "source"}, track, release, plan); err != nil {
		t.Fatal(err)
	}
	session, err := openEPUBPackage(plan.SelectedContent)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func titles(pkg opfPackage) [][2]string {
	var result [][2]string
	for _, element := range pkg.Metadata.DCElements {
		if element.Name != "title" {
			continue
		}
		role := ""
		for _, attr := range element.Attrs {
			if attr.Name.Local != "id" {
				continue
			}
			for _, meta := range pkg.Metadata.Meta {
				if meta.Refines == "#"+attr.Value && meta.Property == "title-type" {
					role = meta.Value
				}
			}
		}
		result = append(result, [2]string{element.Value, role})
	}
	return result
}

func tocLabels(t *testing.T, session *epubPackage) []string {
	t.Helper()
	entries, err := session.memberNavigation("")
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	var walk func([]epubChapter)
	walk = func(entries []epubChapter) {
		for _, entry := range entries {
			labels = append(labels, entry.Title)
			walk(entry.Children)
		}
	}
	walk(entries)
	return labels
}

func assertTOC(t *testing.T, session *epubPackage, want ...string) {
	t.Helper()
	if got := tocLabels(t, session); fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Fatalf("toc labels = %q, want %q", got, want)
	}
}

func assertTitles(t *testing.T, pkg opfPackage, want ...[2]string) {
	t.Helper()
	got := titles(pkg)
	if len(got) != len(want) {
		t.Fatalf("titles = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("titles = %q, want %q", got, want)
		}
	}
}

func TestChapterTitleIsTheChapterAndKeepsTheReleaseTitleExpanded(t *testing.T) {
	session := planChapter(t, chapterPlanInput{
		series: "Rise of the Living Forge", releaseTitle: "Rise of the Living Forge - Chapter 430",
		sequence: &domain.Sequence{Chapter: 430, Position: 430, MatchedText: "Chapter 430", SeriesIndex: "430"},
		output:   domain.OutputFormatEPUB,
	})
	assertTitles(t, session.Package, [2]string{"Chapter 430", ""}, [2]string{"Rise of the Living Forge - Chapter 430", "expanded"})
	assertTOC(t, session, "Chapter 430")
}

func TestReleaseIdentityTitlesAWrappedOriginalByItsChapter(t *testing.T) {
	session := planChapter(t, chapterPlanInput{
		series: "The Sixth School", releaseTitle: "The Sixth School. Book Two. Chapter 071.",
		sequence: &domain.Sequence{Book: 2, Chapter: 71, Position: 71, MatchedText: "Chapter 071", SeriesIndex: "2.71"},
		output:   domain.OutputFormatEPUB, identity: domain.PublicationIdentityRelease,
		attachment: buildEPUB2Fixture(t), preface: true,
	})
	assertTitles(t, session.Package, [2]string{"Book Two, Chapter 71", ""}, [2]string{"The Sixth School. Book Two. Chapter 071.", "expanded"})
	assertTOC(t, session, "Author's note", "Book Two, Chapter 71")
}

func TestEmbeddedIdentityDropsARepeatedSeriesName(t *testing.T) {
	embedded, err := buildSimpleEPUB("Harbor - Chapter 9: Salt", "Ada", "urn:test:embedded", time.Unix(0, 0).UTC(), []epubChapter{{FileName: "story.xhtml", Title: "Harbor - Chapter 9: Salt", BodyHTML: "<p>Story.</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	session := planChapter(t, chapterPlanInput{
		series: "Harbor", releaseTitle: "Harbor Chapter 9",
		sequence: &domain.Sequence{Chapter: 9, Position: 9, MatchedText: "Chapter 9", SeriesIndex: "9"},
		output:   domain.OutputFormatEPUB, attachment: embedded,
	})
	assertTitles(t, session.Package, [2]string{"Chapter 9: Salt", ""}, [2]string{"Harbor - Chapter 9: Salt", "expanded"})
	assertTOC(t, session, "Chapter 9: Salt")
}

func TestPreserveKeepsTheReleaseTitle(t *testing.T) {
	session := planChapter(t, chapterPlanInput{
		series: "The Sixth School", releaseTitle: "The Sixth School. Book Two. Chapter 071.",
		sequence: &domain.Sequence{Book: 2, Chapter: 71, Position: 71, MatchedText: "Chapter 071", SeriesIndex: "2.71"},
		output:   domain.OutputFormatPreserve, identity: domain.PublicationIdentityRelease,
		attachment: buildEPUB2Fixture(t), preface: true,
	})
	assertTitles(t, session.Package, [2]string{"The Sixth School. Book Two. Chapter 071.", ""})
	assertTOC(t, session, "Author's note", "Chapter")
}

func TestAnAuthorsMultiEntryTableOfContentsKeepsItsLabels(t *testing.T) {
	embedded, err := buildSimpleEPUB("Harbor Chapter 9", "Ada", "urn:test:multi", time.Unix(0, 0).UTC(), []epubChapter{
		{FileName: "one.xhtml", Title: "Start", BodyHTML: "<p>One.</p>"},
		{FileName: "two.xhtml", Title: "Harbor - Interlude", BodyHTML: "<p>Two.</p>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	session := planChapter(t, chapterPlanInput{
		series: "Harbor", releaseTitle: "Harbor Chapter 9",
		sequence: &domain.Sequence{Chapter: 9, Position: 9, MatchedText: "Chapter 9", SeriesIndex: "9"},
		output:   domain.OutputFormatEPUB, attachment: embedded,
	})
	assertTOC(t, session, "Start", "Harbor - Interlude")
}

func bodyMatter(t *testing.T, session *epubPackage) string {
	t.Helper()
	for _, item := range session.Package.Manifest.Items {
		if !strings.Contains(" "+item.Properties+" ", " nav ") {
			continue
		}
		entry, err := session.entry(item.Href)
		if err != nil {
			t.Fatal(err)
		}
		for _, label := range landmarkTargets(t, session.Files[entry], entry) {
			return label
		}
	}
	return ""
}

func landmarkTargets(t *testing.T, data []byte, entry string) []string {
	t.Helper()
	document, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, inLandmarks bool) {
		if node.Type == html.ElementNode {
			types := ""
			href := ""
			for _, attr := range node.Attr {
				switch attr.Key {
				case "epub:type":
					types = attr.Val
				case "href":
					href = attr.Val
				}
			}
			if node.Data == "nav" && strings.Contains(" "+types+" ", " landmarks ") {
				inLandmarks = true
			}
			if inLandmarks && node.Data == "a" && strings.Contains(" "+types+" ", " bodymatter ") {
				target, err := resolveManifestHref(path.Dir(entry), href)
				if err != nil {
					t.Fatal(err)
				}
				targets = append(targets, target)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inLandmarks)
		}
	}
	walk(document, false)
	return targets
}

func spineEntries(t *testing.T, session *epubPackage) []string {
	t.Helper()
	var entries []string
	for _, ref := range session.Package.Spine.Itemrefs {
		for _, item := range session.Package.Manifest.Items {
			if item.ID == ref.IDRef {
				entry, err := session.entry(item.Href)
				if err != nil {
					t.Fatal(err)
				}
				entries = append(entries, entry)
			}
		}
	}
	return entries
}

func assertPassesEPUBCheck(t *testing.T, session *epubPackage) {
	t.Helper()
	content, err := writeEPUBArchive(session.Files)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "chapter.epub")
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
	assertEPUBCheckPasses(t, file)
}

func TestBuiltChapterMarksItsBodyMatter(t *testing.T) {
	session := planChapter(t, chapterPlanInput{
		series: "Rise of the Living Forge", releaseTitle: "Rise of the Living Forge - Chapter 430",
		sequence: &domain.Sequence{Chapter: 430, Position: 430, MatchedText: "Chapter 430", SeriesIndex: "430"},
		output:   domain.OutputFormatEPUB,
	})
	if got := bodyMatter(t, session); got != "OEBPS/chapter-001.xhtml" {
		t.Fatalf("body matter = %q", got)
	}
	assertPassesEPUBCheck(t, session)
}

func TestWrappedEPUB2BecomesEPUB3WithBodyMatterAndAnAuthorsNote(t *testing.T) {
	session := planChapter(t, chapterPlanInput{
		series: "The Sixth School", releaseTitle: "The Sixth School. Book Two. Chapter 071.",
		sequence: &domain.Sequence{Book: 2, Chapter: 71, Position: 71, MatchedText: "Chapter 071", SeriesIndex: "2.71"},
		output:   domain.OutputFormatEPUB, identity: domain.PublicationIdentityRelease,
		attachment: buildEPUB2Fixture(t), preface: true,
	})
	if session.Package.Version != "3.0" {
		t.Fatalf("package version = %q, want 3.0", session.Package.Version)
	}
	if got := fmt.Sprint(spineEntries(t, session)); got != "[serial-sync-preface.xhtml chapter.xhtml]" {
		t.Fatalf("spine = %s; reading positions depend on it staying preface then story", got)
	}
	if got := bodyMatter(t, session); got != "chapter.xhtml" {
		t.Fatalf("body matter = %q", got)
	}
	assertTOC(t, session, "Author's note", "Book Two, Chapter 71")
	assertPassesEPUBCheck(t, session)
}

func TestWrappedEPUB3GainsBodyMatterAndAnAuthorsNote(t *testing.T) {
	original, err := buildSimpleEPUB("Harbor Chapter 9", "Ada", "urn:test:wrapped3", time.Unix(0, 0).UTC(), []epubChapter{{FileName: "story.xhtml", Title: "Start", BodyHTML: "<p>Story.</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	session := planChapter(t, chapterPlanInput{
		series: "Harbor", releaseTitle: "Harbor Chapter 9",
		sequence: &domain.Sequence{Chapter: 9, Position: 9, MatchedText: "Chapter 9", SeriesIndex: "9"},
		output:   domain.OutputFormatEPUB, attachment: original, preface: true,
	})
	if got := bodyMatter(t, session); got != "OEBPS/story.xhtml" {
		t.Fatalf("body matter = %q", got)
	}
	assertTOC(t, session, "Author's note", "Chapter 9")
	assertPassesEPUBCheck(t, session)
}
