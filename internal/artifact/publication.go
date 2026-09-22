package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/prateek/serial-sync/internal/domain"
)

const aboutMarker = "serial-sync:about"
const authorMarker = "serial-sync:author"

type publicationAuthor struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Biography string `json:"biography,omitempty"`
	URL       string `json:"url,omitempty"`
	Portrait  string `json:"portrait,omitempty"`
}

func decoratePublication(files map[string][]byte, pkg *opfPackage, packagePath string, metadata domain.PublicationMetadata, includeAbout bool) error {
	if err := removeGeneratedAbout(files, pkg, packagePath); err != nil {
		return err
	}
	authors, err := embedPublicationMetadata(files, pkg, packagePath, metadata)
	if err != nil {
		return err
	}
	if includeAbout {
		return appendAboutPage(files, pkg, packagePath, authors, metadata.Links, metadata.SourceURL)
	}
	return nil
}

func embedPublicationMetadata(files map[string][]byte, pkg *opfPackage, packagePath string, metadata domain.PublicationMetadata) ([]publicationAuthor, error) {
	if metadata.Description != "" && (metadata.DescriptionOverride || !hasDCElement(pkg.Metadata.DCElements, "description")) {
		setPublicationDC(&pkg.Metadata, "description", metadata.Description)
	}
	if metadata.Language != "" {
		setPublicationDC(&pkg.Metadata, "language", metadata.Language)
	}
	addLink := func(name, link string) {
		if validPublicationURL(link) && !slices.ContainsFunc(pkg.Metadata.DCElements, func(element opfDCElement) bool {
			return element.Name == name && element.Value == link
		}) {
			pkg.Metadata.DCElements = append(pkg.Metadata.DCElements, opfDCElement{Name: name, Value: link})
		}
	}
	addLink("source", metadata.SourceURL)
	for _, link := range metadata.Links {
		if link != metadata.SourceURL {
			addLink("relation", link)
		}
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
		item, err := addPublicationAsset(files, pkg, packagePath, metadata.Cover, "cover")
		if err != nil {
			return nil, err
		}
		if item.ID != "" {
			for i := range pkg.Manifest.Items {
				pkg.Manifest.Items[i].Properties = strings.TrimSpace(strings.ReplaceAll(" "+pkg.Manifest.Items[i].Properties+" ", " cover-image ", " "))
				if pkg.Manifest.Items[i].ID == item.ID && strings.HasPrefix(pkg.Version, "3") {
					pkg.Manifest.Items[i].Properties = "cover-image"
				}
			}
			pkg.Metadata.Meta = slices.DeleteFunc(pkg.Metadata.Meta, func(meta opfMeta) bool { return meta.Name == "cover" })
			pkg.Metadata.Meta = append(pkg.Metadata.Meta, opfMeta{Name: "cover", Content: item.ID})
		}
	}
	pkg.Metadata.Meta = slices.DeleteFunc(pkg.Metadata.Meta, func(meta opfMeta) bool { return meta.Name == authorMarker })
	var authors []publicationAuthor
	for i, profile := range metadata.Authors {
		portrait, err := addPublicationAsset(files, pkg, packagePath, profile.Portrait, fmt.Sprintf("portrait-%d", i+1))
		if err != nil {
			return nil, err
		}
		author := publicationAuthor{ID: profile.ID, Name: profile.Name, Biography: profile.Biography, Portrait: portrait.Href}
		if validPublicationURL(profile.URL) {
			author.URL = profile.URL
			addLink("relation", profile.URL)
		}
		data, err := json.Marshal(author)
		if err != nil {
			return nil, err
		}
		pkg.Metadata.Meta = append(pkg.Metadata.Meta, opfMeta{Name: authorMarker, Content: string(data)})
		authors = append(authors, author)
	}
	return authors, nil
}

