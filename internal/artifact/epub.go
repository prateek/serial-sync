package artifact

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

type epubChapter struct {
	Children    []epubChapter
	FileName    string
	Title       string
	BodyHTML    string
	FrontMatter bool
}

type containerDocument struct {
	XMLName  xml.Name       `xml:"container"`
	Version  string         `xml:"version,attr,omitempty"`
	Xmlns    string         `xml:"xmlns,attr,omitempty"`
	RootFile containerFiles `xml:"rootfiles"`
}

type containerFiles struct {
	RootFile []containerRootFile `xml:"rootfile"`
}

type containerRootFile struct {
	FullPath  string `xml:"full-path,attr"`
	MediaType string `xml:"media-type,attr,omitempty"`
}

type opfPackage struct {
	XMLName  xml.Name          `xml:"package"`
	Xmlns    string            `xml:"xmlns,attr,omitempty"`
	UniqueID string            `xml:"unique-identifier,attr,omitempty"`
	Version  string            `xml:"version,attr,omitempty"`
	Prefix   string            `xml:"prefix,attr,omitempty"`
	Metadata opfMetadata       `xml:"metadata"`
	Manifest opfManifest       `xml:"manifest"`
	Spine    opfSpine          `xml:"spine"`
	Attrs    []xml.Attr        `xml:"-"`
	Children []opfPackageChild `xml:"-"`
}

type opfPackageChildKind string

const (
	opfPackageChildMetadata opfPackageChildKind = "metadata"
	opfPackageChildManifest opfPackageChildKind = "manifest"
	opfPackageChildSpine    opfPackageChildKind = "spine"
	opfPackageChildRaw      opfPackageChildKind = "raw"
)

type opfPackageChild struct {
	Kind opfPackageChildKind
	Raw  opfRawElement
}

type opfRawElement struct {
	Tokens []xml.Token
}

func (pkg *opfPackage) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	*pkg = opfPackage{XMLName: start.Name}
	if start.Name.Space != "" {
		pkg.Xmlns = start.Name.Space
	}
	for _, attr := range start.Attr {
		switch {
		case attr.Name.Space == "" && attr.Name.Local == "xmlns":
			pkg.Xmlns = attr.Value
		case attr.Name.Space == "xmlns":
			continue
		case attr.Name.Space == "" && attr.Name.Local == "unique-identifier":
			pkg.UniqueID = attr.Value
		case attr.Name.Space == "" && attr.Name.Local == "version":
			pkg.Version = attr.Value
		case attr.Name.Space == "" && attr.Name.Local == "prefix":
			pkg.Prefix = attr.Value
		default:
			pkg.Attrs = append(pkg.Attrs, attr)
		}
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "metadata":
				var metadata opfMetadata
				if err := decoder.DecodeElement(&metadata, &value); err != nil {
					return err
				}
				pkg.Metadata = metadata
				pkg.Children = append(pkg.Children, opfPackageChild{Kind: opfPackageChildMetadata})
			case "manifest":
				var manifest opfManifest
				if err := decoder.DecodeElement(&manifest, &value); err != nil {
					return err
				}
				pkg.Manifest = manifest
				pkg.Children = append(pkg.Children, opfPackageChild{Kind: opfPackageChildManifest})
			case "spine":
				var spine opfSpine
				if err := decoder.DecodeElement(&spine, &value); err != nil {
					return err
				}
				pkg.Spine = spine
				pkg.Children = append(pkg.Children, opfPackageChild{Kind: opfPackageChildSpine})
			default:
				raw, err := readRawElement(decoder, value)
				if err != nil {
					return err
				}
				pkg.Children = append(pkg.Children, opfPackageChild{Kind: opfPackageChildRaw, Raw: raw})
			}
		case xml.EndElement:
			if value.Name == start.Name {
				return nil
			}
		}
	}
}

func (pkg opfPackage) MarshalXML(encoder *xml.Encoder, start xml.StartElement) error {
	start.Name = xml.Name{Local: "package"}
	start.Attr = opfPackageAttrs(pkg)
	if err := encoder.EncodeToken(start); err != nil {
		return err
	}
	seen := map[opfPackageChildKind]bool{}
	for _, child := range pkg.Children {
		switch child.Kind {
		case opfPackageChildMetadata:
			seen[opfPackageChildMetadata] = true
			if err := encoder.Encode(pkg.Metadata); err != nil {
				return err
			}
		case opfPackageChildManifest:
			seen[opfPackageChildManifest] = true
			if err := encoder.EncodeElement(pkg.Manifest, xml.StartElement{Name: xml.Name{Local: "manifest"}}); err != nil {
				return err
			}
		case opfPackageChildSpine:
			seen[opfPackageChildSpine] = true
			if err := encoder.EncodeElement(pkg.Spine, xml.StartElement{Name: xml.Name{Local: "spine"}}); err != nil {
				return err
			}
		case opfPackageChildRaw:
			if child.Raw.isEmptyGuide() {
				continue
			}
			if err := child.Raw.EncodeXML(encoder); err != nil {
				return err
			}
		}
	}
	if len(pkg.Children) == 0 || !seen[opfPackageChildMetadata] {
		if err := encoder.Encode(pkg.Metadata); err != nil {
			return err
		}
	}
	if len(pkg.Children) == 0 || !seen[opfPackageChildManifest] {
		if err := encoder.EncodeElement(pkg.Manifest, xml.StartElement{Name: xml.Name{Local: "manifest"}}); err != nil {
			return err
		}
	}
	if len(pkg.Children) == 0 || !seen[opfPackageChildSpine] {
		if err := encoder.EncodeElement(pkg.Spine, xml.StartElement{Name: xml.Name{Local: "spine"}}); err != nil {
			return err
		}
	}
	return encoder.EncodeToken(start.End())
}

