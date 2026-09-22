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
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/classify"
	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/publish"
	"github.com/prateek/serial-sync/internal/sequence"
)

type canonicalSidecar struct {
	OutputVersion int                       `json:"output_version"`
	Track         domain.StoryTrack         `json:"track"`
	Release       domain.Release            `json:"release"`
	Decision      domain.TrackDecision      `json:"decision"`
	Normalized    *domain.NormalizedRelease `json:"normalized"`
}

func readCanonicalSidecar(artifact domain.Artifact) (*canonicalSidecar, bool) {
	if artifact.MetadataRef == "" {
		return nil, false
	}
	var meta canonicalSidecar
	data, err := os.ReadFile(artifact.MetadataRef)
	if err != nil || json.Unmarshal(data, &meta) != nil || meta.OutputVersion < 1 {
		return nil, false
	}
	return &meta, true
}

// IsCurrent answers whether this stored artifact is current for the planned
// release, track and decision, matching the sidecar the planner wrote.
func IsCurrent(artifact domain.Artifact, release domain.Release, track domain.StoryTrack, decision domain.TrackDecision) bool {
	meta, ok := readCanonicalSidecar(artifact)
	if !ok || meta.OutputVersion != 2 {
		return false
	}
	if meta.Decision.SelectedContent == nil && meta.Normalized != nil {
		selected := classify.SelectContent(*meta.Normalized, meta.Decision)
		meta.Decision.SelectedContent = &selected
	}
	// The sidecar holds the captured snapshot of the publication metadata,
	// whose asset paths differ from the planned ones; compare by fingerprint.
	if PublicationFingerprint(meta.Decision.Publication) != PublicationFingerprint(decision.Publication) {
		return false
	}
	meta.Decision.Publication, decision.Publication = nil, nil
	return meta.Release.ContentHash == release.ContentHash && meta.Track.TrackName == track.TrackName && meta.Track.CanonicalAuthor == track.CanonicalAuthor && reflect.DeepEqual(meta.Decision, decision)
}

// StoredPublication returns the publication metadata snapshot a stored
// artifact was built with, so a resync keeps the pinned metadata.
func StoredPublication(artifact domain.Artifact) (*domain.PublicationMetadata, error) {
	// Legacy sidecars predate output_version and carry no publication; only an
	// unreadable sidecar is an error.
	var meta canonicalSidecar
	data, err := os.ReadFile(artifact.MetadataRef)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return meta.Decision.Publication, nil
}

