package artifact

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

type epubChapter struct {
	FileName string
	Title    string
	BodyHTML string
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
	XMLName  xml.Name    `xml:"package"`
	Xmlns    string      `xml:"xmlns,attr,omitempty"`
	UniqueID string      `xml:"unique-identifier,attr,omitempty"`
	Version  string      `xml:"version,attr,omitempty"`
	Metadata opfMetadata `xml:"metadata"`
	Manifest opfManifest `xml:"manifest"`
	Spine    opfSpine    `xml:"spine"`
}

type opfMetadata struct {
	XMLName xml.Name `xml:"metadata"`
	DC      string   `xml:"xmlns:dc,attr,omitempty"`
	Title   string   `xml:"dc:title,omitempty"`
	Creator string   `xml:"dc:creator,omitempty"`
	Language string  `xml:"dc:language,omitempty"`
	Identifier opfIdentifier `xml:"dc:identifier"`
}

type opfIdentifier struct {
	ID    string `xml:"id,attr,omitempty"`
	Value string `xml:",chardata"`
}

type opfManifest struct {
	Items []opfItem `xml:"item"`
}

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr,omitempty"`
}

type opfSpine struct {
	Itemrefs []opfItemref `xml:"itemref"`
}

type opfItemref struct {
	IDRef string `xml:"idref,attr"`
}

func buildSimpleEPUB(title, author string, chapters []epubChapter) ([]byte, error) {
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
	for idx, chapter := range chapters {
		id := fmt.Sprintf("chapter-%03d", idx+1)
		files[path.Join("OEBPS", chapter.FileName)] = []byte(chapter.BodyHTML)
		manifest = append(manifest, opfItem{
			ID:        id,
			Href:      chapter.FileName,
			MediaType: "application/xhtml+xml",
		})
		spine = append(spine, opfItemref{IDRef: id})
	}
	files["OEBPS/nav.xhtml"] = []byte(buildNavDocument(title, chapters))
	files["OEBPS/content.opf"] = mustXML(opfPackage{
		Xmlns:    "http://www.idpf.org/2007/opf",
		UniqueID: "bookid",
		Version:  "3.0",
		Metadata: opfMetadata{
			DC:         "http://purl.org/dc/elements/1.1/",
			Title:      title,
			Creator:    author,
			Language:   "en",
			Identifier: opfIdentifier{ID: "bookid", Value: safeIdentifier(title)},
		},
		Manifest: opfManifest{Items: manifest},
		Spine:    opfSpine{Itemrefs: spine},
	})
	return writeEPUBArchive(files)
}

func wrapEPUBWithPreface(original []byte, title, author, prefaceHTML string) ([]byte, error) {
	if strings.TrimSpace(prefaceHTML) == "" {
		return original, nil
	}
	reader, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		return nil, err
	}

	files := map[string][]byte{}
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		files[file.Name] = data
	}
	if _, ok := files["mimetype"]; !ok {
		files["mimetype"] = []byte("application/epub+zip")
	}
	if _, ok := files["mimetype"]; !ok {
		files["mimetype"] = []byte("application/epub+zip")
	}

	containerData, ok := files["META-INF/container.xml"]
	if !ok {
		return nil, fmt.Errorf("epub missing META-INF/container.xml")
	}
	var container containerDocument
	if err := xml.Unmarshal(containerData, &container); err != nil {
		return nil, err
	}
	if len(container.RootFile.RootFile) == 0 {
		return nil, fmt.Errorf("epub container missing rootfile")
	}
	opfPath := container.RootFile.RootFile[0].FullPath
	opfData, ok := files[opfPath]
	if !ok {
		return nil, fmt.Errorf("epub missing package document %q", opfPath)
	}
	var pkg opfPackage
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, err
	}
	if strings.TrimSpace(pkg.Metadata.Title) == "" {
		pkg.Metadata.Title = title
	}
	if strings.TrimSpace(pkg.Metadata.Creator) == "" {
		pkg.Metadata.Creator = author
	}
	if strings.TrimSpace(pkg.Metadata.Language) == "" {
		pkg.Metadata.Language = "en"
	}
	if strings.TrimSpace(pkg.Metadata.Identifier.Value) == "" {
		pkg.Metadata.Identifier = opfIdentifier{ID: "bookid", Value: safeIdentifier(title)}
	}
	if strings.TrimSpace(pkg.UniqueID) == "" {
		pkg.UniqueID = "bookid"
	}
	if strings.TrimSpace(pkg.Version) == "" {
		pkg.Version = "3.0"
	}
	if strings.TrimSpace(pkg.Xmlns) == "" {
		pkg.Xmlns = "http://www.idpf.org/2007/opf"
	}
	if strings.TrimSpace(pkg.Metadata.DC) == "" {
		pkg.Metadata.DC = "http://purl.org/dc/elements/1.1/"
	}

	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}
	prefaceFileName := "serial-sync-preface.xhtml"
	prefaceArchivePath := prefaceFileName
	if opfDir != "" {
		prefaceArchivePath = path.Join(opfDir, prefaceFileName)
	}
	files[prefaceArchivePath] = []byte(prefaceHTML)

	manifestItem := opfItem{
		ID:        "serial-sync-preface",
		Href:      prefaceFileName,
		MediaType: "application/xhtml+xml",
	}
	if !manifestHasID(pkg.Manifest.Items, manifestItem.ID) {
		pkg.Manifest.Items = append([]opfItem{manifestItem}, pkg.Manifest.Items...)
	}
	if !spineHasID(pkg.Spine.Itemrefs, manifestItem.ID) {
		pkg.Spine.Itemrefs = append([]opfItemref{{IDRef: manifestItem.ID}}, pkg.Spine.Itemrefs...)
	}
	files[opfPath] = mustXML(pkg)
	return writeEPUBArchive(files)
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

func buildNavDocument(title string, chapters []epubChapter) string {
	var builder strings.Builder
	builder.WriteString(`<!doctype html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><meta charset="utf-8"/><title>`)
	builder.WriteString(escapeHTML(title))
	builder.WriteString(`</title></head><body><nav epub:type="toc" id="toc"><h1>`)
	builder.WriteString(escapeHTML(title))
	builder.WriteString(`</h1><ol>`)
	for _, chapter := range chapters {
		builder.WriteString(`<li><a href="`)
		builder.WriteString(escapeHTML(chapter.FileName))
		builder.WriteString(`">`)
		builder.WriteString(escapeHTML(chapter.Title))
		builder.WriteString(`</a></li>`)
	}
	builder.WriteString(`</ol></nav></body></html>`)
	return builder.String()
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
