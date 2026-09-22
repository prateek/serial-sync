package artifact

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

type publicationMetadata struct {
	Title, Author, Series string
	Position              int
	PublishedAt           time.Time
	PreserveEmbedded      bool
	Publication           *domain.PublicationMetadata
}

func withPublicationMetadata(content []byte, metadata publicationMetadata) ([]byte, error) {
	files, pkg, packagePath, err := unpackEPUB(content)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(pkg.Version, "2") && pkg.Spine.Toc == "" {
		pkg.Spine.Toc = firstNCXID(pkg.Manifest.Items)
	}
	if !metadata.PreserveEmbedded || !hasDCElement(pkg.Metadata.DCElements, "title") {
		setPublicationDC(&pkg.Metadata, "title", metadata.Title)
	}
	if !metadata.PreserveEmbedded || !hasDCElement(pkg.Metadata.DCElements, "creator") {
		setPublicationDC(&pkg.Metadata, "creator", metadata.Author)
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
	if metadata.Series != "" {
		pkg.Metadata.Meta = append(pkg.Metadata.Meta, opfMeta{Name: "calibre:series", Content: metadata.Series})
		if metadata.Position > 0 {
			pkg.Metadata.Meta = append(pkg.Metadata.Meta, opfMeta{Name: "calibre:series_index", Content: strconv.Itoa(metadata.Position)})
		}
		if strings.HasPrefix(pkg.Version, "3") {
			id := "serial-sync-series"
			for suffix := 1; bytes.Contains(files[packagePath], []byte(`id="`+id+`"`)); suffix++ {
				id = fmt.Sprintf("serial-sync-series-%d", suffix)
			}
			pkg.Metadata.Meta = append(pkg.Metadata.Meta,
				opfMeta{Property: "belongs-to-collection", Value: metadata.Series, Attrs: []xml.Attr{{Name: xml.Name{Local: "id"}, Value: id}}},
				opfMeta{Property: "collection-type", Refines: "#" + id, Value: "series"},
			)
			if metadata.Position > 0 {
				pkg.Metadata.Meta = append(pkg.Metadata.Meta, opfMeta{Property: "group-position", Refines: "#" + id, Value: strconv.Itoa(metadata.Position)})
			}
		}
	}
	if metadata.Publication != nil {
		if err := decoratePublication(files, &pkg, packagePath, *metadata.Publication); err != nil {
			return nil, err
		}
	}
	files[packagePath] = mustXML(pkg)
	return writeStructurallyValidatedEPUBArchive(files)
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