func addPublicationAsset(files map[string][]byte, pkg *opfPackage, packagePath string, asset *domain.MetadataAsset, name string) (opfItem, error) {
	if asset == nil || asset.Path == "" {
		return opfItem{}, nil
	}
	data, err := os.ReadFile(asset.Path)
	if err != nil {
		return opfItem{}, err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != asset.SHA256 {
		return opfItem{}, fmt.Errorf("metadata asset changed after selection: %s", asset.Path)
	}
	ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif"}[asset.MediaType]
	if ext == "" {
		return opfItem{}, fmt.Errorf("unsupported metadata image type %q", asset.MediaType)
	}
	base := path.Dir(packagePath)
	for n := 0; ; n++ {
		directory := "serial-sync"
		if n > 0 {
			directory = fmt.Sprintf("serial-sync-%d", n)
		}
		href := path.Join(directory, name+ext)
		entry := path.Join(base, href)
		if existing, ok := files[entry]; ok {
			if bytes.Equal(existing, data) {
				for _, item := range pkg.Manifest.Items {
					resolved, err := resolveManifestHref(base, item.Href)
					if err == nil && resolved == entry && item.MediaType == asset.MediaType {
						return item, nil
					}
				}
			}
			continue
		}
		item := opfItem{ID: uniqueManifestID(pkg.Manifest.Items, "serial-sync-"+name), Href: href, MediaType: asset.MediaType}
		files[entry] = data
		pkg.Manifest.Items = append(pkg.Manifest.Items, item)
		return item, nil
	}
}

func validPublicationURL(link string) bool {
	u, err := url.Parse(link)
	return err == nil && u.Host != "" && u.User == nil && (u.Scheme == "https" || u.Scheme == "http")
}

func appendAboutPage(files map[string][]byte, pkg *opfPackage, packagePath string, authors []publicationAuthor, links []string, sourceURL string) error {
	base := path.Dir(packagePath)
	directory := "serial-sync"
	for n := 1; files[path.Join(base, directory, "about.xhtml")] != nil; n++ {
		directory = fmt.Sprintf("serial-sync-%d", n)
	}
	var body strings.Builder
	heading := "About this edition"
	if len(authors) == 1 {
		heading = "About the author"
	}
	if len(authors) > 1 {
		heading = "About the authors"
	}
	body.WriteString("<h1>" + heading + "</h1>\n")
	seen := map[string]bool{}
	addLink := func(link, label string) {
		if !validPublicationURL(link) || seen[link] {
			return
		}
		body.WriteString(`<p><a href="` + escapeHTML(link) + `">` + escapeHTML(label) + "</a></p>\n")
		seen[link] = true
	}
	for _, author := range authors {
		if author.Name != "" {
			body.WriteString("<h2>" + escapeHTML(author.Name) + "</h2>\n")
		}
		if author.Portrait != "" {
			href, err := filepath.Rel(directory, author.Portrait)
			if err != nil {
				return err
			}
			body.WriteString(`<p><img src="` + escapeHTML(filepath.ToSlash(href)) + `" alt="` + escapeHTML(author.Name) + `" style="max-width:12em;height:auto" /></p>` + "\n")
		}
		for _, paragraph := range strings.Split(strings.ReplaceAll(author.Biography, "\r\n", "\n"), "\n") {
			if strings.TrimSpace(paragraph) != "" {
				body.WriteString("<p>" + escapeHTML(strings.TrimSpace(paragraph)) + "</p>\n")
			}
		}
		addLink(author.URL, firstNonEmptyString(author.Name, "Author")+" — author page")
	}
	for _, link := range links {
		if link != sourceURL && validPublicationURL(link) && !seen[link] {
			u, _ := url.Parse(link)
			addLink(link, "Related reading on "+u.Hostname())
		}
	}
	addLink(sourceURL, "Original publication")
	if len(authors) == 0 && len(seen) == 0 {
		return nil
	}
	aboutPath := path.Join(directory, "about.xhtml")
	viewport := fixedLayoutViewport(packageIsPrePaginated(*pkg), *pkg, files, base)
	document, err := buildXHTMLDocumentForEPUBVersionWithViewportAndFileName("About", body.String(), pkg.Version, viewport, "")
	if err != nil {
		return err
	}
	content := strings.Replace(document.Content, "</head>", `<style type="text/css">body { margin: 1.5em; line-height: 1.5; } h1 { margin: 0 0 1.5em; } h2 { margin: 1.5em 0 0.75em; } p { margin: 0 0 1em; } img { display: block; max-width: 100%; height: auto; }</style></head>`, 1)
	id := uniqueManifestID(pkg.Manifest.Items, "serial-sync-about")
	files[path.Join(base, aboutPath)] = []byte(content)
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

func removeGeneratedAbout(files map[string][]byte, pkg *opfPackage, packagePath string) error {
	removed := map[string]bool{}
	for _, meta := range pkg.Metadata.Meta {
		if meta.Name == aboutMarker && meta.Content != "" {
			removed[meta.Content] = true
			if err := removeAboutNavigation(files, *pkg, packagePath, meta.Content); err != nil {
				return err
			}
		}
	}
	for _, item := range pkg.Manifest.Items {
		if removed[item.ID] {
			entry, err := resolveManifestHref(path.Dir(packagePath), item.Href)
			if err != nil {
				return err
			}
			delete(files, entry)
		}
	}
	pkg.Manifest.Items = slices.DeleteFunc(pkg.Manifest.Items, func(item opfItem) bool { return removed[item.ID] })
	pkg.Spine.Itemrefs = slices.DeleteFunc(pkg.Spine.Itemrefs, func(ref opfItemref) bool { return removed[ref.IDRef] })
	pkg.Metadata.Meta = slices.DeleteFunc(pkg.Metadata.Meta, func(meta opfMeta) bool { return meta.Name == aboutMarker })
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
