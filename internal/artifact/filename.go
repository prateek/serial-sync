package artifact

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/sequence"
)

type OutputDescription struct {
	Filename string
	MIMEType string
}

// Describe is the one cheap description of a planned output: its filename and
// MIME type. Plan and both previews share it, so preview names cannot drift
// from materialization.
func Describe(track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, decision domain.TrackDecision) OutputDescription {
	name, mime := "chapter.html", "text/html"
	if classify.SelectContent(normalized, decision).Kind == "attachment" {
		if attachment, ok := classify.SelectAttachment(normalized, decision); ok {
			name, mime = attachment.FileName, attachment.MIMEType
		}
	}
	if decision.OutputFormat == domain.OutputFormatEPUB {
		name, mime = forceExtension(name, ".epub"), "application/epub+zip"
	}
	return OutputDescription{Filename: name, MIMEType: mime}
}

func PreviewFilename(track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, decision domain.TrackDecision) string {
	description := Describe(track, release, normalized, decision)
	return PreviewFilenameFor(track, release, normalized, decision, description)
}

// PreviewFilenameFor is the naming step for a caller that already described
// the output, so Describe runs once.
func PreviewFilenameFor(track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, decision domain.TrackDecision, description OutputDescription) string {
	return canonicalFileName(track, release, normalized, description.Filename, description.MIMEType, decision.Sequence)
}

func canonicalFileName(track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, originalFileName, mimeType string, override ...*domain.Sequence) string {
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

	info := sequence.Detect(normalized.Title, originalFileName)
	if len(override) > 0 && override[0] != nil {
		info = *override[0]
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
