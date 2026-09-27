package artifact

import (
	"bytes"
	"cmp"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// pdfBodyHTML extracts a PDF's positioned text with poppler's pdftohtml and
// reflows it into chapter body HTML. Calibre's PDF input runs the same
// extractor; the reflow here replaces its Python heuristics.
func pdfBodyHTML(ctx context.Context, pdfContent []byte) (string, error) {
	extractorPath, err := exec.LookPath("pdftohtml")
	if err != nil {
		return "", fmt.Errorf("pdftohtml (poppler-utils) is required for PDF to EPUB conversion: %w", err)
	}

	workDir, err := os.MkdirTemp("", "serial-sync-pdf-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(workDir)

	inputPath := filepath.Join(workDir, "input.pdf")
	if err := os.WriteFile(inputPath, pdfContent, 0o644); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, extractorPath, "-xml", "-enc", "UTF-8", "-nodrm", "-q", inputPath, filepath.Join(workDir, "output"))
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("pdftohtml failed: %w: %s", err, string(output))
	}
	raw, err := os.ReadFile(filepath.Join(workDir, "output.xml"))
	if err != nil {
		return "", err
	}
	return reflowPDFXML(raw)
}

type pdfLine struct {
	page        int
	top         float64
	left, right float64
	html, plain string
	listItem    bool
}

type pdfRun struct {
	top, left, width, height float64
	html, plain              string
}

var (
	pdfPageNumberPattern = regexp.MustCompile(`(?i)^\s*(page\s*)?\d+(\s*(of|/)\s*\d+)?\s*$`)
	pdfSpacePattern      = regexp.MustCompile(`\s+`)
	pdfListMarkerPattern = regexp.MustCompile(`^\s*\d{1,4}[.)]\s*$`)
	pdfListPrefixPattern = regexp.MustCompile(`^\s*\d{1,4}[.)]\s+`)
)

// reflowPDFXML turns pdftohtml -xml output into <h2>/<p> body HTML. Word
// exports mark paragraphs with a wider line gap, a first-line indent, or a
// short final line; a paragraph continues onto the next page only when the
// page's last line runs full width. Within a page, a short line ends a
// paragraph only in layouts without gap spacing, where it is the sole signal:
// gap-spaced text also has short lines at manual line breaks.
func reflowPDFXML(raw []byte) (string, error) {
	lines, err := pdfXMLLines(raw)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", errors.New("pdf has no extractable text")
	}

	bodyLeft := modalPDFLeft(lines)
	maxRight := 0.0
	var pitches []float64
	for i, line := range lines {
		maxRight = math.Max(maxRight, line.right)
		if i > 0 && lines[i-1].page == line.page && line.top > lines[i-1].top {
			pitches = append(pitches, line.top-lines[i-1].top)
		}
	}
	width := maxRight - bodyLeft
	pitch := median(pitches)
	nearRight, wideGaps := 0, 0
	for _, line := range lines {
		if line.right >= maxRight-0.02*width {
			nearRight++
		}
	}
	for _, gap := range pitches {
		if gap > pitch*1.3 {
			wideGaps++
		}
	}
	justified := float64(nearRight) >= 0.4*float64(len(lines))
	gapSeparated := len(pitches) > 0 && float64(wideGaps) >= 0.05*float64(len(pitches))
	full := func(line pdfLine) bool {
		if justified {
			return line.right >= maxRight-0.03*width
		}
		return line.right >= maxRight-0.25*width
	}

	var paragraphs [][]pdfLine
	for i, line := range lines {
		if i == 0 || startsPDFParagraph(lines[i-1], line, bodyLeft, pitch, gapSeparated, full) {
			paragraphs = append(paragraphs, nil)
		}
		paragraphs[len(paragraphs)-1] = append(paragraphs[len(paragraphs)-1], line)
	}

	var body strings.Builder
	for i, paragraph := range paragraphs {
		content := joinPDFLines(paragraph)
		if content == "" {
			continue
		}
		tag := "p"
		if i == 0 && len(paragraph) == 1 && !full(paragraph[0]) && pdfHeadingText(paragraph[0].plain) {
			tag = "h2"
		}
		fmt.Fprintf(&body, "<%s>%s</%s>\n", tag, content, tag)
	}
	return body.String(), nil
}

// pdfHeadingText rejects opening lines that read as story text: dialogue, or
// a sentence ending in terminal punctuation.
func pdfHeadingText(plain string) bool {
	text := strings.TrimSpace(pdfListPrefixPattern.ReplaceAllString(plain, ""))
	if text == "" || len([]rune(text)) > 120 {
		return false
	}
	if strings.ContainsRune("“\"‘'", []rune(text)[0]) {
		return false
	}
	return !strings.ContainsRune(".!?”\"’", []rune(text)[len([]rune(text))-1])
}

func startsPDFParagraph(prev, line pdfLine, bodyLeft, pitch float64, gapSeparated bool, full func(pdfLine) bool) bool {
	if line.listItem || line.left > bodyLeft+15 {
		return true
	}
	if prev.page != line.page {
		return !full(prev)
	}
	if line.top-prev.top > pitch*1.3 {
		return true
	}
	return !gapSeparated && !full(prev)
}

