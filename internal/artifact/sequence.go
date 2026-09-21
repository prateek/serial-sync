package artifact

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

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
