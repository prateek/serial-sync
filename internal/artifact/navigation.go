package artifact

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
)

func removeAboutNavigation(files map[string][]byte, pkg opfPackage, packagePath, aboutID string) error {
	if aboutID == "" {
		return nil
	}
	base, about := path.Dir(packagePath), ""
	for _, item := range pkg.Manifest.Items {
		if item.ID == aboutID {
			about, _ = resolveManifestHref(base, item.Href)
		}
	}
	for _, item := range pkg.Manifest.Items {
		if !strings.Contains(" "+item.Properties+" ", " nav ") && item.MediaType != "application/x-dtbncx+xml" {
			continue
		}
		entry, err := resolveManifestHref(base, item.Href)
		if err != nil {
			return err
		}
		data := files[entry]
		decoder := xml.NewDecoder(bytes.NewReader(data))
		type container struct {
			start  int64
			remove bool
		}
		var stack []container
		for {
			offset := decoder.InputOffset()
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			switch value := token.(type) {
			case xml.StartElement:
				if value.Name.Local == "li" || value.Name.Local == "navPoint" {
					stack = append(stack, container{start: offset})
				}
				if len(stack) == 0 {
					continue
				}
				for _, attr := range value.Attr {
					if attr.Name.Local != "href" && attr.Name.Local != "src" {
						continue
					}
					u, parseErr := url.Parse(attr.Value)
					if parseErr != nil || u.IsAbs() {
						continue
					}
					linked, _ := resolveManifestHref(path.Dir(entry), u.EscapedPath())
					if linked == about {
						stack[len(stack)-1].remove = true
					}
				}
			case xml.EndElement:
				if (value.Name.Local != "li" && value.Name.Local != "navPoint") || len(stack) == 0 {
					continue
				}
				last := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if last.remove {
					files[entry] = append(append([]byte{}, data[:last.start]...), data[decoder.InputOffset():]...)
					break
				}
			}
			if !bytes.Equal(files[entry], data) {
				break
			}
		}
	}
	return nil
}

type ncxPoint struct {
	Label   string `xml:"navLabel>text"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Points []ncxPoint `xml:"navPoint"`
}

func memberNavigation(files map[string][]byte, pkg opfPackage, packagePath, prefix, aboutID string) ([]epubChapter, error) {
	base := path.Dir(packagePath)
	aboutPath := ""
	for _, item := range pkg.Manifest.Items {
		if item.ID == aboutID {
			aboutPath, _ = resolveManifestHref(base, item.Href)
		}
	}
	for _, item := range pkg.Manifest.Items {
		if !strings.Contains(" "+item.Properties+" ", " nav ") {
			continue
		}
		entry, err := resolveManifestHref(base, item.Href)
		if err != nil {
			return nil, err
		}
		document, err := html.Parse(bytes.NewReader(files[entry]))
		if err != nil {
			return nil, err
		}
		var toc *html.Node
		var find func(*html.Node)
		find = func(node *html.Node) {
			if node.Type == html.ElementNode && node.Data == "nav" {
				for _, attr := range node.Attr {
					if (attr.Key == "epub:type" || attr.Key == "type") && strings.Contains(" "+attr.Val+" ", " toc ") {
						toc = findHTMLElement(node, "ol")
						return
					}
				}
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				find(child)
			}
		}
		find(document)
		if toc != nil {
			return navigationList(toc, entry, prefix, aboutPath), nil
		}
	}
	for _, item := range pkg.Manifest.Items {
		if item.MediaType != "application/x-dtbncx+xml" {
			continue
		}
		entry, err := resolveManifestHref(base, item.Href)
		if err != nil {
			return nil, err
		}
		var ncx struct {
			Points []ncxPoint `xml:"navMap>navPoint"`
		}
		if err := xml.Unmarshal(files[entry], &ncx); err != nil {
			return nil, err
		}
		var convert func([]ncxPoint) []epubChapter
		convert = func(points []ncxPoint) []epubChapter {
			var result []epubChapter
			for _, point := range points {
				href := relocatedNavigationHref(point.Content.Src, entry, prefix, aboutPath)
				if href != "" {
					result = append(result, epubChapter{FileName: href, Title: point.Label, Children: convert(point.Points)})
				}
			}
			return result
		}
		return convert(ncx.Points), nil
	}
	return nil, nil
}

func navigationList(list *html.Node, documentPath, prefix, aboutPath string) []epubChapter {
	var result []epubChapter
	for li := list.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.Data != "li" {
			continue
		}
		var label, children *html.Node
		for child := li.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			switch child.Data {
			case "a", "span":
				label = child
			case "ol":
				children = child
			}
		}
		if label == nil {
			continue
		}
		chapter := epubChapter{Title: nodeText(label)}
		if label.Data == "a" {
			for _, attr := range label.Attr {
				if attr.Key == "href" {
					chapter.FileName = relocatedNavigationHref(attr.Val, documentPath, prefix, aboutPath)
				}
			}
			if chapter.FileName == "" {
				continue
			}
		}
		if children != nil {
			chapter.Children = navigationList(children, documentPath, prefix, aboutPath)
		}
		result = append(result, chapter)
	}
	return result
}

func relocatedNavigationHref(href, documentPath, prefix, aboutPath string) string {
	u, err := url.Parse(href)
	if err != nil || u.IsAbs() || u.Host != "" || href == "" {
		return ""
	}
	entry := documentPath
	if u.Path != "" {
		entry, err = resolveManifestHref(path.Dir(documentPath), u.EscapedPath())
	}
	if err != nil || entry == aboutPath {
		return ""
	}
	u.Path, u.RawPath = path.Join(prefix, entry), ""
	return u.String()
}

func nodeText(node *html.Node) string {
	var out strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			out.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.TrimSpace(out.String())
}
