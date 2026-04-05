package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/domain"
)

type Materializer struct {
	Root string
}

func New(root string) *Materializer {
	return &Materializer{Root: root}
}

func (m *Materializer) Plan(source domain.Source, track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, decision domain.TrackDecision, rawJSON []byte) (domain.ArtifactPlan, error) {
	if !classify.CanMaterialize(normalized, decision) {
		return domain.ArtifactPlan{}, errors.New("release does not produce a materializable canonical artifact")
	}
	meta := map[string]any{
		"source":     source,
		"track":      track,
		"release":    release,
		"decision":   decision,
		"normalized": normalized,
	}
	metadataJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return domain.ArtifactPlan{}, err
	}
	normalizedJSON, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return domain.ArtifactPlan{}, err
	}
	var content []byte
	var fileName string
	var mimeType string
	var kind string
	switch decision.ContentStrategy {
	case domain.ContentStrategyAttachmentPreferred, domain.ContentStrategyAttachmentOnly:
		if attachment, ok := classify.SelectAttachment(normalized, decision); ok {
			if attachment.LocalPath == "" {
				if decision.ContentStrategy == domain.ContentStrategyAttachmentOnly {
					return domain.ArtifactPlan{}, fmt.Errorf("selected attachment %q is missing a local_path", attachment.FileName)
				}
			} else {
				content, err = os.ReadFile(attachment.LocalPath)
				if err != nil {
					return domain.ArtifactPlan{}, err
				}
				fileName = sanitizeFileName(attachment.FileName)
				mimeType = attachment.MIMEType
				kind = attachmentKind(fileName, mimeType)
				break
			}
		}
		fallthrough
	case domain.ContentStrategyTextPost, domain.ContentStrategyTextPlusAttachment:
		rendered := renderHTML(normalized)
		content = []byte(rendered)
		fileName = fmt.Sprintf("%s-%s.html", release.ProviderReleaseID, slug(normalized.Title))
		mimeType = "text/html"
		kind = "html"
	case domain.ContentStrategyManual:
		return domain.ArtifactPlan{}, errors.New("manual strategy does not select a canonical artifact")
	default:
		return domain.ArtifactPlan{}, fmt.Errorf("unsupported content strategy %q", decision.ContentStrategy)
	}
	sum := sha256.Sum256(content)
	return domain.ArtifactPlan{
		ArtifactKind:    kind,
		Filename:        fileName,
		MIMEType:        mimeType,
		SHA256:          hex.EncodeToString(sum[:]),
		SelectedContent: content,
		MetadataJSON:    metadataJSON,
		NormalizedJSON:  normalizedJSON,
		RawJSON:         append([]byte(nil), rawJSON...),
	}, nil
}

func (m *Materializer) Materialize(ctx context.Context, source domain.Source, track domain.StoryTrack, release domain.Release, plan domain.ArtifactPlan) (domain.Artifact, error) {
	select {
	case <-ctx.Done():
		return domain.Artifact{}, ctx.Err()
	default:
	}
	dir := filepath.Join(m.Root, source.ID, track.TrackKey, release.ProviderReleaseID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.Artifact{}, err
	}
	baseName := shortHash(plan.SHA256) + "-" + plan.Filename
	artifactPath := filepath.Join(dir, baseName)
	metadataPath := filepath.Join(dir, baseName+".metadata.json")
	normalizedPath := filepath.Join(dir, baseName+".normalized.json")
	rawPath := filepath.Join(dir, baseName+".raw.json")
	if err := os.WriteFile(artifactPath, plan.SelectedContent, 0o644); err != nil {
		return domain.Artifact{}, err
	}
	if err := os.WriteFile(metadataPath, plan.MetadataJSON, 0o644); err != nil {
		return domain.Artifact{}, err
	}
	if err := os.WriteFile(normalizedPath, plan.NormalizedJSON, 0o644); err != nil {
		return domain.Artifact{}, err
	}
	if len(plan.RawJSON) > 0 {
		if err := os.WriteFile(rawPath, plan.RawJSON, 0o644); err != nil {
			return domain.Artifact{}, err
		}
	}
	return domain.Artifact{
		ID:            "art_" + uuid.NewString(),
		ReleaseID:     release.ID,
		TrackID:       track.ID,
		ArtifactKind:  plan.ArtifactKind,
		IsCanonical:   true,
		Filename:      baseName,
		MIMEType:      plan.MIMEType,
		SHA256:        plan.SHA256,
		StorageRef:    artifactPath,
		BuiltAt:       time.Now().UTC(),
		State:         domain.ArtifactStateMaterialized,
		MetadataRef:   metadataPath,
		NormalizedRef: normalizedPath,
		RawRef:        rawPath,
	}, nil
}

func renderHTML(normalized domain.NormalizedRelease) string {
	title := escapeHTML(normalized.Title)
	body := strings.TrimSpace(normalized.TextHTML)
	if body == "" {
		body = "<p>" + escapeHTML(normalized.TextPlain) + "</p>"
	}
	return "<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>" + title + "</title></head><body><article><h1>" + title + "</h1>\n" + body + "\n</article></body></html>\n"
}

func sanitizeFileName(input string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "\n", " ", "\r", " ")
	return replacer.Replace(strings.TrimSpace(input))
}

func slug(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	var builder strings.Builder
	lastDash := false
	for _, r := range input {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case !lastDash:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "release"
	}
	return result
}

func shortHash(input string) string {
	if len(input) < 12 {
		return input
	}
	return input[:12]
}

func attachmentKind(fileName, mimeType string) string {
	switch {
	case strings.HasSuffix(strings.ToLower(fileName), ".epub"):
		return "epub"
	case strings.HasSuffix(strings.ToLower(fileName), ".pdf"):
		return "pdf"
	case mimeType == "text/html":
		return "html"
	default:
		return "file"
	}
}

func escapeHTML(input string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(input)
}
