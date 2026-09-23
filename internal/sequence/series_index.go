package sequence

import (
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

// IndexedRelease is one release of a series as the series index sees it: its
// resolved sequence and the title it was detected from.
type IndexedRelease struct {
	ReleaseID   string
	PublishedAt time.Time
	Title       string
	Sequence    domain.Sequence
}

// The series index is the reader-facing sort key, distinct from Position,
// which stays the declared-range scalar used for volume coverage. It follows
// the author's numbering: the chapter number, or book.chapter when numbering
// restarts per book. A release the author did not number (an interlude,
// bonus, epilogue or half chapter inside a book) repeats the label of the
// release before it, so the reader breaks the tie by publish date. BookOrbit
// accepts only ^\d+(\.\d+)?$, so letter suffixes are not an option.
//
// Every label depends only on the release and the releases published before
// it, so a new release never relabels an earlier one.

var (
	bookMarker     = regexp.MustCompile(`(?i)\bB(\d{1,2})\s*(?:[-:]\s*)?(?:C(?:h(?:apter)?)?\.?\s*(\d+)\b|(?:prelude|prologue|epilogue|interlude)\b)`)
	pluralChapters = regexp.MustCompile(`(?i)\bchapters\s+(\d+)\b`)
	dottedChapter  = regexp.MustCompile(`^\D*?\b((\d{1,2})\.(\d{1,3}))(?:[\s:–-]|$)`)
	decimalLabel   = regexp.MustCompile(`^(\d+)\.(\d+)$`)
	leadingNumber  = regexp.MustCompile(`^(\d+)`)
)

type indexCoordinates struct {
	book, chapter, part int
	// half is a positive non-integral chapter label such as "5.5".
	half string
	// dotted is the author's "Title 3.1" shorthand, kept as written.
	dotted string
}

func (c indexCoordinates) numbered() bool { return c.chapter > 0 || c.dotted != "" }

func coordinatesOf(release IndexedRelease) indexCoordinates {
	seq := release.Sequence
	c := indexCoordinates{book: seq.Book, chapter: seq.Chapter}
	if part, err := strconv.Atoi(seq.Part); err == nil && part > 0 {
		c.part = part
	}
	if match := bookMarker.FindStringSubmatch(release.Title); match != nil && c.book == 0 {
		c.book, _ = strconv.Atoi(match[1])
		if c.chapter == 0 && match[2] != "" {
			c.chapter, _ = strconv.Atoi(match[2])
		}
	}
	if c.chapter > 0 {
		return c
	}
	if seq.ChapterLabel == "" {
		if match := pluralChapters.FindStringSubmatch(release.Title); match != nil {
			c.chapter, _ = strconv.Atoi(match[1])
			return c
		}
	}
	// A bare "3.1" is the author's book.chapter; "Chapter 3.1" is a half
	// chapter and keeps its label.
	if c.book == 0 && (seq.ChapterLabel == "" || seq.MatchedText == seq.ChapterLabel) {
		if match := dottedChapter.FindStringSubmatch(release.Title); match != nil {
			c.book, _ = strconv.Atoi(match[2])
			c.chapter, _ = strconv.Atoi(match[3])
			c.dotted = match[1]
			return c
		}
	}
	if match := decimalLabel.FindStringSubmatch(seq.ChapterLabel); match != nil {
		if whole, _ := strconv.Atoi(match[1]); whole > 0 {
			c.chapter, c.half = whole, seq.ChapterLabel
		}
	} else if match := leadingNumber.FindStringSubmatch(seq.ChapterLabel); match != nil {
		c.chapter, _ = strconv.Atoi(match[1])
	}
	return c
}

type indexMode int

const (
	chapterMode indexMode = iota
	partMode
	ordinalMode
)

// SeriesIndexes labels every release of one series. The series uses chapter
// numbers when at least half its releases carry one, part numbers when parts
// are what it counts, and release order otherwise (an anthology). byRelease
// forces release order for an anthology whose stories carry their own
// numbers.
func SeriesIndexes(releases []IndexedRelease, byRelease bool) map[string]string {
	ordered := append([]IndexedRelease(nil), releases...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].PublishedAt.Equal(ordered[j].PublishedAt) {
			return ordered[i].ReleaseID < ordered[j].ReleaseID
		}
		return ordered[i].PublishedAt.Before(ordered[j].PublishedAt)
	})
	coords := make([]indexCoordinates, len(ordered))
	chapters, parts := 0, 0
	for i, release := range ordered {
		coords[i] = coordinatesOf(release)
		switch {
		case coords[i].numbered():
			chapters++
		case coords[i].part > 0:
			parts++
		}
	}
	mode := ordinalMode
	switch {
	case byRelease:
	case chapters*2 >= len(ordered):
		mode = chapterMode
	case parts > chapters && (chapters+parts)*2 >= len(ordered):
		mode = partMode
	}
	perBook := mode == chapterMode && restartsPerBook(coords)

	indexes := make(map[string]string, len(ordered))
	last := ""
	book := 1
	for i, release := range ordered {
		c := coords[i]
		label := ""
		switch mode {
		case ordinalMode:
			label = strconv.Itoa(i + 1)
		case partMode:
			if c.part > 0 {
				label = strconv.Itoa(c.part)
			}
		case chapterMode:
			if !perBook {
				switch {
				case c.half != "":
					label = c.half
				case c.chapter > 0:
					label = strconv.Itoa(c.chapter)
				}
				break
			}
			switch {
			case c.dotted != "":
				label = c.dotted
				book = c.book
			case c.book > 0 && c.chapter > 0:
				label = strconv.Itoa(c.book) + "." + strconv.Itoa(c.chapter)
				book = c.book
			case c.chapter > 0:
				label = strconv.Itoa(book) + "." + strconv.Itoa(c.chapter)
			case c.book > 0 && c.book != book:
				// An unnumbered release that opens a book (a prologue)
				// sorts before its chapter 1.
				book = c.book
				label = strconv.Itoa(book) + ".0"
			case last == "":
				label = strconv.Itoa(book) + ".0"
			}
		}
		if label == "" {
			label = last
			if label == "" {
				label = "0"
			}
		}
		indexes[release.ReleaseID] = label
		last = label
	}
	return indexes
}

// restartsPerBook reports whether the author numbers chapters per book:
// either the dotted book.chapter shorthand, or a later book whose chapters
// start again at or below a number an earlier book already reached. Global
// numbering that merely announces a new book ("Chapter 378 & the start of
// Book 5") does not count.
func restartsPerBook(coords []indexCoordinates) bool {
	highest := 0
	book := 0
	for _, c := range coords {
		if c.dotted != "" {
			return true
		}
		if c.chapter == 0 {
			continue
		}
		if c.book > book {
			if book > 0 || highest > 0 {
				if c.chapter <= highest {
					return true
				}
			}
			book = c.book
		}
		highest = max(highest, c.chapter)
	}
	return false
}
