package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/prateek/serial-sync/internal/domain"
)

const aboutMarker = "serial-sync:about"

func decoratePublication(files map[string][]byte, pkg *opfPackage, packagePath string, metadata domain.PublicationMetadata) error {
	if !metadata.DescriptionOverride {
		for _, element := range pkg.Metadata.DCElements {
			if element.Name == "description" && strings.TrimSpace(element.Value) != "" {
				metadata.Description = element.Value
				break
			}
		}
	}
	if metadata.Description != "" && (metadata.DescriptionOverride || !hasDCElement(pkg.Metadata.DCElements, "description")) {
		setPublicationDC(&pkg.Metadata, "description", metadata.Description)
	}
	if metadata.Language != "" {
		setPublicationDC(&pkg.Metadata, "language", metadata.Language)
	}
	base := path.Dir(packagePath)
	directory := "serial-sync"
	for n := 1; files[path.Join(base, directory, "about.xhtml")] != nil; n++ {
		directory = fmt.Sprintf("serial-sync-%d", n)
	}
	addAsset := func(asset *domain.MetadataAsset, name string) (string, error) {
		if asset == nil || asset.Path == "" {
			return "", nil
		}
		data, err := os.ReadFile(asset.Path)
		if err != nil {
			return "", err
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != asset.SHA256 {
			return "", fmt.Errorf("metadata asset changed after selection: %s", asset.Path)
		}
		ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif"}[asset.MediaType]
		if ext == "" {
			return "", fmt.Errorf("unsupported metadata image type %q", asset.MediaType)
		}
		href := path.Join(directory, name+ext)
		files[path.Join(base, href)] = data
		id := uniqueManifestID(pkg.Manifest.Items, "serial-sync-"+name)
		pkg.Manifest.Items = append(pkg.Manifest.Items, opfItem{ID: id, Href: href, MediaType: asset.MediaType})
		return id, nil
	}
	existingCover := ""
	for _, item := range pkg.Manifest.Items {
		if strings.Contains(" "+item.Properties+" ", " cover-image ") {
			existingCover = item.ID
		}
	}
	for _, meta := range pkg.Metadata.Meta {
		if meta.Name == "cover" {
			existingCover = meta.Content
		}
	}
	if metadata.Cover != nil && (existingCover == "" || metadata.CoverOverride) {
		id, err := addAsset(metadata.Cover, "cover")
		if err != nil {
			return err
		}
		if id != "" {
			for i := range pkg.Manifest.Items {
				pkg.Manifest.Items[i].Properties = strings.TrimSpace(strings.ReplaceAll(" "+pkg.Manifest.Items[i].Properties+" ", " cover-image ", " "))
				if pkg.Manifest.Items[i].ID == id && strings.HasPrefix(pkg.Version, "3") {
					pkg.Manifest.Items[i].Properties = "cover-image"
				}
			}
			kept := pkg.Metadata.Meta[:0]
			for _, meta := range pkg.Metadata.Meta {
				if meta.Name != "cover" {
					kept = append(kept, meta)
				}
			}
			pkg.Metadata.Meta = append(kept, opfMeta{Name: "cover", Content: id})
		}
	}
	body := "<h1>About</h1>"
	if metadata.Description != "" {
		body += "<p>" + escapeHTML(metadata.Description) + "</p>"
	}
	links := append([]string{}, metadata.Links...)
	for i, author := range metadata.Authors {
		body += "<h2>" + escapeHTML(firstNonEmptyString(author.Name, "About the author")) + "</h2>"
		id, err := addAsset(author.Portrait, fmt.Sprintf("portrait-%d", i+1))
		if err != nil {
			return err
		}
		if id != "" {
			for _, item := range pkg.Manifest.Items {
				if item.ID == id {
					body += `<p><img src="` + escapeHTML(path.Base(item.Href)) + `" alt="` + escapeHTML(author.Name) + `" style="max-width:100%;height:auto" /></p>`
				}
			}
		}
		if author.Biography != "" {
			body += "<p>" + escapeHTML(author.Biography) + "</p>"
		}
		if author.URL != "" {
			links = append(links, author.URL)
		}
	}
	seen := map[string]bool{}
	for _, link := range links {
		u, err := url.Parse(link)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || seen[link] {
			continue
		}
		body += `<p><a href="` + escapeHTML(link) + `">` + escapeHTML(link) + `</a></p>`
		seen[link] = true
	}
	aboutPath := path.Join(directory, "about.xhtml")
	viewport := fixedLayoutViewport(packageIsPrePaginated(*pkg), *pkg, files, base)
	document, err := buildXHTMLDocumentForEPUBVersionWithViewport("About", body, pkg.Version, viewport)
	if err != nil {
		return err
	}
	id := uniqueManifestID(pkg.Manifest.Items, "serial-sync-about")
	files[path.Join(base, aboutPath)] = []byte(document.Content)
	pkg.Manifest.Items = append(pkg.Manifest.Items, opfItem{ID: id, Href: aboutPath, MediaType: "application/xhtml+xml"})
	pkg.Spine.Itemrefs = append(pkg.Spine.Itemrefs, opfItemref{IDRef: id})
	pkg.Metadata.Meta = append(pkg.Metadata.Meta, opfMeta{Name: aboutMarker, Content: id})
	for _, item := range pkg.Manifest.Items {
		isNav := strings.Contains(" "+item.Properties+" ", " nav ")
		isNCX := item.MediaType == "application/x-dtbncx+xml"
		if !isNav && !isNCX {
			continue
		}
		entry, err := resolveManifestHref(base, item.Href)
		if err != nil {
			return err
		}
		href, err := filepath.Rel(path.Dir(entry), path.Join(base, aboutPath))
		if err != nil {
			return err
		}
		updated, err := appendAboutNavigation(files[entry], filepath.ToSlash(href), id, isNCX)
		if err != nil {
			return fmt.Errorf("append About navigation %s: %w", entry, err)
		}
		files[entry] = updated
	}
	return nil
}

func appendAboutNavigation(data []byte, href, id string, ncx bool) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, target := 0, -1
	inTOC := false
	for {
		offset := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			return nil, fmt.Errorf("table of contents not found")
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			depth++
			if value.Name.Local == "nav" {
				for _, attr := range value.Attr {
					if attr.Name.Local == "type" && strings.Contains(" "+attr.Value+" ", " toc ") {
						inTOC = true
					}
				}
			}
			if target < 0 && ((ncx && value.Name.Local == "navMap") || (!ncx && inTOC && value.Name.Local == "ol")) {
				target = depth
			}
		case xml.EndElement:
			if depth == target {
				addition := `<li xmlns="http://www.w3.org/1999/xhtml"><a href="` + escapeHTML(href) + `">About</a></li>`
				if ncx {
					addition = `<navPoint xmlns="http://www.daisy.org/z3986/2005/ncx/" id="` + escapeHTML(id) + `"><navLabel><text>About</text></navLabel><content src="` + escapeHTML(href) + `"/></navPoint>`
				}
				result := append([]byte{}, data[:offset]...)
				result = append(result, []byte(addition)...)
				return append(result, data[offset:]...), nil
			}
			if value.Name.Local == "nav" {
				inTOC = false
			}
			depth--
		}
	}
}
