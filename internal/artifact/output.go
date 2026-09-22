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

// outputProfile is applyOutputProfile's answer: the profiled bytes plus the
// name, type and validation the plan will record. A struct, so callers read
// named fields instead of five positional values.
type outputProfile struct {
	Content    []byte
	FileName   string
	MIMEType   string
	NeedsCheck bool
	Err        error
}

// applyOutputProfile turns the selected content into the bytes the library
// will hold. Name and type follow describeOutputProfile — the same function
// previews call — so the two modes cannot disagree about what a copy is
// called; this switch only does the byte work the description promised.
func applyOutputProfile(ctx context.Context, track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, decision domain.TrackDecision, content []byte, originalFileName, mimeType string, selectedAttachment bool) outputProfile {
	outputFormat := decision.OutputFormat
	if outputFormat == "" {
		outputFormat = domain.OutputFormatPreserve
	}
	preface := shouldPrependPostPreface(decision, mimeType, selectedAttachment, normalized)
	described := describeOutputProfile(outputFormat, originalFileName, mimeType, selectedAttachment, preface)

	switch outputFormat {
	case domain.OutputFormatPreserve:
		if preface && isEPUB(originalFileName, mimeType) {
			prefaceHTML := renderPrefaceHTML(track, release, normalized)
			epubContent, err := wrapEPUBWithPreface(content, track.TrackName, firstNonEmptyString(track.CanonicalAuthor, normalized.CreatorName), epubIdentifierForRelease(release, normalized.Title), epubModifiedForRelease(release), prefaceHTML)
			if err != nil {
				return outputProfile{Err: err}
			}
			return outputProfile{Content: epubContent, FileName: described.FileName, MIMEType: described.MIMEType, NeedsCheck: true}
		}
		return outputProfile{Content: content, FileName: originalFileName, MIMEType: mimeType}
	case domain.OutputFormatEPUB:
		prefaceHTML := ""
		if preface {
			prefaceHTML = renderPrefaceHTML(track, release, normalized)
		}
		author := firstNonEmptyString(track.CanonicalAuthor, normalized.CreatorName)
		identifier := epubIdentifierForRelease(release, normalized.Title)
		modified := epubModifiedForRelease(release)
		if isEPUB(originalFileName, mimeType) {
			if prefaceHTML == "" {
				return outputProfile{Content: content, FileName: described.FileName, MIMEType: described.MIMEType}
			}
			epubContent, err := wrapEPUBWithPreface(content, track.TrackName, author, identifier, modified, prefaceHTML)
			if err != nil {
				return outputProfile{Err: err}
			}
			return outputProfile{Content: epubContent, FileName: described.FileName, MIMEType: described.MIMEType, NeedsCheck: true}
		}
		if isPDFSource(originalFileName, mimeType) {
			epubContent, err := convertPDFToEPUB(ctx, content, track.TrackName, author, identifier, modified)
			if err != nil {
				return outputProfile{Err: err}
			}
			if prefaceHTML != "" {
				epubContent, err = wrapEPUBWithPreface(epubContent, track.TrackName, author, identifier, modified, prefaceHTML)
				if err != nil {
					return outputProfile{Err: err}
				}
			}
			return outputProfile{Content: epubContent, FileName: described.FileName, MIMEType: described.MIMEType, NeedsCheck: true}
		}
		if isHTMLSource(originalFileName, mimeType) {
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
			epubContent, err := buildSimpleEPUB(track.TrackName, author, identifier, modified, chapters)
			if err != nil {
				return outputProfile{Err: err}
			}
			return outputProfile{Content: epubContent, FileName: described.FileName, MIMEType: described.MIMEType, NeedsCheck: true}
		}
		return outputProfile{Err: fmt.Errorf("output format %q is only supported for EPUB attachments or HTML/text sources", outputFormat)}
	default:
		return outputProfile{Err: fmt.Errorf("unsupported output format %q", outputFormat)}
	}
}

func isPDFSource(fileName, mimeType string) bool {
	return strings.EqualFold(strings.TrimSpace(mimeType), "application/pdf") || strings.EqualFold(filepath.Ext(fileName), ".pdf")
}

func isHTMLSource(fileName, mimeType string) bool {
	return strings.EqualFold(strings.TrimSpace(mimeType), "text/html")
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
