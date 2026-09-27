package artifact

import (
	"fmt"
	"strings"
	"testing"
)

// pdfXMLText is one positioned run of pdftohtml -xml output.
type pdfXMLText struct {
	top, left, width, height int
	inner                    string
}

func pdfXML(pages ...[]pdfXMLText) []byte {
	var out strings.Builder
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<pdf2xml producer=\"poppler\">\n")
	for i, texts := range pages {
		fmt.Fprintf(&out, `<page number="%d" position="absolute" top="0" left="0" height="1188" width="918">`+"\n", i+1)
		for _, t := range texts {
			fmt.Fprintf(&out, `<text top="%d" left="%d" width="%d" height="%d" font="0">%s</text>`+"\n", t.top, t.left, t.width, t.height, t.inner)
		}
		out.WriteString("</page>\n")
	}
	out.WriteString("</pdf2xml>\n")
	return []byte(out.String())
}

// justifiedLine is a full-width body line in a Word layout with 108pt margins
// and 21pt line pitch; short lines end paragraphs.
func justifiedLine(top int, text string) pdfXMLText {
	return pdfXMLText{top: top, left: 108, width: 707, height: 21, inner: text}
}

func shortLine(top int, text string) pdfXMLText {
	return pdfXMLText{top: top, left: 108, width: 300, height: 21, inner: text}
}

func reflowBody(t *testing.T, raw []byte) string {
	t.Helper()
	body, err := reflowPDFXML(raw)
	if err != nil {
		t.Fatalf("reflowPDFXML: %v", err)
	}
	return body
}

func TestReflowPDFXMLJoinsWrappedLinesAndSplitsOnParagraphGaps(t *testing.T) {
	t.Parallel()
	body := reflowBody(t, pdfXML([]pdfXMLText{
		shortLine(108, "Chapter One: Start"),
		justifiedLine(142, "The first paragraph wraps "),
		justifiedLine(163, "across three lines of "),
		shortLine(185, "text. "),
		justifiedLine(218, "The second paragraph "),
		shortLine(240, "ends here. "),
	}))
	want := "<h2>Chapter One: Start</h2>\n" +
		"<p>The first paragraph wraps across three lines of text.</p>\n" +
		"<p>The second paragraph ends here.</p>\n"
	if body != want {
		t.Fatalf("body:\n%s\nwant:\n%s", body, want)
	}
}

func TestReflowPDFXMLContinuesAParagraphAcrossAPageOnlyAfterAFullLine(t *testing.T) {
	t.Parallel()
	body := reflowBody(t, pdfXML(
		[]pdfXMLText{
			justifiedLine(108, "A long opening paragraph that fills the line and "),
			justifiedLine(130, "keeps going past the page "),
			justifiedLine(163, "Another paragraph that runs to the page end and "),
			justifiedLine(185, "stops at the foot "),
		},
		[]pdfXMLText{
			shortLine(108, "of the page. "),
			justifiedLine(141, "Fresh paragraph that "),
			shortLine(163, "ends short. "),
		},
		[]pdfXMLText{
			shortLine(108, "A new page, a new paragraph. "),
		},
	))
	paragraphs := strings.Split(strings.TrimSpace(body), "\n")
	want := []string{
		"<p>A long opening paragraph that fills the line and keeps going past the page</p>",
		"<p>Another paragraph that runs to the page end and stops at the foot of the page.</p>",
		"<p>Fresh paragraph that ends short.</p>",
		"<p>A new page, a new paragraph.</p>",
	}
	if strings.Join(paragraphs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paragraphs:\n%s\nwant:\n%s", body, strings.Join(want, "\n"))
	}
}

func TestReflowPDFXMLKeepsLineEndHyphensAttached(t *testing.T) {
	t.Parallel()
	body := reflowBody(t, pdfXML([]pdfXMLText{
		justifiedLine(108, "They were spur-of-the-"),
		shortLine(130, "moment shots. "),
	}))
	if !strings.Contains(body, "spur-of-the-moment shots.") {
		t.Fatalf("hyphenated compound split across lines should rejoin without a space:\n%s", body)
	}
}

func TestReflowPDFXMLStartsParagraphsAtFirstLineIndentsInRaggedText(t *testing.T) {
	t.Parallel()
	// A ragged-right novel layout: paragraphs start indented at 162, wrapped
	// lines return to 108, and line pitch is uniform.
	ragged := func(top, left, width int, text string) pdfXMLText {
		return pdfXMLText{top: top, left: left, width: width, height: 20, inner: text}
	}
	body := reflowBody(t, pdfXML([]pdfXMLText{
		ragged(108, 162, 560, "“Lixxar,” Damien said, barely louder than a whisper. "),
		ragged(150, 162, 629, "The cabinet didn’t budge. He said the words again, with more "),
		ragged(192, 108, 90, "conviction. "),
		ragged(234, 162, 646, "The doors popped open with a click that nearly made him "),
		ragged(276, 108, 640, "jump out of his chest, and he grinned at the empty room "),
		ragged(318, 108, 420, "before stepping closer. "),
		ragged(360, 108, 94, "Chapter 1"),
	}))
	want := "<p>“Lixxar,” Damien said, barely louder than a whisper.</p>\n" +
		"<p>The cabinet didn’t budge. He said the words again, with more conviction.</p>\n" +
		"<p>The doors popped open with a click that nearly made him jump out of his chest, and he grinned at the empty room before stepping closer.</p>\n" +
		"<p>Chapter 1</p>\n"
	if body != want {
		t.Fatalf("body:\n%s\nwant:\n%s", body, want)
	}
}