func joinPDFLines(lines []pdfLine) string {
	var out strings.Builder
	for i, line := range lines {
		if i > 0 {
			prev := strings.TrimRight(lines[i-1].plain, " ")
			if prev == lines[i-1].plain && !strings.HasSuffix(prev, "-") && !strings.HasPrefix(line.plain, " ") {
				out.WriteByte(' ')
			}
		}
		out.WriteString(line.html)
	}
	return strings.TrimSpace(pdfSpacePattern.ReplaceAllString(out.String(), " "))
}

func pdfXMLLines(raw []byte) ([]pdfLine, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var (
		lines      []pdfLine
		pageNumber int
		pageHeight float64
		runs       []pdfRun
	)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse pdftohtml xml: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "page":
				pageNumber++
				pageHeight = xmlFloatAttr(element, "height")
				runs = nil
			case "image":
				return nil, fmt.Errorf("pdf page %d has an image; PDF conversion supports text-only chapters", pageNumber)
			case "text":
				run, err := readPDFRun(decoder, element)
				if err != nil {
					return nil, err
				}
				runs = append(runs, run)
			}
		case xml.EndElement:
			if element.Name.Local == "page" {
				lines = append(lines, pdfPageLines(pageNumber, pageHeight, runs)...)
			}
		}
	}
	return lines, nil
}

func readPDFRun(decoder *xml.Decoder, start xml.StartElement) (pdfRun, error) {
	run := pdfRun{
		top:    xmlFloatAttr(start, "top"),
		left:   xmlFloatAttr(start, "left"),
		width:  xmlFloatAttr(start, "width"),
		height: xmlFloatAttr(start, "height"),
	}
	var html, plain strings.Builder
	for depth := 1; depth > 0; {
		token, err := decoder.Token()
		if err != nil {
			return pdfRun{}, fmt.Errorf("parse pdftohtml text: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			if element.Name.Local == "i" || element.Name.Local == "b" {
				html.WriteString("<" + element.Name.Local + ">")
			}
		case xml.EndElement:
			depth--
			if depth > 0 && (element.Name.Local == "i" || element.Name.Local == "b") {
				html.WriteString("</" + element.Name.Local + ">")
			}
		case xml.CharData:
			html.WriteString(escapeHTML(string(element)))
			plain.Write(element)
		}
	}
	run.html, run.plain = html.String(), plain.String()
	return run, nil
}

// pdfPageLines groups a page's runs into lines: a run joins the current line
// when it starts within half the line's height, which keeps Word's lowered
// list-separator spaces on their line.
func pdfPageLines(page int, pageHeight float64, runs []pdfRun) []pdfLine {
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].top != runs[j].top {
			return runs[i].top < runs[j].top
		}
		return runs[i].left < runs[j].left
	})
	var groups [][]pdfRun
	for _, run := range runs {
		if n := len(groups); n > 0 && run.top < groups[n-1][0].top+0.5*groups[n-1][0].height {
			groups[n-1] = append(groups[n-1], run)
			continue
		}
		groups = append(groups, []pdfRun{run})
	}

	var lines []pdfLine
	for _, group := range groups {
		slices.SortStableFunc(group, func(a, b pdfRun) int { return cmp.Compare(a.left, b.left) })
		line := pdfLine{page: page, top: group[0].top, left: math.Inf(1)}
		for i, run := range group {
			line.html += run.html
			line.plain += run.plain
			if strings.TrimSpace(run.plain) == "" {
				continue
			}
			// A Word list marker hangs left of the item's text; measure the
			// indent from the text so items and their wrapped lines align.
			if i == 0 && len(group) > 1 && pdfListMarkerPattern.MatchString(run.plain) {
				line.listItem = true
				continue
			}
			line.left = math.Min(line.left, run.left)
			line.right = math.Max(line.right, run.left+run.width)
		}
		if math.IsInf(line.left, 1) {
			line.left, line.right, line.listItem = group[0].left, group[0].left+group[0].width, false
		}
		if strings.TrimSpace(line.plain) == "" {
			continue
		}
		atEdge := line.top < pageHeight*0.08 || line.top > pageHeight*0.92
		if atEdge && pdfPageNumberPattern.MatchString(line.plain) {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func modalPDFLeft(lines []pdfLine) float64 {
	counts := map[float64]int{}
	for _, line := range lines {
		counts[math.Round(line.left)]++
	}
	best, bestCount := 0.0, -1
	for left, count := range counts {
		if count > bestCount || (count == bestCount && left < best) {
			best, bestCount = left, count
		}
	}
	return best
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}

func xmlFloatAttr(element xml.StartElement, name string) float64 {
	for _, attr := range element.Attr {
		if attr.Name.Local == name {
			value, _ := strconv.ParseFloat(attr.Value, 64)
			return value
		}
	}
	return 0
}