func opfPackageAttrs(pkg opfPackage) []xml.Attr {
	attrs := []xml.Attr{}
	if strings.TrimSpace(pkg.Xmlns) != "" {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: pkg.Xmlns})
	}
	if strings.TrimSpace(pkg.UniqueID) != "" {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "unique-identifier"}, Value: pkg.UniqueID})
	}
	if strings.TrimSpace(pkg.Version) != "" {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "version"}, Value: pkg.Version})
	}
	if strings.TrimSpace(pkg.Prefix) != "" {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "prefix"}, Value: pkg.Prefix})
	}
	attrs = append(attrs, pkg.Attrs...)
	if packageUsesOPFNamespace(pkg) && !hasXMLAttr(attrs, "xmlns:opf") {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: "xmlns:opf"}, Value: "http://www.idpf.org/2007/opf"})
	}
	return attrs
}

func packageUsesOPFNamespace(pkg opfPackage) bool {
	for _, identifier := range pkg.Metadata.Identifiers {
		if attrsUsePrefix(opfIdentifierAttrs(identifier), "opf:") {
			return true
		}
	}
	if attrsUsePrefix(opfIdentifierAttrs(pkg.Metadata.Identifier), "opf:") {
		return true
	}
	return false
}

func attrsUsePrefix(attrs []xml.Attr, prefix string) bool {
	for _, attr := range attrs {
		if strings.HasPrefix(attr.Name.Local, prefix) {
			return true
		}
	}
	return false
}

func hasXMLAttr(attrs []xml.Attr, local string) bool {
	for _, attr := range attrs {
		if attr.Name.Space == "" && attr.Name.Local == local {
			return true
		}
	}
	return false
}

func readRawElement(decoder *xml.Decoder, start xml.StartElement) (opfRawElement, error) {
	tokens := []xml.Token{xml.CopyToken(start)}
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return opfRawElement{}, err
		}
		tokens = append(tokens, xml.CopyToken(token))
		switch token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return opfRawElement{Tokens: tokens}, nil
}

func (raw opfRawElement) EncodeXML(encoder *xml.Encoder) error {
	for _, token := range raw.Tokens {
		if start, ok := token.(xml.StartElement); ok {
			start.Attr = withoutXMLNamespaceDeclarations(start.Attr)
			token = start
		}
		if err := encoder.EncodeToken(token); err != nil {
			return err
		}
	}
	return nil
}

func (raw opfRawElement) isEmptyGuide() bool {
	if len(raw.Tokens) == 0 {
		return false
	}
	start, ok := raw.Tokens[0].(xml.StartElement)
	if !ok || start.Name != (xml.Name{Space: "http://www.idpf.org/2007/opf", Local: "guide"}) {
		return false
	}
	for _, token := range raw.Tokens[1:] {
		switch value := token.(type) {
		case xml.StartElement:
			return false
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return false
			}
		}
	}
	return true
}

func withoutXMLNamespaceDeclarations(attrs []xml.Attr) []xml.Attr {
	result := make([]xml.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if attr.Name.Space == "xmlns" || attr.Name.Space == "" && attr.Name.Local == "xmlns" {
			continue
		}
		result = append(result, attr)
	}
	return result
}

type opfMetadata struct {
	XMLName     xml.Name        `xml:"metadata"`
	DC          string          `xml:"xmlns:dc,attr,omitempty"`
	Title       string          `xml:"-"`
	Creator     string          `xml:"-"`
	Language    string          `xml:"-"`
	Identifier  opfIdentifier   `xml:"-"`
	Meta        []opfMeta       `xml:"meta,omitempty"`
	Identifiers []opfIdentifier `xml:"-"`
	DCElements  []opfDCElement  `xml:"-"`
	Attrs       []xml.Attr      `xml:"-"`
	Raw         []opfRawElement `xml:"-"`
}

type opfDCElement struct {
	Name  string
	Attrs []xml.Attr
	Value string
}

type opfIdentifier struct {
	ID    string     `xml:"id,attr,omitempty"`
	Value string     `xml:",chardata"`
	Attrs []xml.Attr `xml:"-"`
}

func (identifier *opfIdentifier) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	*identifier = opfIdentifier{}
	for _, attr := range start.Attr {
		attr = normalizeOPFAttr(attr)
		identifier.Attrs = append(identifier.Attrs, attr)
		if attr.Name.Local == "id" {
			identifier.ID = attr.Value
		}
	}
	return decoder.DecodeElement(&identifier.Value, &start)
}

func (identifier opfIdentifier) MarshalXML(encoder *xml.Encoder, start xml.StartElement) error {
	start.Name.Local = "dc:identifier"
	start.Attr = opfIdentifierAttrs(identifier)
	return encoder.EncodeElement(identifier.Value, start)
}

func (metadata *opfMetadata) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	metadata.XMLName = start.Name
	for _, attr := range start.Attr {
		if attr.Name.Space == "xmlns" {
			if attr.Name.Local == "dc" {
				metadata.DC = attr.Value
			}
			continue
		}
		metadata.Attrs = append(metadata.Attrs, attr)
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "title":
				var text string
				if err := decoder.DecodeElement(&text, &value); err != nil {
					return err
				}
				metadata.DCElements = append(metadata.DCElements, opfDCElement{Name: "title", Attrs: value.Attr, Value: text})
				if strings.TrimSpace(metadata.Title) == "" {
					metadata.Title = text
				}
			case "creator":
				var text string
				if err := decoder.DecodeElement(&text, &value); err != nil {
					return err
				}
				metadata.DCElements = append(metadata.DCElements, opfDCElement{Name: "creator", Attrs: value.Attr, Value: text})
				if strings.TrimSpace(metadata.Creator) == "" {
					metadata.Creator = text
				}
			case "language":
				var text string
				if err := decoder.DecodeElement(&text, &value); err != nil {
					return err
				}
				metadata.DCElements = append(metadata.DCElements, opfDCElement{Name: "language", Attrs: value.Attr, Value: text})
				if strings.TrimSpace(metadata.Language) == "" {
					metadata.Language = text
				}
			case "identifier":
				var identifier opfIdentifier
				if err := decoder.DecodeElement(&identifier, &value); err != nil {
					return err
				}
				metadata.Identifiers = append(metadata.Identifiers, identifier)
				if strings.TrimSpace(metadata.Identifier.Value) == "" {
					metadata.Identifier = identifier
				}
			case "meta":
				var meta opfMeta
				if err := decoder.DecodeElement(&meta, &value); err != nil {
					return err
				}
				metadata.Meta = append(metadata.Meta, meta)
			default:
				if isPreservedDCElement(value.Name.Local) {
					var text string
					if err := decoder.DecodeElement(&text, &value); err != nil {
						return err
					}
					metadata.DCElements = append(metadata.DCElements, opfDCElement{Name: value.Name.Local, Attrs: value.Attr, Value: text})
					continue
				}
				raw, err := readRawElement(decoder, value)
				if err != nil {
					return err
				}
				metadata.Raw = append(metadata.Raw, raw)
			}
		case xml.EndElement:
			if value.Name == start.Name {
				return nil
			}
		}
	}
}

