package artifact

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/prateek/serial-sync/internal/sequence"
	"golang.org/x/net/html"
)

func (s *epubPackage) removeAboutNavigation(aboutID string) error {
	if aboutID == "" {
		return nil
	}
	about := ""
	for _, item := range s.Package.Manifest.Items {
		if item.ID == aboutID {
			var err error
			about, err = s.entry(item.Href)
			if err != nil {
				return err
			}
		}
	}
	if about == "" {
		return nil
	}
	for _, item := range s.Package.Manifest.Items {
		if !strings.Contains(" "+item.Properties+" ", " nav ") && item.MediaType != "application/x-dtbncx+xml" {
			continue
		}
		entry, err := s.entry(item.Href)
		if err != nil {
			return err
		}
		for {
			data := s.Files[entry]
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
						s.Files[entry] = append(append([]byte{}, data[:last.start]...), data[decoder.InputOffset():]...)
						break
					}
				}
				if !bytes.Equal(s.Files[entry], data) {
					break
				}
			}
			if bytes.Equal(s.Files[entry], data) {
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

func (s *epubPackage) memberNavigation(prefix string) ([]epubChapter, error) {
	for _, item := range s.Package.Manifest.Items {
		if !strings.Contains(" "+item.Properties+" ", " nav ") {
			continue
		}
		entry, err := s.entry(item.Href)
		if err != nil {
			return nil, err
		}
		document, err := html.Parse(bytes.NewReader(s.Files[entry]))
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
			return navigationList(toc, entry, prefix), nil
		}
	}
	for _, item := range s.Package.Manifest.Items {
		if item.MediaType != "application/x-dtbncx+xml" {
			continue
		}
		entry, err := s.entry(item.Href)
		if err != nil {
			return nil, err
		}
		var ncx struct {
			Points []ncxPoint `xml:"navMap>navPoint"`
		}
		if err := xml.Unmarshal(s.Files[entry], &ncx); err != nil {
			return nil, err
		}
		var convert func([]ncxPoint) []epubChapter
		convert = func(points []ncxPoint) []epubChapter {
			var result []epubChapter
			for _, point := range points {
				href := relocatedNavigationHref(point.Content.Src, entry, prefix)
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

func navigationList(list *html.Node, documentPath, prefix string) []epubChapter {
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
					chapter.FileName = relocatedNavigationHref(attr.Val, documentPath, prefix)
				}
			}
			if chapter.FileName == "" {
				continue
			}
		}
		if children != nil {
			chapter.Children = navigationList(children, documentPath, prefix)
		}
		result = append(result, chapter)
	}
	return result
}

