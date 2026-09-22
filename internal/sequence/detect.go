package sequence

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/prateek/serial-sync/internal/domain"
)

// Detect is the single sequence detector. Title parsing belongs to this
// module: artifact filenames and discovery features both read it, and neither
// may depend on the other.

var explicitChapter = regexp.MustCompile(`(?i)\b(?:chapter|chap|ch|chaper|chaptger|chapeter)\.?\s*:?\s*(-?\d+(?:\.\d+)?[a-z]?)\b`)
var compactSequence = regexp.MustCompile(`(?i)\bB(\d+)C(-?\d+(?:\.\d+)?[a-z]?)\b`)
var namedPart = regexp.MustCompile(`(?i)\[part\s+([a-z]+\d+|\d+[a-z]?)\]`)
var bareChapter = regexp.MustCompile(`^\s*(-?\d+(?:\.\d+)?[a-z]?)(?:\s*[-:]\s+|\s*$)`)
var trailingDecimal = regexp.MustCompile(`(?:^|\s)(\d+\.\d+[a-z]?)\s*$`)

func parseSequenceInfo(text string) sequenceInfo {
	switch strings.ToLower(filepath.Ext(text)) {
	case ".epub", ".pdf", ".html", ".txt", ".mobi":
		text = strings.TrimSuffix(text, filepath.Ext(text))
	}
	info := parseChapterIdentity(text)
	if match := namedPart.FindStringSubmatchIndex(text); match != nil {
		info.Part = strings.ToUpper(text[match[2]:match[3]])
		first, last := match[0], match[1]
		if info.MatchedText != "" {
			if start := strings.Index(text, info.MatchedText); start >= 0 {
				first = min(first, start)
				last = max(last, start+len(info.MatchedText))
			}
		}
		info.MatchedText = text[first:last]
		info.KeepSingle = true
		info.Reason = "part marker requires an explicit chapter override for volume coverage"
	}
	return info
}

func parseChapterIdentity(text string) sequenceInfo {
	info := parseWordSequence(text)
	if match := compactSequence.FindStringSubmatch(text); match != nil {
		info.Book, _ = strconv.Atoi(match[1])
		return chapterIdentity(info, match[2], match[0])
	}
	if match := explicitChapter.FindStringSubmatch(text); match != nil {
		label := match[1]
		start := len(match[0]) - len(label)
		// Filenames use chapter-50 as a separator; Chapter -50 is a signed number.
		if strings.HasPrefix(label, "-") && start > 0 && (unicode.IsLetter(rune(match[0][start-1])) || match[0][start-1] == '.') {
			label = strings.TrimPrefix(label, "-")
		}
		return chapterIdentity(info, label, match[0])
	}
	if info.HasChapter() {
		return info
	}
	for _, pattern := range []*regexp.Regexp{bareChapter, trailingDecimal} {
		if match := pattern.FindStringSubmatch(text); match != nil {
			return chapterIdentity(info, match[1], strings.TrimSpace(match[0]))
		}
	}
	return info
}

func chapterIdentity(info sequenceInfo, label, matched string) sequenceInfo {
	info.MatchedText = matched
	if number, err := strconv.Atoi(label); err == nil && number > 0 {
		info.Chapter = number
		return info
	}
	info.Chapter = 0
	info.ChapterLabel = label
	info.KeepSingle = true
	info.Reason = "non-integral or non-positive chapter requires an explicit override for volume coverage"
	return info
}

var filenameTokenPattern = regexp.MustCompile(`[A-Za-z0-9]+`)

type sequenceInfo = domain.Sequence

func Detect(texts ...string) domain.Sequence { return detectSequenceInfo(texts...) }

func detectSequenceInfo(texts ...string) sequenceInfo {
	info := sequenceInfo{}
	for _, text := range texts {
		parsed := parseSequenceInfo(text)
		if info.Book == 0 && parsed.Book > 0 {
			info.Book = parsed.Book
		}
		if !info.HasChapter() && info.Part == "" && (parsed.HasChapter() || parsed.Part != "") {
			book := info.Book
			info = parsed
			if book > 0 {
				info.Book = book
			}
		}
		if info.Part == "" && parsed.Part != "" {
			info.Part = parsed.Part
			info.KeepSingle = true
			info.Reason = parsed.Reason
		}
	}
	return info
}