func (metadata opfMetadata) MarshalXML(encoder *xml.Encoder, start xml.StartElement) error {
	start.Name.Local = "metadata"
	start.Attr = append(start.Attr, withoutXMLNamespaceDeclarations(metadata.Attrs)...)
	start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: "xmlns:dc"}, Value: firstNonEmptyString(metadata.DC, "http://purl.org/dc/elements/1.1/")})
	if err := encoder.EncodeToken(start); err != nil {
		return err
	}
	if err := encodeDCElements(encoder, metadata); err != nil {
		return err
	}
	for _, identifier := range metadataIdentifiers(metadata) {
		if err := encodeOPFIdentifier(encoder, identifier); err != nil {
			return err
		}
	}
	for _, meta := range metadata.Meta {
		if err := encoder.Encode(meta); err != nil {
			return err
		}
	}
	for _, raw := range metadata.Raw {
		if err := raw.EncodeXML(encoder); err != nil {
			return err
		}
	}
	return encoder.EncodeToken(start.End())
}

func isPreservedDCElement(name string) bool {
	switch name {
	case "contributor", "coverage", "date", "description", "format", "publisher", "relation", "rights", "source", "subject", "type":
		return true
	default:
		return false
	}
}

func encodeDCElements(encoder *xml.Encoder, metadata opfMetadata) error {
	elements := append([]opfDCElement(nil), metadata.DCElements...)
	if !hasDCElement(elements, "title") && strings.TrimSpace(metadata.Title) != "" {
		elements = append([]opfDCElement{{Name: "title", Value: metadata.Title}}, elements...)
	}
	if !hasDCElement(elements, "creator") && strings.TrimSpace(metadata.Creator) != "" {
		elements = append(elements, opfDCElement{Name: "creator", Value: metadata.Creator})
	}
	if !hasDCElement(elements, "language") && strings.TrimSpace(metadata.Language) != "" {
		elements = append(elements, opfDCElement{Name: "language", Value: metadata.Language})
	}
	for _, element := range elements {
		if strings.TrimSpace(element.Name) == "" || strings.TrimSpace(element.Value) == "" {
			continue
		}
		start := xml.StartElement{
			Name: xml.Name{Local: "dc:" + element.Name},
			Attr: withoutXMLNamespaceDeclarations(element.Attrs),
		}
		if err := encoder.EncodeElement(element.Value, start); err != nil {
			return err
		}
	}
	return nil
}

func hasDCElement(elements []opfDCElement, name string) bool {
	for _, element := range elements {
		if element.Name == name && strings.TrimSpace(element.Value) != "" {
			return true
		}
	}
	return false
}

func metadataIdentifiers(metadata opfMetadata) []opfIdentifier {
	if len(metadata.Identifiers) != 0 {
		return metadata.Identifiers
	}
	if strings.TrimSpace(metadata.Identifier.Value) == "" && strings.TrimSpace(metadata.Identifier.ID) == "" {
		return nil
	}
	return []opfIdentifier{metadata.Identifier}
}

func encodeOPFIdentifier(encoder *xml.Encoder, identifier opfIdentifier) error {
	return encoder.Encode(identifier)
}

func opfIdentifierAttrs(identifier opfIdentifier) []xml.Attr {
	attrs := append([]xml.Attr(nil), identifier.Attrs...)
	return setXMLAttr(attrs, xml.Name{Local: "id"}, identifier.ID)
}

func opfMetaAttrs(meta opfMeta) []xml.Attr {
	attrs := append([]xml.Attr(nil), meta.Attrs...)
	attrs = setXMLAttr(attrs, xml.Name{Local: "property"}, meta.Property)
	attrs = setXMLAttr(attrs, xml.Name{Local: "refines"}, meta.Refines)
	attrs = setXMLAttr(attrs, xml.Name{Local: "name"}, meta.Name)
	attrs = setXMLAttr(attrs, xml.Name{Local: "content"}, meta.Content)
	return attrs
}

func setXMLAttr(attrs []xml.Attr, name xml.Name, value string) []xml.Attr {
	value = strings.TrimSpace(value)
	for idx := range attrs {
		if attrs[idx].Name == name {
			if value == "" {
				return append(attrs[:idx], attrs[idx+1:]...)
			}
			attrs[idx].Value = value
			return attrs
		}
	}
	if value == "" {
		return attrs
	}
	return append(attrs, xml.Attr{Name: name, Value: value})
}

type opfMeta struct {
	Property string     `xml:"property,attr,omitempty"`
	Refines  string     `xml:"refines,attr,omitempty"`
	Name     string     `xml:"name,attr,omitempty"`
	Content  string     `xml:"content,attr,omitempty"`
	Value    string     `xml:",chardata"`
	Attrs    []xml.Attr `xml:"-"`
}