func relocatedNavigationHref(href, documentPath, prefix string) string {
	u, err := url.Parse(href)
	if err != nil || u.IsAbs() || u.Host != "" || href == "" {
		return ""
	}
	entry := documentPath
	if u.Path != "" {
		entry, err = resolveManifestHref(path.Dir(documentPath), u.EscapedPath())
	}
	if err != nil {
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

type navigationLabel struct {
	text       string
	start, end int64
	target     string
}

func (s *epubPackage) relabelChapterNavigation(title, series string, replaced []string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	generated := map[string]bool{}
	for _, item := range s.Package.Manifest.Items {
		if strings.HasPrefix(item.ID, "serial-sync-") {
			if entry, err := s.entry(item.Href); err == nil {
				generated[entry] = true
			}
		}
	}
	for _, item := range s.Package.Manifest.Items {
		nav := strings.Contains(" "+item.Properties+" ", " nav ")
		if !nav && item.MediaType != "application/x-dtbncx+xml" {
			continue
		}
		entry, err := s.entry(item.Href)
		if err != nil {
			return err
		}
		labels, err := navigationLabels(s.Files[entry], entry, nav)
		if err != nil {
			return err
		}
		var content []navigationLabel
		for _, label := range labels {
			if !generated[label.target] {
				content = append(content, label)
			}
		}
		if len(content) != 1 || !replaceableLabel(content[0], series, replaced) {
			continue
		}
		data := s.Files[entry]
		s.Files[entry] = append(append(append([]byte{}, data[:content[0].start]...), escapeHTML(title)...), data[content[0].end:]...)
	}
	return nil
}

func replaceableLabel(label navigationLabel, series string, replaced []string) bool {
	text := strings.TrimSpace(label.text)
	switch strings.ToLower(text) {
	case "", "start", "begin", "beginning", "chapter", "text", "content", "contents", "untitled", "unknown":
		return true
	}
	base := path.Base(label.target)
	if strings.EqualFold(text, base) || strings.EqualFold(text, strings.TrimSuffix(base, path.Ext(base))) {
		return true
	}
	for _, title := range replaced {
		if strings.TrimSpace(title) != "" && strings.EqualFold(text, strings.TrimSpace(title)) {
			return true
		}
	}
	return series != "" && sequence.WithoutSeriesName(text, series) != text
}

func navigationLabels(data []byte, documentPath string, nav bool) ([]navigationLabel, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = false
	decoder.Entity = xml.HTMLEntity
	var labels []navigationLabel
	var open []int
	inTOC, navDepth := false, 0
	labelDepth, textStart := 0, int64(-1)
	var text strings.Builder
	resolve := func(href string) string {
		u, err := url.Parse(href)
		if err != nil || u.IsAbs() || u.Path == "" {
			return ""
		}
		target, _ := resolveManifestHref(path.Dir(documentPath), u.EscapedPath())
		return target
	}
	for {
		offset := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			return labels, nil
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if nav {
				if value.Name.Local == "nav" {
					navDepth++
					for _, attr := range value.Attr {
						if attr.Name.Local == "type" && strings.Contains(" "+attr.Value+" ", " toc ") {
							inTOC, navDepth = true, 1
						}
					}
				}
				if inTOC && value.Name.Local == "a" && labelDepth == 0 {
					label := navigationLabel{start: decoder.InputOffset()}
					for _, attr := range value.Attr {
						if attr.Name.Local == "href" {
							label.target = resolve(attr.Value)
						}
					}
					labels = append(labels, label)
					open = append(open, len(labels)-1)
					labelDepth, textStart = 1, label.start
					text.Reset()
					continue
				}
			} else {
				switch value.Name.Local {
				case "navMap":
					inTOC = true
				case "navPoint":
					if inTOC {
						labels = append(labels, navigationLabel{start: -1})
						open = append(open, len(labels)-1)
					}
				case "text":
					if inTOC && len(open) > 0 && labels[open[len(open)-1]].start < 0 {
						labels[open[len(open)-1]].start = decoder.InputOffset()
						labelDepth, textStart = 1, labels[open[len(open)-1]].start
						text.Reset()
						continue
					}
				case "content":
					if inTOC && len(open) > 0 {
						for _, attr := range value.Attr {
							if attr.Name.Local == "src" {
								labels[open[len(open)-1]].target = resolve(attr.Value)
							}
						}
					}
				}
			}
			if labelDepth > 0 {
				labelDepth++
			}
		case xml.CharData:
			if labelDepth > 0 {
				text.Write(value)
			}
		case xml.EndElement:
			if labelDepth > 0 {
				labelDepth--
				if labelDepth == 0 && textStart >= 0 {
					current := &labels[open[len(open)-1]]
					current.end, current.text = offset, text.String()
					textStart = -1
					if nav {
						open = open[:len(open)-1]
					}
				}
				continue
			}
			if nav && value.Name.Local == "nav" && inTOC {
				if navDepth--; navDepth == 0 {
					inTOC = false
				}
			}
			if !nav && value.Name.Local == "navPoint" && len(open) > 0 {
				open = open[:len(open)-1]
			}
			if !nav && value.Name.Local == "navMap" {
				inTOC = false
			}
		}
	}
}

const prefaceTitle = "Author's note"

func (s *epubPackage) replaceLegacyTOC(title string, entries []epubChapter) error {
	kept := s.Package.Manifest.Items[:0]
	for _, item := range s.Package.Manifest.Items {
		if item.MediaType == "application/x-dtbncx+xml" {
			entry, err := s.entry(item.Href)
			if err != nil {
				return err
			}
			delete(s.Files, entry)
			continue
		}
		kept = append(kept, item)
	}
	s.Package.Manifest.Items = kept
	s.Package.Spine.Toc = ""

	var relocate func([]epubChapter) []epubChapter
	relocate = func(entries []epubChapter) []epubChapter {
		result := make([]epubChapter, 0, len(entries))
		for _, entry := range entries {
			entry.FileName = relativeHref(s.Dir, entry.FileName)
			entry.Children = relocate(entry.Children)
			result = append(result, entry)
		}
		return result
	}
	fileName := "serial-sync-nav.xhtml"
	for suffix := 1; ; suffix++ {
		if _, taken := s.Files[path.Join(s.Dir, fileName)]; !taken {
			break
		}
		fileName = "serial-sync-nav-" + strconv.Itoa(suffix) + ".xhtml"
	}
	s.Files[path.Join(s.Dir, fileName)] = []byte(buildNavDocument(title, relocate(entries), ""))
	s.Package.Manifest.Items = append(s.Package.Manifest.Items, opfItem{ID: uniqueManifestID(s.Package.Manifest.Items, "serial-sync-nav"), Href: fileName, MediaType: "application/xhtml+xml", Properties: "nav"})
	return nil
}

func (s *epubPackage) navigatePreface(preface string) error {
	bodyMatter := ""
	for i, ref := range s.Package.Spine.Itemrefs {
		if i == 0 || ref.Linear == "no" {
			continue
		}
		for _, item := range s.Package.Manifest.Items {
			if item.ID == ref.IDRef {
				entry, err := s.entry(item.Href)
				if err != nil {
					return err
				}
				bodyMatter = entry
			}
		}
		break
	}
	for _, item := range s.Package.Manifest.Items {
		if !strings.Contains(" "+item.Properties+" ", " nav ") {
			continue
		}
		entry, err := s.entry(item.Href)
		if err != nil {
			return err
		}
		dir := path.Dir(entry)
		entryHTML := `<li><a href="` + escapeHTML(relativeHref(dir, preface)) + `">` + escapeHTML(prefaceTitle) + `</a></li>`
		landmarks := ""
		if bodyMatter != "" {
			landmarks = landmarksNav(relativeHref(dir, bodyMatter))
		}
		data, err := spliceNavigation(s.Files[entry], entryHTML, landmarks)
		if err != nil {
			return err
		}
		s.Files[entry] = data
		return nil
	}
	return nil
}

func spliceNavigation(data []byte, entryHTML, landmarks string) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = false
	decoder.Entity = xml.HTMLEntity
	listAt, tocEnd := int64(-1), int64(-1)
	inTOC, depth, hasLandmarks := false, 0, false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "nav" {
				for _, attr := range value.Attr {
					if attr.Name.Local != "type" {
						continue
					}
					if strings.Contains(" "+attr.Value+" ", " landmarks ") {
						hasLandmarks = true
					}
					if strings.Contains(" "+attr.Value+" ", " toc ") && tocEnd < 0 {
						inTOC = true
					}
				}
				if inTOC {
					depth++
				}
			}
			if inTOC && value.Name.Local == "ol" && listAt < 0 {
				listAt = decoder.InputOffset()
			}
		case xml.EndElement:
			if inTOC && value.Name.Local == "nav" {
				if depth--; depth == 0 {
					inTOC, tocEnd = false, decoder.InputOffset()
				}
			}
		}
	}
	if listAt < 0 || tocEnd < 0 {
		return data, nil
	}
	if hasLandmarks {
		landmarks = ""
	}
	var out bytes.Buffer
	out.Write(data[:listAt])
	out.WriteString(entryHTML)
	out.Write(data[listAt:tocEnd])
	out.WriteString(landmarks)
	out.Write(data[tocEnd:])
	return out.Bytes(), nil
}

func relativeHref(dir, target string) string {
	file, fragment, _ := strings.Cut(target, "#")
	from := strings.Split(strings.Trim(dir, "/"), "/")
	if dir == "" || dir == "." {
		from = nil
	}
	to := strings.Split(file, "/")
	for len(from) > 0 && len(to) > 1 && from[0] == to[0] {
		from, to = from[1:], to[1:]
	}
	parts := make([]string, 0, len(from)+len(to))
	for range from {
		parts = append(parts, "..")
	}
	href := strings.Join(append(parts, to...), "/")
	if fragment != "" {
		href += "#" + fragment
	}
	return href
}
