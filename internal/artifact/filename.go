package artifact

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/prateek/serial-sync/internal/domain"
)

var filenameTokenPattern = regexp.MustCompile(`[A-Za-z0-9]+`)

type sequenceInfo struct {
	Book    int
	Chapter int
}

func canonicalFileName(track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, originalFileName, mimeType string) string {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(originalFileName)))
	if ext == "" {
		ext = extensionForMime(mimeType)
	}

	trackName := normalizeFileComponent(track.TrackName)
	if trackName == "" {
		trackName = normalizeFileComponent(track.TrackKey)
	}
	if trackName == "" {
		trackName = "Release"
	}

	info := detectSequenceInfo(normalized.Title, originalFileName)
	releaseToken := releaseFileToken(release.ProviderReleaseID)
	if info.Chapter > 0 {
		parts := []string{trackName}
		if info.Book > 0 {
			parts = append(parts, fmt.Sprintf("Bk%02d", info.Book))
		}
		parts = append(parts, fmt.Sprintf("Ch%05d", info.Chapter), releaseToken)
		return strings.Join(parts, " - ") + ext
	}

	parts := []string{trackName}
	if !release.PublishedAt.IsZero() {
		parts = append(parts, release.PublishedAt.UTC().Format("2006-01-02"))
	}
	if titlePart := normalizeFileComponent(normalized.Title); titlePart != "" && !strings.EqualFold(titlePart, trackName) {
		parts = append(parts, titlePart)
	}
	parts = append(parts, releaseToken)
	return strings.Join(parts, " - ") + ext
}

func detectSequenceInfo(texts ...string) sequenceInfo {
	info := sequenceInfo{}
	for _, text := range texts {
		parsed := parseSequenceInfo(text)
		if info.Book == 0 && parsed.Book > 0 {
			info.Book = parsed.Book
		}
		if info.Chapter == 0 && parsed.Chapter > 0 {
			info.Chapter = parsed.Chapter
		}
		if info.Book > 0 && info.Chapter > 0 {
			return info
		}
	}
	return info
}

func parseSequenceInfo(text string) sequenceInfo {
	tokens := filenameTokenPattern.FindAllString(strings.ToLower(text), -1)
	info := sequenceInfo{}
	for idx, token := range tokens {
		switch token {
		case "book", "bk", "volume", "vol":
			if number, consumed, ok := parseNumberTokens(tokens[idx+1:]); ok && info.Book == 0 {
				info.Book = number
				idx += consumed
			}
		case "chapter", "chap", "ch":
			if idx > 0 {
				if number, ok := parseSimpleNumber(tokens[idx-1]); ok && info.Chapter == 0 {
					info.Chapter = number
				}
			}
			if number, _, ok := parseNumberTokens(tokens[idx+1:]); ok && info.Chapter == 0 {
				info.Chapter = number
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

func normalizeFileComponent(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", " -", "\n", " ", "\r", " ", "\"", "", "*", "", "?", "", "<", "", ">", "", "|", "")
	input = replacer.Replace(input)
	fields := strings.FieldsFunc(input, func(r rune) bool {
		return unicode.IsControl(r)
	})
	cleaned := strings.Join(fields, " ")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return strings.Trim(cleaned, " .-")
}

func releaseFileToken(providerReleaseID string) string {
	token := normalizeFileComponent(providerReleaseID)
	token = strings.ReplaceAll(token, " ", "-")
	if token == "" {
		return "release"
	}
	return "r" + token
}

func extensionForMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "application/epub+zip":
		return ".epub"
	case "application/pdf":
		return ".pdf"
	case "text/html":
		return ".html"
	default:
		return ".bin"
	}
}