func normalizeOPFAttr(attr xml.Attr) xml.Attr {
	switch attr.Name.Space {
	case "http://www.idpf.org/2007/opf":
		attr.Name = xml.Name{Local: "opf:" + attr.Name.Local}
	case "http://www.w3.org/XML/1998/namespace":
		attr.Name = xml.Name{Local: "xml:" + attr.Name.Local}
	case "xmlns":
		attr.Name = xml.Name{Local: "xmlns:" + attr.Name.Local}
	}
	return attr
}

func (meta *opfMeta) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	*meta = opfMeta{}
	for _, attr := range start.Attr {
		meta.Attrs = append(meta.Attrs, attr)
		switch attr.Name.Local {
		case "property":
			meta.Property = attr.Value
		case "refines":
			meta.Refines = attr.Value
		case "name":
			meta.Name = attr.Value
		case "content":
			meta.Content = attr.Value
		}
	}
	return decoder.DecodeElement(&meta.Value, &start)
}

func (meta opfMeta) MarshalXML(encoder *xml.Encoder, start xml.StartElement) error {
	start.Name.Local = "meta"
	start.Attr = opfMetaAttrs(meta)
	return encoder.EncodeElement(meta.Value, start)
}

type opfManifest struct {
	Items []opfItem `xml:"item"`
}

type opfItem struct {
	ID           string     `xml:"id,attr"`
	Href         string     `xml:"href,attr"`
	MediaType    string     `xml:"media-type,attr"`
	Properties   string     `xml:"properties,attr,omitempty"`
	Fallback     string     `xml:"fallback,attr,omitempty"`
	MediaOverlay string     `xml:"media-overlay,attr,omitempty"`
	Attrs        []xml.Attr `xml:",any,attr"`
}

type opfSpine struct {
	ID                       string       `xml:"id,attr,omitempty"`
	Toc                      string       `xml:"toc,attr,omitempty"`
	PageProgressionDirection string       `xml:"page-progression-direction,attr,omitempty"`
	Itemrefs                 []opfItemref `xml:"itemref"`
	Attrs                    []xml.Attr   `xml:",any,attr"`
}

type opfItemref struct {
	ID         string     `xml:"id,attr,omitempty"`
	IDRef      string     `xml:"idref,attr"`
	Linear     string     `xml:"linear,attr,omitempty"`
	Properties string     `xml:"properties,attr,omitempty"`
	Attrs      []xml.Attr `xml:",any,attr"`
}

func buildSimpleEPUB(title, author, identifier string, modified time.Time, chapters []epubChapter) ([]byte, error) {
	files := map[string][]byte{
		"mimetype": []byte("application/epub+zip"),
		"META-INF/container.xml": mustXML(containerDocument{
			Version: "1.0",
			Xmlns:   "urn:oasis:names:tc:opendocument:xmlns:container",
			RootFile: containerFiles{
				RootFile: []containerRootFile{{
					FullPath:  "OEBPS/content.opf",
					MediaType: "application/oebps-package+xml",
				}},
			},
		}),
	}

	manifest := []opfItem{{
		ID:         "nav",
		Href:       "nav.xhtml",
		MediaType:  "application/xhtml+xml",
		Properties: "nav",
	}}
	spine := []opfItemref{}
	bodyMatter := ""
	for idx, chapter := range chapters {
		id := fmt.Sprintf("chapter-%03d", idx+1)
		document, err := buildXHTMLDocumentForEPUBVersionWithViewportAndFileName(chapter.Title, chapter.BodyHTML, "3.0", "", chapter.FileName)
		if err != nil {
			return nil, fmt.Errorf("build chapter %q: %w", chapter.Title, err)
		}
		files[path.Join("OEBPS", chapter.FileName)] = []byte(document.Content)
		manifest = append(manifest, opfItem{
			ID:        id,
			Href:      chapter.FileName,
			MediaType: "application/xhtml+xml",
		})
		spine = append(spine, opfItemref{IDRef: id})
		if bodyMatter == "" && !chapter.FrontMatter {
			bodyMatter = chapter.FileName
		}
	}
	files["OEBPS/nav.xhtml"] = []byte(buildNavDocument(title, chapters, bodyMatter))
	pkg := opfPackage{
		Xmlns:    "http://www.idpf.org/2007/opf",
		UniqueID: "bookid",
		Version:  "3.0",
		Metadata: opfMetadata{
			DC:         "http://purl.org/dc/elements/1.1/",
			Title:      title,
			Creator:    author,
			Language:   "en",
			Identifier: opfIdentifier{ID: "bookid", Value: firstNonEmptyString(identifier, safeIdentifier(title))},
			Meta:       []opfMeta{modifiedMeta(modified)},
		},
		Manifest: opfManifest{Items: manifest},
		Spine:    opfSpine{Itemrefs: spine},
	}
	files["OEBPS/content.opf"] = mustXML(pkg)
	return writeStructurallyValidatedEPUBArchive(files)
}

func wrapEPUBWithPreface(original []byte, title, author, identifier string, modified time.Time, prefaceHTML string) ([]byte, error) {
	if strings.TrimSpace(prefaceHTML) == "" {
		return original, nil
	}
	return rewriteEPUBPackage(original, title, author, identifier, modified, prefaceHTML, false)
}

func normalizeEPUBMetadata(original []byte, title, author, identifier string, modified time.Time) ([]byte, error) {
	return rewriteEPUBPackage(original, title, author, identifier, modified, "", true)
}

