package sequence

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/prateek/serial-sync/internal/domain"
)

// ChapterTitle names a chapter by its place in the author's numbering and
// any subtitle after the chapter marker, never by the series name. The book
// is named only when seq's series index restarts numbering per book.
func ChapterTitle(releaseTitle, seriesName string, seq domain.Sequence) string {
	title := strings.TrimSpace(releaseTitle)
	c := coordinatesOf(IndexedRelease{Title: title, Sequence: seq})
	marker, label := seq.MatchedText, seq.ChapterLabel
	switch {
	case c.dotted != "" && strings.Contains(seq.SeriesIndex, "."):
		marker, label = c.dotted, strconv.Itoa(c.chapter)
	case c.dotted != "":
		marker, label = c.dotted, c.dotted
	case label == "" && seq.Chapter > 0:
		label = strconv.Itoa(seq.Chapter)
	}
	if label == "" {
		if seq.Part == "" || seq.MatchedText == "" {
			return WithoutSeriesName(title, seriesName)
		}
		return joinSubtitle("Part "+seq.Part, afterMarker(title, seq.MatchedText, seriesName))
	}

	rest := afterMarker(title, marker, seriesName)
	noun := "Chapter "
	if match := chapterRangeEnd.FindStringSubmatch(rest); match != nil {
		if start, err := strconv.ParseFloat(label, 64); err == nil {
			if end, err := strconv.ParseFloat(match[1], 64); err == nil && end > start {
				noun, label, rest = "Chapters ", label+"-"+match[1], trimTitleSeparators(rest[len(match[0]):])
			}
		}
	}
	heading := noun + label
	if seq.Part != "" {
		heading += ", Part " + seq.Part
	}
	if book, chapter, ok := strings.Cut(seq.SeriesIndex, "."); ok && c.chapter > 0 && seq.SeriesIndex != c.half && chapter == strconv.Itoa(c.chapter) {
		if number, err := strconv.Atoi(book); err == nil && number > 0 {
			heading = "Book " + bookName(number) + ", " + heading
		}
	}
	return joinSubtitle(heading, rest)
}

var chapterRangeEnd = regexp.MustCompile(`^\s*(?:-|–|&|and|to)\s*(\d+(?:\.\d+)?)\b`)

func afterMarker(title, marker, seriesName string) string {
	at := strings.Index(title, marker)
	if marker == "" || at < 0 {
		return withoutSeriesName(title, seriesName)
	}
	rest := title[at+len(marker):]
	// The marker sat inside the author's parentheses: "Series (Ch. 1-3: Wagers)".
	if strings.Count(title[:at], "(") > strings.Count(title[:at], ")") {
		rest = strings.TrimSuffix(strings.TrimSpace(rest), ")")
	}
	return rest
}

func joinSubtitle(heading, rest string) string {
	if strings.HasPrefix(strings.TrimSpace(rest), "(") || strings.HasPrefix(strings.TrimSpace(rest), "[") {
		return heading + " " + strings.TrimSpace(rest)
	}
	if subtitle := trimTitleSeparators(rest); subtitle != "" {
		return heading + ": " + subtitle
	}
	return heading
}

func withoutSeriesName(title, seriesName string) string {
	seriesName = strings.TrimSpace(seriesName)
	if seriesName == "" || len(title) < len(seriesName) || !strings.EqualFold(title[:len(seriesName)], seriesName) {
		return title
	}
	rest := title[len(seriesName):]
	if next, _ := utf8.DecodeRuneInString(rest); unicode.IsLetter(next) || unicode.IsDigit(next) {
		return title
	}
	return trimTitleSeparators(rest)
}

func trimTitleSeparators(text string) string {
	text = strings.TrimSpace(strings.TrimLeftFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("-–—:.,|&", r)
	}))
	if strings.TrimFunc(text, unicode.IsPunct) == "" {
		return ""
	}
	return text
}

var bookNames = []string{"Zero", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine", "Ten", "Eleven", "Twelve", "Thirteen", "Fourteen", "Fifteen", "Sixteen", "Seventeen", "Eighteen", "Nineteen", "Twenty"}

func bookName(number int) string {
	if number < len(bookNames) {
		return bookNames[number]
	}
	return strconv.Itoa(number)
}

// WithoutSeriesName removes a leading series name from title, keeping the
// title whole when nothing else would remain.
func WithoutSeriesName(title, seriesName string) string {
	title = strings.TrimSpace(title)
	if rest := withoutSeriesName(title, seriesName); rest != "" {
		return rest
	}
	return title
}