func parseWordSequence(text string) sequenceInfo {
	spans := filenameTokenPattern.FindAllStringIndex(text, -1)
	tokens := make([]string, len(spans))
	for i, span := range spans {
		tokens[i] = strings.ToLower(text[span[0]:span[1]])
	}
	info := sequenceInfo{}
	for idx, token := range tokens {
		switch token {
		case "book", "bk", "volume", "vol":
			if number, consumed, ok := parseNumberTokens(tokens[idx+1:]); ok && info.Book == 0 {
				info.Book = number
				idx += consumed
			}
		case "chapter", "chap", "ch", "chaper", "chaptger", "chapeter":
			if number, consumed, ok := parseNumberTokens(tokens[idx+1:]); ok && info.Chapter == 0 {
				info.Chapter = number
				info.MatchedText = text[spans[idx][0]:spans[idx+consumed][1]]
			} else if idx > 0 {
				if number, ok := parseSimpleNumber(tokens[idx-1]); ok && info.Chapter == 0 {
					info.Chapter = number
					info.MatchedText = text[spans[idx-1][0]:spans[idx][1]]
				}
			}
		}
	}
	return info
}

func parseNumberTokens(tokens []string) (int, int, bool) {
	if len(tokens) == 0 {
		return 0, 0, false
	}
	if number, ok := parseSimpleNumber(tokens[0]); ok {
		return number, 1, true
	}

	total := 0
	current := 0
	consumed := 0
	matched := false
	for _, token := range tokens {
		switch token {
		case "and":
			if matched {
				consumed++
				continue
			}
			return 0, 0, false
		case "hundred":
			if !matched {
				return 0, 0, false
			}
			if current == 0 {
				current = 1
			}
			current *= 100
			consumed++
		case "thousand":
			if !matched {
				return 0, 0, false
			}
			if current == 0 {
				current = 1
			}
			total += current * 1000
			current = 0
			consumed++
		default:
			value, ok := numberWordValue(token)
			if !ok {
				if matched {
					return total + current, consumed, true
				}
				return 0, 0, false
			}
			current += value
			consumed++
			matched = true
		}
	}
	if !matched {
		return 0, 0, false
	}
	return total + current, consumed, true
}

func parseSimpleNumber(token string) (int, bool) {
	if token == "" {
		return 0, false
	}
	if digits, err := strconv.Atoi(token); err == nil {
		return digits, true
	}
	value, ok := ordinalWordValue(token)
	if ok {
		return value, true
	}
	return 0, false
}

func numberWordValue(token string) (int, bool) {
	switch token {
	case "zero":
		return 0, true
	case "one":
		return 1, true
	case "two":
		return 2, true
	case "three":
		return 3, true
	case "four":
		return 4, true
	case "five":
		return 5, true
	case "six":
		return 6, true
	case "seven":
		return 7, true
	case "eight":
		return 8, true
	case "nine":
		return 9, true
	case "ten":
		return 10, true
	case "eleven":
		return 11, true
	case "twelve":
		return 12, true
	case "thirteen":
		return 13, true
	case "fourteen":
		return 14, true
	case "fifteen":
		return 15, true
	case "sixteen":
		return 16, true
	case "seventeen":
		return 17, true
	case "eighteen":
		return 18, true
	case "nineteen":
		return 19, true
	case "twenty":
		return 20, true
	case "thirty":
		return 30, true
	case "forty":
		return 40, true
	case "fifty":
		return 50, true
	case "sixty":
		return 60, true
	case "seventy":
		return 70, true
	case "eighty":
		return 80, true
	case "ninety":
		return 90, true
	default:
		return 0, false
	}
}

func ordinalWordValue(token string) (int, bool) {
	switch token {
	case "first":
		return 1, true
	case "second":
		return 2, true
	case "third":
		return 3, true
	case "fourth":
		return 4, true
	case "fifth":
		return 5, true
	case "sixth":
		return 6, true
	case "seventh":
		return 7, true
	case "eighth":
		return 8, true
	case "ninth":
		return 9, true
	case "tenth":
		return 10, true
	default:
		return 0, false
	}
}