func rewriteEPUBPackage(original []byte, title, author, identifier string, modified time.Time, prefaceHTML string, replaceIdentifier bool) ([]byte, error) {
	session, err := openEPUBPackage(original)
	if err != nil {
		return nil, err
	}
	if _, ok := session.Files["mimetype"]; !ok {
		session.Files["mimetype"] = []byte("application/epub+zip")
	}

	pkg := &session.Package
	// Upgrading keeps the spine, so saved reading positions still resolve.
	upgrade := strings.TrimSpace(prefaceHTML) != "" && strings.HasPrefix(strings.TrimSpace(pkg.Version), "2")
	var legacyTOC []epubChapter
	if upgrade {
		if legacyTOC, err = session.memberNavigation(""); err != nil {
			return nil, err
		}
	}
	if upgrade {
		upgradeLegacyMetadata(pkg)
	}
	if strings.TrimSpace(pkg.Version) == "" || upgrade {
		pkg.Version = "3.0"
	}
	if strings.TrimSpace(pkg.Xmlns) == "" {
		pkg.Xmlns = "http://www.idpf.org/2007/opf"
	}
	pkg.Prefix = sanitizePackagePrefix(pkg.Prefix)
	if strings.TrimSpace(pkg.Metadata.DC) == "" {
		pkg.Metadata.DC = "http://purl.org/dc/elements/1.1/"
	}
	opfDir := session.Dir
	fixedLayout := packageIsPrePaginated(*pkg)
	prefaceViewport := fixedLayoutViewport(fixedLayout, *pkg, session.Files, opfDir)
	ensurePackageMetadata(pkg, title, author, identifier, modified, session.Files, opfDir, replaceIdentifier)
	if strings.HasPrefix(strings.TrimSpace(pkg.Version), "2") && strings.TrimSpace(pkg.Spine.Toc) == "" {
		pkg.Spine.Toc = firstNCXID(pkg.Manifest.Items)
	}
	if replaceIdentifier {
		if err := normalizeManifestXHTMLTitles(session.Files, *pkg, opfDir, title); err != nil {
			return nil, err
		}
	}

	if strings.TrimSpace(prefaceHTML) != "" {
		prefaceFileName, prefaceArchivePath := uniquePrefacePath(session.Files, opfDir)
		prefaceDocument, err := buildXHTMLDocumentForEPUBVersionWithViewportAndFileName(prefaceTitle, prefaceHTML, pkg.Version, prefaceViewport, prefaceFileName)
		if err != nil {
			return nil, fmt.Errorf("build preface: %w", err)
		}
		session.Files[prefaceArchivePath] = []byte(prefaceDocument.Content)

		manifestID := uniqueManifestID(pkg.Manifest.Items, "serial-sync-preface")
		manifestItem := opfItem{
			ID:        manifestID,
			Href:      prefaceFileName,
			MediaType: "application/xhtml+xml",
		}
		if !manifestHasID(pkg.Manifest.Items, manifestItem.ID) {
			pkg.Manifest.Items = append([]opfItem{manifestItem}, pkg.Manifest.Items...)
		}
		if !spineHasID(pkg.Spine.Itemrefs, manifestItem.ID) {
			pkg.Spine.Itemrefs = append([]opfItemref{{IDRef: manifestItem.ID}}, pkg.Spine.Itemrefs...)
		}
		if upgrade {
			if err := session.replaceLegacyTOC(title, legacyTOC); err != nil {
				return nil, err
			}
		}
		if err := session.navigatePreface(prefaceArchivePath); err != nil {
			return nil, err
		}
	}
	return session.write(packageIndented)
}

func upgradeLegacyMetadata(pkg *opfPackage) {
	metadata := &pkg.Metadata
	for _, meta := range metadata.Meta {
		if meta.Name != "cover" {
			continue
		}
		for i := range pkg.Manifest.Items {
			if item := &pkg.Manifest.Items[i]; item.ID == meta.Content && strings.HasPrefix(item.MediaType, "image/") {
				item.Properties = strings.TrimSpace(item.Properties + " cover-image")
			}
		}
	}
	legacy := func(attr xml.Attr) (string, bool) {
		if attr.Name.Space == "http://www.idpf.org/2007/opf" {
			return attr.Name.Local, true
		}
		if attr.Name.Space == "" && strings.HasPrefix(attr.Name.Local, "opf:") {
			return strings.TrimPrefix(attr.Name.Local, "opf:"), true
		}
		return "", false
	}
	for i := range metadata.DCElements {
		element := &metadata.DCElements[i]
		id := ""
		var kept []xml.Attr
		var refinements []opfMeta
		for _, attr := range element.Attrs {
			name, isLegacy := legacy(attr)
			switch {
			case !isLegacy:
				if attr.Name.Local == "id" {
					id = attr.Value
				}
				kept = append(kept, attr)
			case name == "role":
				refinements = append(refinements, opfMeta{Property: "role", Value: attr.Value, Attrs: []xml.Attr{{Name: xml.Name{Local: "scheme"}, Value: "marc:relators"}}})
			case name == "file-as":
				refinements = append(refinements, opfMeta{Property: "file-as", Value: attr.Value})
			}
		}
		if len(refinements) > 0 && id == "" {
			id = fmt.Sprintf("serial-sync-%s-%d", element.Name, i+1)
			kept = append(kept, xml.Attr{Name: xml.Name{Local: "id"}, Value: id})
		}
		for _, refinement := range refinements {
			refinement.Refines = "#" + id
			metadata.Meta = append(metadata.Meta, refinement)
		}
		element.Attrs = kept
	}
	stripLegacy := func(identifier *opfIdentifier) {
		kept := identifier.Attrs[:0]
		for _, attr := range identifier.Attrs {
			if _, isLegacy := legacy(attr); !isLegacy {
				kept = append(kept, attr)
			}
		}
		identifier.Attrs = kept
	}
	stripLegacy(&metadata.Identifier)
	for i := range metadata.Identifiers {
		stripLegacy(&metadata.Identifiers[i])
	}
}