// PublicationFingerprint identifies publication metadata by its selected
// bytes; asset file locations are capture references and do not count.
func PublicationFingerprint(metadata *domain.PublicationMetadata) string {
	data, _ := json.Marshal(metadata)
	var copy *domain.PublicationMetadata
	_ = json.Unmarshal(data, &copy)
	if copy != nil {
		if copy.Cover != nil {
			copy.Cover.Path = ""
		}
		for i := range copy.Authors {
			if copy.Authors[i].Portrait != nil {
				copy.Authors[i].Portrait.Path = ""
			}
		}
	}
	data, _ = json.Marshal(copy)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// IsLegacy answers whether a stored artifact predates the output_version: 2
// sidecar and still needs an explicit migration rebuild.
func IsLegacy(artifact domain.Artifact) bool {
	meta, ok := readCanonicalSidecar(artifact)
	return !ok || meta.OutputVersion < 2
}

// ArchivedSequence returns the sequence this stored artifact was built with,
// when its sidecar records one.
func ArchivedSequence(artifact domain.Artifact) *domain.Sequence {
	meta, ok := readCanonicalSidecar(artifact)
	if !ok || meta.Decision.Sequence == nil {
		return nil
	}
	return meta.Decision.Sequence
}

// IntactOnDisk answers whether the artifact's stored bytes still match its
// recorded content hash.
func IntactOnDisk(artifact domain.Artifact) (bool, error) {
	actual, err := publish.FileHash(artifact.StorageRef)
	if err != nil {
		return false, err
	}
	return actual == artifact.SHA256, nil
}

type Materializer struct {
	Root string
}

func New(root string) *Materializer {
	return &Materializer{Root: root}
}

func (m *Materializer) Plan(ctx context.Context, source domain.Source, track domain.StoryTrack, release domain.Release, normalized domain.NormalizedRelease, decision domain.TrackDecision, rawJSON []byte) (domain.ArtifactPlan, error) {
	if !classify.CanMaterialize(normalized, decision) {
		return domain.ArtifactPlan{}, errors.New("release does not produce a materializable canonical artifact")
	}
	meta := map[string]any{
		"output_version": 2,
		"source":         source,
		"track":          track,
		"release":        release,
		"decision":       decision,
		"normalized":     normalized,
	}
	normalizedJSON, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return domain.ArtifactPlan{}, err
	}
	var content []byte
	var originalFileName string
	var mimeType string
	var kind string
	var selectedAttachment bool
	selection := classify.SelectContent(normalized, decision)
	switch selection.Kind {
	case "attachment":
		attachment, ok := classify.SelectAttachment(normalized, decision)
		if !ok {
			return domain.ArtifactPlan{}, errors.New("selected attachment no longer matches the captured release")
		}
		if attachment.LocalPath == "" {
			return domain.ArtifactPlan{}, fmt.Errorf("selected attachment %q is missing a local_path", attachment.FileName)
		}
		content, err = os.ReadFile(attachment.LocalPath)
		if err != nil {
			return domain.ArtifactPlan{}, err
		}
		originalFileName = attachment.FileName
		mimeType = attachment.MIMEType
		selectedAttachment = true

	case "body":
		rendered := renderHTML(normalized)
		content = []byte(rendered)
		originalFileName = fmt.Sprintf("%s.html", slug(normalized.Title))
		mimeType = "text/html"
	case "none":
		return domain.ArtifactPlan{}, errors.New("manual strategy does not select a canonical artifact")
	default:
		return domain.ArtifactPlan{}, fmt.Errorf("unsupported content strategy %q", decision.ContentStrategy)
	}
	preserveEmbedded := selectedAttachment && (strings.EqualFold(strings.TrimSpace(mimeType), "application/epub+zip") || strings.EqualFold(filepath.Ext(originalFileName), ".epub"))
	validateEPUBCheck := false
	content, originalFileName, mimeType, validateEPUBCheck, err = applyOutputProfile(ctx, track, release, normalized, decision, content, originalFileName, mimeType, selectedAttachment)
	if err != nil {
		return domain.ArtifactPlan{}, err
	}
	if validateEPUBCheck || decision.Publication != nil && mimeType == "application/epub+zip" {
		info := sequence.Detect(normalized.Title, originalFileName)
		position := info.Chapter
		if decision.Sequence != nil {
			position = decision.Sequence.Position
		}
		content, err = withPublicationMetadata(content, publicationMetadata{
			Title: release.Title, Author: firstNonEmptyString(track.CanonicalAuthor, normalized.CreatorName),
			Series: track.TrackName, Position: position, PublishedAt: release.PublishedAt,
			PreserveEmbedded: preserveEmbedded, Publication: decision.Publication,
		})
		if err != nil {
			return domain.ArtifactPlan{}, err
		}
		validateEPUBCheck = true
	}
	fileName := canonicalFileName(track, release, normalized, originalFileName, mimeType, decision.Sequence)
	snapshot, assets, err := m.snapshotPublication(decision.Publication)
	if err != nil {
		return domain.ArtifactPlan{}, err
	}
	decision.Publication = snapshot
	meta["decision"] = decision
	metadataJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return domain.ArtifactPlan{}, err
	}
	kind = attachmentKind(fileName, mimeType)
	sum := sha256.Sum256(content)
	return domain.ArtifactPlan{
		MetadataAssets:    assets,
		ArtifactKind:      kind,
		Filename:          fileName,
		MIMEType:          mimeType,
		SHA256:            hex.EncodeToString(sum[:]),
		ValidateEPUBCheck: validateEPUBCheck,
		SelectedContent:   content,
		MetadataJSON:      metadataJSON,
		NormalizedJSON:    normalizedJSON,
		RawJSON:           append([]byte(nil), rawJSON...),
	}, nil
}

func (m *Materializer) Materialize(ctx context.Context, source domain.Source, track domain.StoryTrack, release domain.Release, plan domain.ArtifactPlan) (domain.Artifact, error) {
	select {
	case <-ctx.Done():
		return domain.Artifact{}, ctx.Err()
	default:
	}
	dir := filepath.Join(m.Root, source.ID, track.TrackKey, release.ProviderReleaseID, plan.SHA256)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.Artifact{}, err
	}
	baseName := plan.Filename
	artifactPath := filepath.Join(dir, baseName)
	metadataPath := filepath.Join(dir, baseName+".metadata.json")
	normalizedPath := filepath.Join(dir, baseName+".normalized.json")
	rawPath := filepath.Join(dir, baseName+".raw.json")
	if plan.ValidateEPUBCheck {
		if err := validateEPUBArchive(plan.SelectedContent); err != nil {
			return domain.Artifact{}, err
		}
	}
	for path, data := range plan.MetadataAssets {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return domain.Artifact{}, err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return domain.Artifact{}, err
		}
	}
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
