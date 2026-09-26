package artifact

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
	"github.com/prateek/serial-sync/internal/sequence"
)

type publicationMetadata struct {
	Title, Author, Series string
	// Chapter marks a standalone chapter in epub output.
	Chapter          bool
	ExpandedTitle    string
	SeriesIndex      string
	PublishedAt      time.Time
	PreserveEmbedded bool
	IncludeAbout     bool
	Publication      *domain.PublicationMetadata
}

// positionIndex renders a scalar series position, where zero means unknown.
func positionIndex(position int) string {
	if position <= 0 {
		return ""
	}
	return strconv.Itoa(position)
}

func withPublicationMetadata(content []byte, metadata publicationMetadata) ([]byte, error) {
	session, err := openEPUBPackage(content)
	if err != nil {
		return nil, err
	}
	pkg := &session.Package
	if strings.HasPrefix(pkg.Version, "2") && pkg.Spine.Toc == "" {
		pkg.Spine.Toc = firstNCXID(pkg.Manifest.Items)
	}
	replacedTitles := []string{metadata.ExpandedTitle, pkg.Metadata.Title}
	expanded := metadata.ExpandedTitle
	if metadata.Publication != nil && metadata.Publication.IdentitySource == domain.PublicationIdentityRelease {
		replacePublicationIdentity(&pkg.Metadata, session.PackagePath, metadata.Title, metadata.Author)
	} else {
		if !metadata.PreserveEmbedded || !hasDCElement(pkg.Metadata.DCElements, "title") {
			setPublicationDC(&pkg.Metadata, "title", metadata.Title)
		} else if metadata.Chapter {
			expanded = pkg.Metadata.Title
			setPublicationDC(&pkg.Metadata, "title", sequence.WithoutSeriesName(pkg.Metadata.Title, metadata.Series))
		}
		if !metadata.PreserveEmbedded || !hasDCElement(pkg.Metadata.DCElements, "creator") {
			setPublicationDC(&pkg.Metadata, "creator", metadata.Author)
		}
	}
	if metadata.Chapter {
		if err := session.relabelChapterNavigation(firstDCValue(pkg.Metadata, "title"), metadata.Series, replacedTitles); err != nil {
			return nil, err
		}
		if strings.HasPrefix(pkg.Version, "3") {
			addExpandedTitle(pkg, expanded)
		}
	}
	if !metadata.PublishedAt.IsZero() && (!metadata.PreserveEmbedded || !hasDCElement(pkg.Metadata.DCElements, "date")) {
		setPublicationDC(&pkg.Metadata, "date", metadata.PublishedAt.UTC().Format(time.RFC3339))
	}
	removedIDs := map[string]bool{}
	for _, meta := range pkg.Metadata.Meta {
		if meta.Property == "belongs-to-collection" {
			for _, attr := range meta.Attrs {
				if attr.Name.Local == "id" {
					removedIDs["#"+attr.Value] = true
				}
			}
		}
	}
	kept := []opfMeta{}
	for _, meta := range pkg.Metadata.Meta {
		if meta.Property == "belongs-to-collection" || removedIDs[meta.Refines] || meta.Name == "calibre:series" || meta.Name == "calibre:series_index" {
			continue
		}
		kept = append(kept, meta)
	}
	pkg.Metadata.Meta = kept
	if metadata.Publication != nil {
		if err := session.decoratePublication(*metadata.Publication, metadata.IncludeAbout); err != nil {
			return nil, err
		}
	}
	if metadata.Series != "" {
		pkg.Metadata.Meta = append(pkg.Metadata.Meta, opfMeta{Name: "calibre:series", Content: metadata.Series})
		if metadata.SeriesIndex != "" {
			pkg.Metadata.Meta = append(pkg.Metadata.Meta, opfMeta{Name: "calibre:series_index", Content: metadata.SeriesIndex})
		}
		if strings.HasPrefix(pkg.Version, "3") {
			id := "serial-sync-series"
			for suffix := 1; bytes.Contains(mustXML(*pkg), []byte(`id="`+id+`"`)); suffix++ {
				id = fmt.Sprintf("serial-sync-series-%d", suffix)
			}
			pkg.Metadata.Meta = append(pkg.Metadata.Meta,
				opfMeta{Property: "belongs-to-collection", Value: metadata.Series, Attrs: []xml.Attr{{Name: xml.Name{Local: "id"}, Value: id}}},
				opfMeta{Property: "collection-type", Refines: "#" + id, Value: "series"},
			)
			if metadata.SeriesIndex != "" {
				pkg.Metadata.Meta = append(pkg.Metadata.Meta, opfMeta{Property: "group-position", Refines: "#" + id, Value: metadata.SeriesIndex})
			}
		}
	}
	return session.write(packageCompact)
}