func manifestHasID(items []opfItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func spineHasID(items []opfItemref, id string) bool {
	for _, item := range items {
		if item.IDRef == id {
			return true
		}
	}
	return false
}

func ensurePackageMetadata(pkg *opfPackage, title, author, identifier string, modified time.Time, files map[string][]byte, opfDir string, replaceIdentifier bool) {
	selectPackageIdentifier(pkg)
	if strings.TrimSpace(pkg.Metadata.Title) == "" {
		pkg.Metadata.Title = title
	}
	ensureDCElement(&pkg.Metadata, "title", pkg.Metadata.Title)
	if strings.TrimSpace(pkg.Metadata.Creator) == "" {
		pkg.Metadata.Creator = author
	}
	ensureDCElement(&pkg.Metadata, "creator", pkg.Metadata.Creator)
	if strings.TrimSpace(pkg.Metadata.Language) == "" {
		pkg.Metadata.Language = "en"
	}
	ensureDCElement(&pkg.Metadata, "language", pkg.Metadata.Language)
	if strings.TrimSpace(pkg.Metadata.DC) == "" {
		pkg.Metadata.DC = "http://purl.org/dc/elements/1.1/"
	}
	if strings.TrimSpace(pkg.UniqueID) == "" {
		pkg.UniqueID = "bookid"
	}
	if strings.TrimSpace(pkg.Metadata.Identifier.ID) == "" {
		pkg.Metadata.Identifier.ID = pkg.UniqueID
	}
	if strings.TrimSpace(pkg.Metadata.Identifier.Value) == "" {
		pkg.Metadata.Identifier.Value = firstNonEmptyString(identifier, safeIdentifier(title))
	}
	if replaceIdentifier && strings.TrimSpace(identifier) != "" {
		pkg.Metadata.Identifier.Value = identifier
	}
	if strings.TrimSpace(pkg.Metadata.Identifier.ID) == "" {
		pkg.Metadata.Identifier.ID = pkg.UniqueID
	}
	pkg.UniqueID = pkg.Metadata.Identifier.ID
	pkg.Attrs = sanitizePackageAttrs(pkg.Attrs)
	pkg.Spine.Attrs = nil
	for idx := range pkg.Spine.Itemrefs {
		pkg.Spine.Itemrefs[idx].Attrs = nil
	}
	syncMetadataIdentifiers(&pkg.Metadata)
	declaredPrefixes := packagePrefixNames(pkg.Prefix)
	for idx := range pkg.Manifest.Items {
		pkg.Manifest.Items[idx].Attrs = nil
		pkg.Manifest.Items[idx].Properties = sanitizeManifestProperties(pkg.Manifest.Items[idx], files, opfDir, declaredPrefixes)
	}
	metadataMeta := pkg.Metadata.Meta
	pkg.Metadata.Meta = nil
	if strings.HasPrefix(strings.TrimSpace(pkg.Version), "3") {
		pkg.Metadata.Meta = sanitizeMetadataMeta(metadataMeta, modified, packagePrefixNames(pkg.Prefix))
	} else {
		pkg.Metadata.Meta = sanitizeLegacyMetadataMeta(metadataMeta)
	}
}

func ensureDCElement(metadata *opfMetadata, name, value string) {
	value = strings.TrimSpace(value)
	if value == "" || hasDCElement(metadata.DCElements, name) {
		return
	}
	metadata.DCElements = append(metadata.DCElements, opfDCElement{Name: name, Value: value})
}

func syncMetadataIdentifiers(metadata *opfMetadata) {
	if strings.TrimSpace(metadata.Identifier.ID) == "" && strings.TrimSpace(metadata.Identifier.Value) == "" {
		return
	}
	if len(metadata.Identifiers) == 0 {
		metadata.Identifiers = []opfIdentifier{metadata.Identifier}
		return
	}
	for idx := range metadata.Identifiers {
		if metadata.Identifiers[idx].ID == metadata.Identifier.ID {
			metadata.Identifiers[idx] = metadata.Identifier
			return
		}
	}
	metadata.Identifiers = append(metadata.Identifiers, metadata.Identifier)
}

func selectPackageIdentifier(pkg *opfPackage) {
	if strings.TrimSpace(pkg.UniqueID) == "" || len(pkg.Metadata.Identifiers) == 0 {
		return
	}
	for _, identifier := range pkg.Metadata.Identifiers {
		if identifier.ID == pkg.UniqueID {
			pkg.Metadata.Identifier = identifier
			return
		}
	}
}

func modifiedMeta(modified time.Time) opfMeta {
	return opfMeta{Property: "dcterms:modified", Value: formatEPUBModified(modified)}
}

func sanitizeMetadataMeta(existing []opfMeta, modified time.Time, declaredPrefixes map[string]struct{}) []opfMeta {
	meta := make([]opfMeta, 0, len(existing)+1)
	seen := map[string]struct{}{}
	for _, item := range existing {
		property := strings.TrimSpace(item.Property)
		refines := strings.TrimSpace(item.Refines)
		if property == "" && (item.Name == aboutMarker || item.Name == authorMarker) {
			meta = append(meta, opfMeta{Name: item.Name, Content: item.Content})
			continue
		}
		if property == "" || strings.HasPrefix(property, "calibre:") {
			continue
		}
		if property == "dcterms:modified" && refines == "" {
			continue
		}
		value := strings.TrimSpace(item.Value)
		if value == "" {
			continue
		}
		if !allowedMetadataProperty(property, refines != "", declaredPrefixes) {
			continue
		}
		if refines == "" && refinementOnlyMetadataProperty(property) {
			continue
		}
		item.Property = property
		item.Refines = refines
		item.Value = value
		key := xmlAttrsSignature(opfMetaAttrs(item)) + "\x00" + value
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		meta = append(meta, item)
	}
	meta = append(meta, modifiedMeta(modified))
	return meta
}

func xmlAttrsSignature(attrs []xml.Attr) string {
	parts := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		parts = append(parts, attr.Name.Space+"\x00"+attr.Name.Local+"\x00"+attr.Value)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x01")
}

func sanitizeLegacyMetadataMeta(existing []opfMeta) []opfMeta {
	meta := make([]opfMeta, 0, len(existing))
	seen := map[string]struct{}{}
	for _, item := range existing {
		if strings.TrimSpace(item.Property) != "" {
			continue
		}
		item.Name = strings.TrimSpace(item.Name)
		item.Content = strings.TrimSpace(item.Content)
		if item.Name == "" && item.Content == "" {
			continue
		}
		key := item.Name + "\x00" + item.Content + "\x00" + strings.TrimSpace(item.Value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		meta = append(meta, item)
	}
	return meta
}

func packageIsPrePaginated(pkg opfPackage) bool {
	for _, meta := range pkg.Metadata.Meta {
		if strings.TrimSpace(meta.Property) == "rendition:layout" && strings.TrimSpace(meta.Value) == "pre-paginated" {
			return true
		}
	}
	return false
}

func fixedLayoutViewport(fixedLayout bool, pkg opfPackage, files map[string][]byte, opfDir string) string {
	if !fixedLayout {
		return ""
	}
	if viewport := packageViewport(pkg, files, opfDir); viewport != "" {
		return viewport
	}
	return "width=600, height=800"
}

func packageViewport(pkg opfPackage, files map[string][]byte, opfDir string) string {
	manifest := make(map[string]opfItem, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		manifest[item.ID] = item
	}
	for _, itemref := range pkg.Spine.Itemrefs {
		item, ok := manifest[itemref.IDRef]
		if !ok || item.MediaType != "application/xhtml+xml" {
			continue
		}
		itemPath, err := resolveManifestHref(opfDir, item.Href)
		if err != nil {
			continue
		}
		if viewport := xhtmlViewport(files[itemPath]); viewport != "" {
			return viewport
		}
	}
	return ""
}

func xhtmlViewport(content []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "meta" {
			continue
		}
		var name string
		var value string
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "name":
				name = attr.Value
			case "content":
				value = attr.Value
			}
		}
		if strings.EqualFold(strings.TrimSpace(name), "viewport") {
			return strings.TrimSpace(value)
		}
	}
}

