package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prateek/serial-sync/internal/domain"
)

func VolumeFilename(title string, number int) string {
	return fmt.Sprintf("%s-vol%02d.epub", slug(title), number)
}

func BookVolumeFilename(title string, number int) string {
	return fmt.Sprintf("%s-bk%02d.epub", slug(title), number)
}

func (m *Materializer) BuildVolume(ctx context.Context, volume domain.VolumeEdition, title string, chapters []domain.PublishCandidate) (domain.Artifact, error) {
	if len(chapters) == 0 {
		return domain.Artifact{}, fmt.Errorf("volume has no chapters")
	}
	identifier := "urn:uuid:" + uuid.NewSHA1(uuid.NameSpaceURL, []byte("serial-sync:volume:"+volume.SeriesID+":"+volume.GroupID)).String()
	modified := time.Unix(0, 0).UTC()
	for _, chapter := range chapters {
		if date := epubModifiedForRelease(chapter.Release); date.After(modified) {
			modified = date
		}
	}
	front := "<p>" + escapeHTML(title) + "</p>"
	for _, note := range volume.Notes {
		front += "<p>Intentional gap: " + escapeHTML(note) + "</p>"
	}
	content, err := buildSimpleEPUB(title, chapters[0].Track.CanonicalAuthor, identifier, modified, []epubChapter{{FileName: "contents.xhtml", Title: "Contents", BodyHTML: front}})
	if err != nil {
		return domain.Artifact{}, err
	}
	session, err := openEPUBPackage(content)
	if err != nil {
		return domain.Artifact{}, err
	}
	nav := []epubChapter{}
	for i, chapter := range chapters {
		if err := ctx.Err(); err != nil {
			return domain.Artifact{}, err
		}
		data, err := os.ReadFile(chapter.Artifact.StorageRef)
		if err != nil {
			return domain.Artifact{}, err
		}
		member, err := openEPUBPackage(data)
		if err != nil {
			return domain.Artifact{}, fmt.Errorf("chapter %s: %w", chapter.Release.Title, err)
		}
		prefix := fmt.Sprintf("members/%04d", i+1)
		items := map[string]opfItem{}
		if err := member.removeGeneratedAbout(); err != nil {
			return domain.Artifact{}, err
		}
		for _, item := range member.Package.Manifest.Items {
			originalID := item.ID
			entry, err := member.entry(item.Href)
			if err != nil {
				return domain.Artifact{}, fmt.Errorf("chapter resource %q: %w", item.Href, err)
			}
			if strings.HasPrefix(entry, "../") {
				return domain.Artifact{}, fmt.Errorf("unsafe member path %q", entry)
			}
			payload, ok := member.Files[entry]
			if !ok {
				return domain.Artifact{}, fmt.Errorf("chapter resource %s is missing", entry)
			}
			item.ID = fmt.Sprintf("member-%04d-%s", i+1, originalID)
			resourcePath := path.Join(prefix, entry)
			item.Href = (&url.URL{Path: resourcePath}).EscapedPath()
			item.Properties = strings.ReplaceAll(item.Properties, "cover-image", "")
			item.Properties = strings.TrimSpace(strings.ReplaceAll(" "+item.Properties+" ", " nav ", " "))
			if item.Fallback != "" {
				item.Fallback = fmt.Sprintf("member-%04d-%s", i+1, item.Fallback)
			}
			if item.MediaOverlay != "" {
				item.MediaOverlay = fmt.Sprintf("member-%04d-%s", i+1, item.MediaOverlay)
			}
			session.Files[path.Join("OEBPS", resourcePath)] = payload
			session.Package.Manifest.Items = append(session.Package.Manifest.Items, item)
			items[originalID] = item
		}
		first := true
		memberNav, err := member.memberNavigation(prefix)
		if err != nil {
			return domain.Artifact{}, fmt.Errorf("chapter %s navigation: %w", chapter.Release.Title, err)
		}
		for _, ref := range member.Package.Spine.Itemrefs {
			item, ok := items[ref.IDRef]
			if !ok {
				continue
			}
			session.Package.Spine.Itemrefs = append(session.Package.Spine.Itemrefs, opfItemref{IDRef: item.ID, Linear: ref.Linear})
			if first {
				if len(memberNav) == 1 && memberNav[0].FileName == item.Href {
					memberNav = memberNav[0].Children
				}
				nav = append(nav, epubChapter{FileName: item.Href, Title: chapter.Release.Title, Children: memberNav})
				first = false
			}
		}
		if first {
			return domain.Artifact{}, fmt.Errorf("chapter %s has no reading content", chapter.Release.Title)
		}
	}
	session.Files["OEBPS/nav.xhtml"] = []byte(buildNavDocument(title, nav, strings.SplitN(nav[0].FileName, "#", 2)[0]))
	content, err = session.write(packageIndented)
	if err != nil {
		return domain.Artifact{}, err
	}
	content, err = withPublicationMetadata(content, publicationMetadata{Title: title, Author: chapters[0].Track.CanonicalAuthor, Series: chapters[0].Track.TrackName, SeriesIndex: volumeSeriesIndex(volume, chapters[0]), PublishedAt: chapters[0].Release.PublishedAt, Publication: volume.Publication, IncludeAbout: true})
	if err != nil {
		return domain.Artifact{}, err
	}
	if err := validateEPUBArchive(content); err != nil {
		return domain.Artifact{}, err
	}
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	dir := filepath.Join(m.Root, "volumes", volume.SeriesID, volume.ID, hash)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.Artifact{}, err
	}
	filename := volume.Artifact.Filename
	art := domain.Artifact{ID: "volart_" + hash, TrackID: volume.TrackID, ArtifactKind: "epub", IsCanonical: true, Filename: filename, MIMEType: "application/epub+zip", SHA256: hash, StorageRef: filepath.Join(dir, filename), BuiltAt: time.Now().UTC(), State: domain.ArtifactStateMaterialized, MetadataRef: filepath.Join(dir, "volume.json")}
	volume.Artifact = art
	snapshot, assets, err := m.snapshotPublication(volume.Publication)
	if err != nil {
		return domain.Artifact{}, err
	}
	volume.Publication = snapshot
	for assetPath, data := range assets {
		if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
			return domain.Artifact{}, err
		}
		if err := os.WriteFile(assetPath, data, 0o644); err != nil {
			return domain.Artifact{}, err
		}
	}
	metadata, err := json.MarshalIndent(volume, "", "  ")
	if err != nil {
		return domain.Artifact{}, err
	}
	if err := os.WriteFile(art.StorageRef, content, 0o644); err != nil {
		return domain.Artifact{}, err
	}
	if err := os.WriteFile(art.MetadataRef, metadata, 0o644); err != nil {
		return domain.Artifact{}, err
	}
	return art, nil
}

// volumeSeriesIndex sorts a volume where its first chapter sorts.
func volumeSeriesIndex(volume domain.VolumeEdition, first domain.PublishCandidate) string {
	if recorded := ArchivedSequence(first.Artifact); recorded != nil && recorded.SeriesIndex != "" {
		return recorded.SeriesIndex
	}
	return positionIndex(volume.Members[0].Position)
}