func replacePublicationIdentity(metadata *opfMetadata, packagePath, title, author string) {
	removed := map[string]bool{}
	refinementTarget := func(reference string) string {
		u, err := url.Parse(strings.TrimSpace(reference))
		if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment == "" {
			return ""
		}
		if u.Path != "" && path.Clean(path.Join(path.Dir(packagePath), u.Path)) != path.Clean(packagePath) {
			return ""
		}
		return "#" + u.Fragment
	}
	removeIDs := func(attrs []xml.Attr) {
		for _, attr := range attrs {
			if attr.Name.Local == "id" && attr.Value != "" {
				removed["#"+attr.Value] = true
			}
		}
	}
	kept := metadata.DCElements[:0]
	for _, element := range metadata.DCElements {
		if element.Name == "title" || element.Name == "creator" {
			removeIDs(element.Attrs)
			continue
		}
		kept = append(kept, element)
	}
	metadata.DCElements = kept
	for {
		before := len(metadata.Meta) + len(metadata.Raw)
		kept := metadata.Meta[:0]
		for _, meta := range metadata.Meta {
			if removed[refinementTarget(meta.Refines)] || meta.Name == "calibre:title_sort" || meta.Name == "calibre:author_sort" {
				removeIDs(meta.Attrs)
				continue
			}
			kept = append(kept, meta)
		}
		metadata.Meta = kept
		keptRaw := metadata.Raw[:0]
		for _, raw := range metadata.Raw {
			remove := false
			if len(raw.Tokens) > 0 {
				if start, ok := raw.Tokens[0].(xml.StartElement); ok && start.Name.Local == "link" && (start.Name.Space == "" || start.Name.Space == "http://www.idpf.org/2007/opf") {
					for _, attr := range start.Attr {
						if attr.Name.Local == "refines" && removed[refinementTarget(attr.Value)] {
							removeIDs(start.Attr)
							remove = true
						}
					}
				}
			}
			if !remove {
				keptRaw = append(keptRaw, raw)
			}
		}
		metadata.Raw = keptRaw
		if len(metadata.Meta)+len(metadata.Raw) == before {
			break
		}
	}
	metadata.Title, metadata.Creator = strings.TrimSpace(title), strings.TrimSpace(author)
	setPublicationDC(metadata, "title", title)
	setPublicationDC(metadata, "creator", author)
}

func setPublicationDC(metadata *opfMetadata, name, value string) {
	value = strings.TrimSpace(value)
	for i := range metadata.DCElements {
		if metadata.DCElements[i].Name == name {
			metadata.DCElements[i].Value = value
			return
		}
	}
	metadata.DCElements = append(metadata.DCElements, opfDCElement{Name: name, Value: value})
}

func firstDCValue(metadata opfMetadata, name string) string {
	for _, element := range metadata.DCElements {
		if element.Name == name {
			return element.Value
		}
	}
	return ""
}

func addExpandedTitle(pkg *opfPackage, expanded string) {
	expanded = strings.TrimSpace(expanded)
	if expanded == "" || strings.EqualFold(expanded, strings.TrimSpace(firstDCValue(pkg.Metadata, "title"))) {
		return
	}
	id := "serial-sync-title-expanded"
	for suffix := 1; bytes.Contains(mustXML(*pkg), []byte(`id="`+id+`"`)); suffix++ {
		id = fmt.Sprintf("serial-sync-title-expanded-%d", suffix)
	}
	// Readers take the first dc:title as the main title.
	pkg.Metadata.DCElements = append(pkg.Metadata.DCElements, opfDCElement{Name: "title", Value: expanded, Attrs: []xml.Attr{{Name: xml.Name{Local: "id"}, Value: id}}})
	pkg.Metadata.Meta = append(pkg.Metadata.Meta, opfMeta{Property: "title-type", Refines: "#" + id, Value: "expanded"})
}