func normalizeManifestXHTMLTitles(files map[string][]byte, pkg opfPackage, opfDir, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	for _, item := range pkg.Manifest.Items {
		if item.MediaType != "application/xhtml+xml" {
			continue
		}
		itemPath, err := resolveManifestHref(opfDir, item.Href)
		if err != nil {
			return err
		}
		content, ok := files[itemPath]
		if !ok {
			continue
		}
		rewritten, err := normalizeXHTMLTitle(content, title)
		if err != nil {
			return fmt.Errorf("normalize XHTML title %q: %w", itemPath, err)
		}
		files[itemPath] = rewritten
	}
	return nil
}

func normalizeXHTMLTitle(content []byte, title string) ([]byte, error) {
	lower := bytes.ToLower(content)
	start := bytes.Index(lower, []byte("<title>"))
	if start < 0 {
		return append([]byte(nil), content...), nil
	}
	valueStart := start + len("<title>")
	relativeEnd := bytes.Index(lower[valueStart:], []byte("</title>"))
	if relativeEnd < 0 {
		return nil, fmt.Errorf("title element is not closed")
	}
	valueEnd := valueStart + relativeEnd
	var out bytes.Buffer
	out.Grow(len(content) + len(title))
	out.Write(content[:valueStart])
	out.WriteString(escapeHTML(title))
	out.Write(content[valueEnd:])
	return out.Bytes(), nil
}

func sanitizePackagePrefix(prefix string) string {
	fields := strings.Fields(prefix)
	if len(fields) < 2 {
		return strings.TrimSpace(prefix)
	}
	kept := make([]string, 0, len(fields))
	for idx := 0; idx < len(fields); {
		name := fields[idx]
		if idx+1 >= len(fields) {
			kept = append(kept, fields[idx:]...)
			break
		}
		iri := fields[idx+1]
		if strings.EqualFold(strings.TrimSuffix(name, ":"), "calibre") {
			idx += 2
			continue
		}
		kept = append(kept, name, iri)
		idx += 2
	}
	return strings.Join(kept, " ")
}

func packagePrefixNames(prefix string) map[string]struct{} {
	names := map[string]struct{}{}
	fields := strings.Fields(prefix)
	for idx := 0; idx < len(fields)-1; idx += 2 {
		name := strings.TrimSuffix(strings.TrimSpace(fields[idx]), ":")
		if name != "" {
			names[name] = struct{}{}
		}
	}
	return names
}

func allowedMetadataProperty(property string, refined bool, declaredPrefixes map[string]struct{}) bool {
	if strings.Contains(property, ":") {
		prefix := strings.SplitN(property, ":", 2)[0]
		switch prefix {
		case "a11y", "dcterms", "marc", "media", "onix", "rendition", "schema", "xsd":
			return true
		default:
			_, ok := declaredPrefixes[prefix]
			return ok
		}
	}
	if refined && refinementOnlyMetadataProperty(property) {
		return true
	}
	switch property {
	case "belongs-to-collection":
		return true
	default:
		return false
	}
}

func refinementOnlyMetadataProperty(property string) bool {
	switch property {
	case "alternate-script", "authority", "collection-type", "display-seq", "file-as", "group-position", "identifier-type", "meta-auth", "role", "term", "title-type":
		return true
	default:
		return false
	}
}

func formatEPUBModified(modified time.Time) string {
	if modified.IsZero() {
		modified = time.Unix(0, 0)
	}
	return modified.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
}

func firstNCXID(items []opfItem) string {
	for _, item := range items {
		if item.MediaType == "application/x-dtbncx+xml" {
			return item.ID
		}
	}
	return ""
}

func uniquePrefacePath(files map[string][]byte, opfDir string) (fileName, archivePath string) {
	for idx := 1; ; idx++ {
		fileName = "serial-sync-preface.xhtml"
		if idx > 1 {
			fileName = fmt.Sprintf("serial-sync-preface-%d.xhtml", idx)
		}
		archivePath = fileName
		if opfDir != "" {
			archivePath = path.Join(opfDir, fileName)
		}
		if _, exists := files[archivePath]; !exists {
			return fileName, archivePath
		}
	}
}

func uniqueManifestID(items []opfItem, preferred string) string {
	if !manifestHasID(items, preferred) {
		return preferred
	}
	for idx := 2; ; idx++ {
		candidate := fmt.Sprintf("%s-%d", preferred, idx)
		if !manifestHasID(items, candidate) {
			return candidate
		}
	}
}

