package artifact

import (
	"path/filepath"
	"strings"

	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/domain"
)

// ReadingCopy is the one answer to "what would the library hold" for a
// release: the canonical file name, its type, and the validation the bytes
// will need. Plan materializes a copy; Describe previews one without reading
// or converting bytes, so previews cannot drift from materialization.
//
// The sequence is consumed from the decision (sequence.Apply guarantees it);
// nothing here re-detects it. A copy named from a different sequence than the
// one published in calibre metadata would split identity between file and
// catalog.
type ReadingCopy struct {
	FileName         string
	MIMEType         string
	ArtifactKind     string
	NeedsEPUBCheck   bool
	PreserveEmbedded bool
}

// isEPUB is the single test for "these bytes are an EPUB": the MIME type or
// the file extension, either one counts.
func isEPUB(fileName, mimeType string) bool {
	return strings.EqualFold(strings.TrimSpace(mimeType), "application/epub+zip") || strings.EqualFold(filepath.Ext(fileName), ".epub")
}

// describeOutputProfile answers the reading-copy question from the selected
// content's name and type alone. The byte path (applyOutputProfile) feeds the
// same three inputs into the same result, which is what keeps the two modes
// agreeing.
func describeOutputProfile(outputFormat domain.OutputFormat, originalFileName, mimeType string, selectedAttachment, preface bool) ReadingCopy {
	result := ReadingCopy{FileName: originalFileName, MIMEType: mimeType, PreserveEmbedded: selectedAttachment && isEPUB(originalFileName, mimeType)}
	switch outputFormat {
	case domain.OutputFormatPreserve:
		result.NeedsEPUBCheck = preface && isEPUB(originalFileName, mimeType)
	case domain.OutputFormatEPUB:
		if isEPUB(originalFileName, mimeType) {
			result.FileName = forceExtension(originalFileName, ".epub")
			result.MIMEType = "application/epub+zip"
			result.NeedsEPUBCheck = preface
		} else if isEPUBConvertible(originalFileName, mimeType) {
			result.FileName = forceExtension(originalFileName, ".epub")
			result.MIMEType = "application/epub+zip"
			result.NeedsEPUBCheck = true
		}
	}
	result.ArtifactKind = attachmentKind(result.FileName, result.MIMEType)
	return result
}

// isEPUBConvertible reports whether the output profile can produce an EPUB
// from these inputs at all: PDF converts via Calibre and HTML builds
// directly; anything else is left alone (Plan rejects it with its own error).
func isEPUBConvertible(fileName, mimeType string) bool {
	if strings.EqualFold(strings.TrimSpace(mimeType), "application/pdf") || strings.EqualFold(filepath.Ext(fileName), ".pdf") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(mimeType), "text/html")
}

// Describe names and types the reading copy a decision would produce, from
// the decision alone.
func Describe(track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, decision domain.TrackDecision) ReadingCopy {
	name, mime := "chapter.html", "text/html"
	selectedAttachment := false
	if classify.SelectContent(normalized, decision).Kind == "attachment" {
		if attachment, ok := classify.SelectAttachment(normalized, decision); ok {
			name, mime = attachment.FileName, attachment.MIMEType
			selectedAttachment = true
		}
	}
	outputFormat := decision.OutputFormat
	if outputFormat == "" {
		outputFormat = domain.OutputFormatPreserve
	}
	preface := shouldPrependPostPreface(decision, mime, selectedAttachment, normalized)
	result := describeOutputProfile(outputFormat, name, mime, selectedAttachment, preface)
	result.FileName = canonicalFileName(track, release, normalized, result.FileName, result.MIMEType, decision.Sequence)
	return result
}