func TestReflowPDFXMLKeepsNumberedListMarkersOnTheirLine(t *testing.T) {
	t.Parallel()
	// Word numbered paragraphs: the marker, a separator space set in another
	// font a few points lower, then the indented text body.
	body := reflowBody(t, pdfXML([]pdfXMLText{
		{top: 108, left: 135, width: 25, height: 35, inner: "1."},
		{top: 116, left: 160, width: 8, height: 28, inner: " "},
		{top: 108, left: 162, width: 440, height: 35, inner: "Chapter One: A New Beginning"},
		{top: 204, left: 135, width: 25, height: 35, inner: "2."},
		{top: 212, left: 160, width: 8, height: 28, inner: " "},
		{top: 204, left: 162, width: 656, height: 35, inner: "Deep in the mountains, in a remote village with "},
		{top: 240, left: 162, width: 657, height: 35, inner: "only one road in and out of it, a young man was "},
		{top: 277, left: 162, width: 656, height: 35, inner: "staring dazedly at a line of ants crawling on a "},
		{top: 313, left: 162, width: 398, height: 35, inner: "wall beside the bed. "},
	}))
	want := "<h2>1. Chapter One: A New Beginning</h2>\n" +
		"<p>2. Deep in the mountains, in a remote village with only one road in and out of it, a young man was staring dazedly at a line of ants crawling on a wall beside the bed.</p>\n"
	if body != want {
		t.Fatalf("body:\n%s\nwant:\n%s", body, want)
	}
}

func TestReflowPDFXMLDropsPageNumbersAndKeepsInlineEmphasis(t *testing.T) {
	t.Parallel()
	body := reflowBody(t, pdfXML(
		[]pdfXMLText{
			{top: 40, left: 440, width: 20, height: 21, inner: "Page 1"},
			justifiedLine(108, "Salt &amp; <i>iron</i> kept the "),
			shortLine(130, "door shut &lt;tight&gt;. "),
			{top: 1120, left: 450, width: 12, height: 21, inner: "1"},
		},
	))
	want := "<p>Salt &amp; <i>iron</i> kept the door shut &lt;tight&gt;.</p>\n"
	if body != want {
		t.Fatalf("body:\n%s\nwant:\n%s", body, want)
	}
}

func TestReflowPDFXMLOrdersRunsLeftToRightWithinALine(t *testing.T) {
	t.Parallel()
	body := reflowBody(t, pdfXML([]pdfXMLText{
		{top: 108, left: 400, width: 415, height: 21, inner: "the second half of the line "},
		{top: 108, left: 108, width: 292, height: 21, inner: "The first half and "},
		shortLine(130, "then the end. "),
	}))
	if !strings.Contains(body, "The first half and the second half of the line then the end.") {
		t.Fatalf("runs should read left to right:\n%s", body)
	}
}

func TestReflowPDFXMLRejectsImages(t *testing.T) {
	t.Parallel()
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<pdf2xml producer="poppler">
<page number="1" position="absolute" top="0" left="0" height="1188" width="918">
<image top="108" left="108" width="200" height="200" src="page-1_1.png"/>
<text top="330" left="108" width="300" height="21" font="0">Caption. </text>
</page>
</pdf2xml>`)
	if _, err := reflowPDFXML(raw); err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("expected an image error, got %v", err)
	}
}

func TestReflowPDFXMLStartsANumberedItemEvenAfterAFullLine(t *testing.T) {
	t.Parallel()
	numbered := func(top int, marker, text string, width int) []pdfXMLText {
		return []pdfXMLText{
			{top: top, left: 135, width: 25, height: 35, inner: marker},
			{top: top + 8, left: 160, width: 8, height: 28, inner: " "},
			{top: top, left: 162, width: width, height: 35, inner: text},
		}
	}
	body := reflowBody(t, pdfXML(
		append(append(numbered(108, "15.", "He waited for an answer that did not ", 656),
			pdfXMLText{top: 144, left: 162, width: 657, height: 35, inner: "come, and the silence ran all the way to the "},
			pdfXMLText{top: 180, left: 162, width: 657, height: 35, inner: "edge of the room and back to the door again. "}),
			numbered(216, "16.", "Current count: 0.", 150)...),
		append(numbered(108, "17.", "Another part of Greg, however, ", 656),
			pdfXMLText{top: 144, left: 162, width: 300, height: 35, inner: "stayed quiet. "}),
	))
	want := "<p>15. He waited for an answer that did not come, and the silence ran all the way to the edge of the room and back to the door again.</p>\n" +
		"<p>16. Current count: 0.</p>\n" +
		"<p>17. Another part of Greg, however, stayed quiet.</p>\n"
	if body != want {
		t.Fatalf("body:\n%s\nwant:\n%s", body, want)
	}
}

func TestReflowPDFXMLLeavesAnOpeningLineOfDialogueAsAParagraph(t *testing.T) {
	t.Parallel()
	for _, opening := range []string{"“Got you!”", "“Stop!” Came the stern order from Olivia.", "1. “Roka… Roka…”"} {
		body := reflowBody(t, pdfXML([]pdfXMLText{
			shortLine(108, opening),
			justifiedLine(142, "The story carries on in a paragraph that wraps "),
			shortLine(163, "onto a second line. "),
		}))
		if strings.Contains(body, "<h2>") {
			t.Fatalf("opening %q should stay a paragraph:\n%s", opening, body)
		}
	}
}
