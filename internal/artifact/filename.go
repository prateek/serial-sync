package artifact

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/prateek/serial-sync/internal/domain"
)

// canonicalFileName builds the final file name from the profiled name, type
// and the sequence the decider resolved. A nil sequence means no chapter
// identity, which falls back to the dated title form.
func canonicalFileName(track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, originalFileName, mimeType string, seq *domain.Sequence) string {
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

	info := domain.Sequence{}
	if seq != nil {
		info = *seq
	}
	if info.HasChapter() || info.Part != "" {
		parts := []string{trackName}
		if info.Book > 0 {
			parts = append(parts, fmt.Sprintf("Bk%02d", info.Book))
		}
		switch {
		case info.ChapterLabel != "":
			parts = append(parts, "Ch"+strings.ReplaceAll(info.ChapterLabel, "-", "minus"))
		case info.Chapter > 0:
			parts = append(parts, fmt.Sprintf("Ch%04d", info.Chapter))
		}
		if info.Part != "" {
			parts = append(parts, "Part", info.Part)
		}
		return joinSlugged(parts...) + ext
	}

	parts := []string{trackName}
	if !release.PublishedAt.IsZero() {
		parts = append(parts, release.PublishedAt.UTC().Format("2006-01-02"))
	}
	if titlePart := normalizeFileComponent(normalized.Title); titlePart != "" && !strings.EqualFold(titlePart, trackName) {
		parts = append(parts, titlePart)
	}
	return joinSlugged(parts...) + ext
}

func joinSlugged(parts ...string) string {
	slugged := make([]string, 0, len(parts))
	for _, part := range parts {
		token := slug(part)
		if token == "" {
			continue
		}
		slugged = append(slugged, token)
	}
	if len(slugged) == 0 {
		return "release"
	}
	return strings.Join(slugged, "-")
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
