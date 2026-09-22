package artifact

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/domain"
)

func applyOutputProfile(ctx context.Context, track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, decision domain.TrackDecision, content []byte, originalFileName, mimeType string, selectedAttachment bool) ([]byte, string, string, bool, error) {
	outputFormat := decision.OutputFormat
	if outputFormat == "" {
		outputFormat = domain.OutputFormatPreserve
	}

	switch outputFormat {
	case domain.OutputFormatPreserve:
		if shouldPrependPostPreface(decision, mimeType, selectedAttachment, normalized) &&
			(strings.EqualFold(strings.TrimSpace(mimeType), "application/epub+zip") || strings.EqualFold(filepath.Ext(originalFileName), ".epub")) {
			prefaceHTML := renderPrefaceHTML(track, release, normalized)
			epubContent, err := wrapEPUBWithPreface(content, track.TrackName, firstNonEmptyString(track.CanonicalAuthor, normalized.CreatorName), epubIdentifierForRelease(release, normalized.Title), epubModifiedForRelease(release), prefaceHTML)
			if err != nil {
				return nil, "", "", false, err
			}
			return epubContent, forceExtension(originalFileName, ".epub"), "application/epub+zip", true, nil
		}
		return content, originalFileName, mimeType, false, nil
	case domain.OutputFormatEPUB:
		prefaceHTML := ""
		if shouldPrependPostPreface(decision, mimeType, selectedAttachment, normalized) {
			prefaceHTML = renderPrefaceHTML(track, release, normalized)
		}
		if strings.EqualFold(strings.TrimSpace(mimeType), "application/epub+zip") || strings.EqualFold(filepath.Ext(originalFileName), ".epub") {
			if prefaceHTML == "" {
				return content, forceExtension(originalFileName, ".epub"), "application/epub+zip", false, nil
			}
			epubContent, err := wrapEPUBWithPreface(content, track.TrackName, firstNonEmptyString(track.CanonicalAuthor, normalized.CreatorName), epubIdentifierForRelease(release, normalized.Title), epubModifiedForRelease(release), prefaceHTML)
			if err != nil {
				return nil, "", "", false, err
			}
			return epubContent, forceExtension(originalFileName, ".epub"), "application/epub+zip", true, nil
		}
		if strings.EqualFold(strings.TrimSpace(mimeType), "application/pdf") || strings.EqualFold(filepath.Ext(originalFileName), ".pdf") {
			epubContent, err := convertPDFToEPUB(ctx, content, track.TrackName, firstNonEmptyString(track.CanonicalAuthor, normalized.CreatorName), epubIdentifierForRelease(release, normalized.Title), epubModifiedForRelease(release))
			if err != nil {
				return nil, "", "", false, err
			}
			if prefaceHTML != "" {
				epubContent, err = wrapEPUBWithPreface(epubContent, track.TrackName, firstNonEmptyString(track.CanonicalAuthor, normalized.CreatorName), epubIdentifierForRelease(release, normalized.Title), epubModifiedForRelease(release), prefaceHTML)
				if err != nil {
					return nil, "", "", false, err
				}
			}
			return epubContent, forceExtension(originalFileName, ".epub"), "application/epub+zip", true, nil
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
			epubContent, err := buildSimpleEPUB(track.TrackName, firstNonEmptyString(track.CanonicalAuthor, normalized.CreatorName), epubIdentifierForRelease(release, normalized.Title), epubModifiedForRelease(release), chapters)
			if err != nil {
				return nil, "", "", false, err
			}
			return epubContent, forceExtension(originalFileName, ".epub"), "application/epub+zip", true, nil
		}
		return nil, "", "", false, fmt.Errorf("output format %q is only supported for EPUB attachments or HTML/text sources", outputFormat)
	default:
		return nil, "", "", false, fmt.Errorf("unsupported output format %q", outputFormat)
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
	return "<section><h1>" + heading + "</h1><h2>" + subheading + "</h2>" + body + "</section>"
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

func epubIdentifierForRelease(release domain.Release, title string) string {
	seed := firstNonEmptyString(
		scopedReleaseIdentity("provider", release.SourceID, release.ProviderReleaseID),
		scopedReleaseIdentity("url", release.SourceID, release.URL),
		release.ID,
		title,
		"serial-sync-release",
	)
	return "urn:uuid:" + uuid.NewSHA1(uuid.NameSpaceURL, []byte("serial-sync:"+seed)).String()
}

func scopedReleaseIdentity(kind, sourceID, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return kind + ":" + value
	}
	return kind + ":" + sourceID + ":" + value
}

func epubModifiedForRelease(release domain.Release) time.Time {
	if !release.EditedAt.IsZero() {
		return release.EditedAt.UTC()
	}
	if !release.PublishedAt.IsZero() {
		return release.PublishedAt.UTC()
	}
	return time.Unix(0, 0).UTC()
}