func sanitizeManifestProperties(item opfItem, files map[string][]byte, opfDir string, declaredPrefixes map[string]struct{}) string {
	kept := []string{}
	seen := map[string]struct{}{}
	for _, property := range strings.Fields(item.Properties) {
		switch property {
		case "cover-image", "nav", "remote-resources", "scripted", "switch":
			kept = appendManifestProperty(kept, seen, property)
		case "mathml":
			if manifestItemUsesMathML(item, files, opfDir) {
				kept = appendManifestProperty(kept, seen, property)
			}
		case "svg":
			if manifestItemUsesSVG(item, files, opfDir) {
				kept = appendManifestProperty(kept, seen, property)
			}
		default:
			if propertyPrefixDeclared(property, declaredPrefixes) {
				kept = appendManifestProperty(kept, seen, property)
			}
		}
	}
	if item.MediaType == "application/xhtml+xml" {
		if manifestItemUsesMathML(item, files, opfDir) {
			kept = appendManifestProperty(kept, seen, "mathml")
		}
		if manifestItemUsesSVG(item, files, opfDir) {
			kept = appendManifestProperty(kept, seen, "svg")
		}
	}
	return strings.Join(kept, " ")
}

func sanitizePackageAttrs(attrs []xml.Attr) []xml.Attr {
	kept := []xml.Attr{}
	for _, attr := range attrs {
		if allowedPackageAttr(attr) {
			kept = append(kept, attr)
		}
	}
	return kept
}

func allowedPackageAttr(attr xml.Attr) bool {
	if attr.Name.Space == "xml" && attr.Name.Local == "lang" {
		return true
	}
	if attr.Name.Space != "" {
		return false
	}
	switch attr.Name.Local {
	case "dir", "id", "lang":
		return true
	default:
		return false
	}
}

func propertyPrefixDeclared(property string, declaredPrefixes map[string]struct{}) bool {
	prefix, _, ok := strings.Cut(property, ":")
	if !ok || strings.TrimSpace(prefix) == "" {
		return false
	}
	_, declared := declaredPrefixes[prefix]
	return declared
}

func appendManifestProperty(properties []string, seen map[string]struct{}, property string) []string {
	if _, ok := seen[property]; ok {
		return properties
	}
	seen[property] = struct{}{}
	return append(properties, property)
}

func manifestItemUsesSVG(item opfItem, files map[string][]byte, opfDir string) bool {
	if item.MediaType == "image/svg+xml" {
		return true
	}
	return manifestItemUsesXMLNamespace(item, files, opfDir, "svg", "http://www.w3.org/2000/svg")
}

func manifestItemUsesMathML(item opfItem, files map[string][]byte, opfDir string) bool {
	return manifestItemUsesXMLNamespace(item, files, opfDir, "math", "http://www.w3.org/1998/Math/MathML")
}

func manifestItemUsesXMLNamespace(item opfItem, files map[string][]byte, opfDir, localName, namespace string) bool {
	if item.MediaType != "application/xhtml+xml" {
		return false
	}
	itemPath, err := resolveManifestHref(opfDir, item.Href)
	if err != nil {
		return false
	}
	content, ok := files[itemPath]
	if !ok {
		return false
	}
	decoder := xml.NewDecoder(bytes.NewReader(content))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == localName && (start.Name.Space == namespace || start.Name.Space == "") {
			return true
		}
	}
}

func buildNavDocument(title string, chapters []epubChapter, bodyMatter string) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><meta charset="utf-8" /><title>`)
	builder.WriteString(escapeHTML(title))
	builder.WriteString(`</title></head><body><nav epub:type="toc" id="toc"><h1>`)
	builder.WriteString(escapeHTML(title))
	builder.WriteString(`</h1><ol>`)
	var writeEntries func([]epubChapter)
	writeEntries = func(entries []epubChapter) {
		for _, chapter := range entries {
			builder.WriteString(`<li>`)
			if chapter.FileName == "" {
				builder.WriteString(`<span>` + escapeHTML(chapter.Title) + `</span>`)
			} else {
				builder.WriteString(`<a href="` + escapeHTML(chapter.FileName) + `">` + escapeHTML(chapter.Title) + `</a>`)
			}
			if len(chapter.Children) > 0 {
				builder.WriteString(`<ol>`)
				writeEntries(chapter.Children)
				builder.WriteString(`</ol>`)
			}
			builder.WriteString(`</li>`)
		}
	}
	writeEntries(chapters)
	builder.WriteString(`</ol></nav>`)
	builder.WriteString(landmarksNav(bodyMatter))
	builder.WriteString(`</body></html>`)
	return builder.String()
}

func landmarksNav(bodyMatter string) string {
	if bodyMatter == "" {
		return ""
	}
	return `<nav epub:type="landmarks" hidden=""><ol><li><a epub:type="bodymatter" href="` + escapeHTML(bodyMatter) + `">Start of story</a></li></ol></nav>`
}

func writeEPUBArchive(files map[string][]byte) ([]byte, error) {
	var out bytes.Buffer
	writer := zip.NewWriter(&out)

	mimetypeHeader := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	mimetypeHeader.SetMode(0o644)
	entry, err := writer.CreateHeader(mimetypeHeader)
	if err != nil {
		return nil, err
	}
	if _, err := entry.Write(files["mimetype"]); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(files))
	for name := range files {
		if name == "mimetype" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o644)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(files[name]); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeStructurallyValidatedEPUBArchive(files map[string][]byte) ([]byte, error) {
	content, err := writeEPUBArchive(files)
	if err != nil {
		return nil, err
	}
	if err := validateEPUBArchiveStructure(content); err != nil {
		return nil, err
	}
	return content, nil
}

func mustXML(value any) []byte {
	data, err := xml.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	return append([]byte(xml.Header), data...)
}

func safeIdentifier(title string) string {
	value := strings.TrimSpace(title)
	if value == "" {
		return "serial-sync-book"
	}
	return "serial-sync:" + value
}
