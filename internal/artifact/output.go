package artifact

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/prateek/serial-sync/internal/domain"
)

func applyOutputProfile(track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, decision domain.TrackDecision, content []byte, originalFileName, mimeType string, selectedAttachment bool) ([]byte, string, string, error) {
	outputFormat := decision.OutputFormat
	if outputFormat == "" {
		outputFormat = domain.OutputFormatPreserve
	}

	switch outputFormat {
	case domain.OutputFormatPreserve:
		if shouldPrependPostPreface(decision, mimeType, selectedAttachment, normalized) &&
			(strings.EqualFold(strings.TrimSpace(mimeType), "application/epub+zip") || strings.EqualFold(filepath.Ext(originalFileName), ".epub")) {
			prefaceHTML := renderPrefaceHTML(track, release, normalized)
			epubContent, err := wrapEPUBWithPreface(content, track.TrackName, firstNonEmptyString(track.CanonicalAuthor, normalized.CreatorName), prefaceHTML)
			if err != nil {
				return nil, "", "", err
			}
			return epubContent, forceExtension(originalFileName, ".epub"), "application/epub+zip", nil
		}
		return content, originalFileName, mimeType, nil
	case domain.OutputFormatEPUB:
		prefaceHTML := ""
		if shouldPrependPostPreface(decision, mimeType, selectedAttachment, normalized) {
			prefaceHTML = renderPrefaceHTML(track, release, normalized)
		}
		if strings.EqualFold(strings.TrimSpace(mimeType), "application/epub+zip") || strings.EqualFold(filepath.Ext(originalFileName), ".epub") {
			epubContent, err := wrapEPUBWithPreface(content, track.TrackName, firstNonEmptyString(track.CanonicalAuthor, normalized.CreatorName), prefaceHTML)
			if err != nil {
				return nil, "", "", err
			}
			return epubContent, forceExtension(originalFileName, ".epub"), "application/epub+zip", nil
		}
		if strings.EqualFold(strings.TrimSpace(mimeType), "text/html") {
			chapters := []epubChapter{{
				FileName: "chapter-001.xhtml",
				Title:    normalized.Title,
				BodyHTML: string(content),
			}}
			if prefaceHTML != "" {
				chapters = append([]epubChapter{{
					FileName: "preface.xhtml",
					Title:    "Preface",
					BodyHTML: prefaceHTML,
				}}, chapters...)
			}
			epubContent, err := buildSimpleEPUB(track.TrackName, firstNonEmptyString(track.CanonicalAuthor, normalized.CreatorName), chapters)
			if err != nil {
				return nil, "", "", err
			}
			return epubContent, forceExtension(originalFileName, ".epub"), "application/epub+zip", nil
		}
		return nil, "", "", fmt.Errorf("output format %q is only supported for EPUB attachments or HTML/text sources", outputFormat)
	default:
		return nil, "", "", fmt.Errorf("unsupported output format %q", outputFormat)
	}
}

func shouldPrependPostPreface(decision domain.TrackDecision, mimeType string, selectedAttachment bool, normalized domain.NormalizedRelease) bool {
	if decision.PrefaceMode != domain.PrefaceModePrependPost {
		return false
	}
	if !selectedAttachment {
		return false
	}
	if strings.TrimSpace(normalized.TextHTML) == "" && strings.TrimSpace(normalized.TextPlain) == "" {
		return false
	}
	return strings.TrimSpace(mimeType) != "text/html"
}

func renderPrefaceHTML(track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease) string {
	body := strings.TrimSpace(normalized.TextHTML)
	if body == "" {
		body = "<p>" + escapeHTML(normalized.TextPlain) + "</p>"
	}
	heading := escapeHTML(firstNonEmptyString(track.TrackName, normalized.Title))
	subheading := escapeHTML(release.Title)
	return "<!doctype html>\n<html xmlns=\"http://www.w3.org/1999/xhtml\"><head><meta charset=\"utf-8\"/><title>Preface</title></head><body><section><h1>" + heading + "</h1><h2>" + subheading + "</h2>" + body + "</section></body></html>\n"
}

func forceExtension(fileName, ext string) string {
	base := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if strings.TrimSpace(base) == "" {
		base = "release"
	}
	return base + ext
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
