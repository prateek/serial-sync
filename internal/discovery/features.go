package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/prateek/serial-sync/internal/artifact"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/textcontent"
)

const ExtractorVersion = 2

var prologuePattern = regexp.MustCompile(`(?i)\b(prologue|chapter\s*(one|1)\b)`)
var teaserPattern = regexp.MustCompile(`(?i)\b(teaser|sneak peek|preview)\b`)

var bookPattern = regexp.MustCompile(`(?i)\b(book|bk|volume|vol)\s*\d+\b`)

type Features struct {
	CollectionReferences []domain.LabelReference `json:"collection_references,omitempty"`
	SequenceOrigin       string                  `json:"sequence_origin"`
	PublishedAt          time.Time               `json:"published_at"`
	Teaser               bool                    `json:"teaser"`
	ReleaseID            string                  `json:"release_id"`
	Family               string                  `json:"family"`
	Sequence             domain.Sequence         `json:"sequence"`
	First                bool                    `json:"first"`
	BodyChars            int                     `json:"body_chars"`
	ContentHash          string                  `json:"content_hash"`
	Collections          []string                `json:"collections"`
	Tags                 []string                `json:"tags"`
	Attachments          []string                `json:"attachments"`
	BookFile             bool                    `json:"book_file"`
	External             bool                    `json:"external"`
}

func Extract(release domain.NormalizedRelease) Features {
	features := Features{SequenceOrigin: "title", PublishedAt: release.PublishedAt, ReleaseID: release.ProviderReleaseID, Sequence: artifact.DetectSequence(release.Title), BodyChars: textcontent.Count(release), Collections: release.Collections, Tags: release.Tags}
	if release.Enrichment != nil {
		features.CollectionReferences = release.Enrichment.Collections
	}
	title := release.Title
	for _, attachment := range release.Attachments {
		features.Attachments = append(features.Attachments, attachment.FileName)
		ext := strings.ToLower(filepath.Ext(attachment.FileName))
		features.BookFile = features.BookFile || ext == ".epub" || ext == ".pdf"
		if !features.Sequence.HasChapter() && features.Sequence.Part == "" {
			sequence := artifact.DetectSequence(attachment.FileName)
			if sequence.HasChapter() || sequence.Part != "" {
				features.Sequence = sequence
				features.SequenceOrigin = "attachment"
				title = strings.TrimSuffix(attachment.FileName, filepath.Ext(attachment.FileName))
			}
		}
	}
	features.Teaser = teaserPattern.MatchString(release.Title)
	features.First = features.Sequence.Chapter == 1 || !features.Sequence.HasChapter() && prologuePattern.MatchString(release.Title)
	if features.Sequence.HasChapter() || features.Sequence.Part != "" || prologuePattern.MatchString(title) {
		features.Family = titleFamily(title, features.Sequence)
	}
	features.External = features.BodyChars < 1500 && len(release.Attachments) == 0 && (strings.Contains(release.TextHTML, "href=") || strings.Contains(release.TextPlain, "https://"))
	copy := release
	copy.Enrichment = nil
	copy.Tags = nil
	copy.Collections = nil
	copy.ProviderReleaseID = ""
	copy.URL = ""
	copy.PublishedAt = time.Time{}
	copy.EditedAt = time.Time{}
	copy.Attachments = append([]domain.Attachment(nil), release.Attachments...)
	for i := range copy.Attachments {
		copy.Attachments[i].LocalPath = ""
		copy.Attachments[i].DownloadURL = ""
	}
	data, _ := json.Marshal(copy)
	features.ContentHash = digest(data)
	return features
}

func titleFamily(title string, sequence domain.Sequence) string {
	if sequence.MatchedText != "" {
		if index := strings.Index(title, sequence.MatchedText); index >= 0 {
			title = title[:index]
		}
	} else if index := prologuePattern.FindStringIndex(title); index != nil {
		title = title[:index[0]]
	}
	title = bookPattern.ReplaceAllString(title, "")
	return labelKey(title)
}

func labelKey(text string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
