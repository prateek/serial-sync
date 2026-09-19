package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func validateEPUBArchive(content []byte) error {
	if err := validateEPUBArchiveStructure(content); err != nil {
		return err
	}
	return validateEPUBCheck(content)
}

func validateEPUBArchiveStructure(content []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return fmt.Errorf("read epub zip: %w", err)
	}
	if len(reader.File) == 0 {
		return fmt.Errorf("epub zip is empty")
	}
	if err := validateMimetypeEntry(reader.File[0]); err != nil {
		return err
	}

	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		files[file.Name] = file
	}
	containerFile, ok := files["META-INF/container.xml"]
	if !ok {
		return fmt.Errorf("epub missing META-INF/container.xml")
	}
	containerData, err := readZipFile(containerFile)
	if err != nil {
		return err
	}
	var container containerDocument
	if err := xml.Unmarshal(containerData, &container); err != nil {
		return fmt.Errorf("parse META-INF/container.xml: %w", err)
	}
	if len(container.RootFile.RootFile) == 0 {
		return fmt.Errorf("epub container missing rootfile")
	}
	opfPath := container.RootFile.RootFile[0].FullPath
	opfFile, ok := files[opfPath]
	if !ok {
		return fmt.Errorf("epub missing package document %q", opfPath)
	}
	opfData, err := readZipFile(opfFile)
	if err != nil {
		return err
	}
	var pkg opfPackage
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return fmt.Errorf("parse package document %q: %w", opfPath, err)
	}
	selectPackageIdentifier(&pkg)
	if err := validateOPFPackage(pkg); err != nil {
		return fmt.Errorf("validate package document %q: %w", opfPath, err)
	}
	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}
	for _, item := range pkg.Manifest.Items {
		itemPath, err := resolveManifestHref(opfDir, item.Href)
		if err != nil {
			return fmt.Errorf("manifest item %q has invalid href %q: %w", item.ID, item.Href, err)
		}
		itemFile, ok := files[itemPath]
		if !ok {
			return fmt.Errorf("manifest item %q missing resource %q", item.ID, itemPath)
		}
		if item.MediaType != "application/xhtml+xml" {
			continue
		}
		itemData, err := readZipFile(itemFile)
		if err != nil {
			return err
		}
		if err := validateXMLDocument(itemData); err != nil {
			return fmt.Errorf("manifest item %q is not XML well-formed: %w", item.ID, err)
		}
	}
	return nil
}

func resolveManifestHref(opfDir, href string) (string, error) {
	parsed, err := url.Parse(href)
	if err != nil {
		return "", err
	}
	if parsed.IsAbs() || parsed.Opaque != "" {
		return "", fmt.Errorf("href must be a package-relative URL")
	}
	unescaped, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(unescaped) == "" {
		return "", fmt.Errorf("href path is empty")
	}
	if opfDir == "" {
		return path.Clean(unescaped), nil
	}
	return path.Join(opfDir, unescaped), nil
}

func validateMimetypeEntry(file *zip.File) error {
	if file.Name != "mimetype" {
		return fmt.Errorf("epub first zip entry is %q, want mimetype", file.Name)
	}
	if file.Method != zip.Store {
		return fmt.Errorf("epub mimetype entry must be stored without compression")
	}
	if len(file.Extra) != 0 {
		return fmt.Errorf("epub mimetype entry must not have a zip extra field")
	}
	data, err := readZipFile(file)
	if err != nil {
		return err
	}
	if string(data) != "application/epub+zip" {
		return fmt.Errorf("epub mimetype entry has invalid content")
	}
	return nil
}

func validateOPFPackage(pkg opfPackage) error {
	if strings.TrimSpace(pkg.UniqueID) == "" {
		return fmt.Errorf("package unique-identifier is required")
	}
	if strings.TrimSpace(pkg.Metadata.Identifier.ID) != strings.TrimSpace(pkg.UniqueID) {
		return fmt.Errorf("package unique-identifier %q does not match dc:identifier id %q", pkg.UniqueID, pkg.Metadata.Identifier.ID)
	}
	if strings.TrimSpace(pkg.Metadata.Identifier.Value) == "" {
		return fmt.Errorf("dc:identifier is required")
	}
	if strings.TrimSpace(pkg.Metadata.Title) == "" {
		return fmt.Errorf("dc:title is required")
	}
	if strings.TrimSpace(pkg.Metadata.Language) == "" {
		return fmt.Errorf("dc:language is required")
	}
	if strings.HasPrefix(strings.TrimSpace(pkg.Version), "3") {
		modified := 0
		nav := 0
		for _, meta := range pkg.Metadata.Meta {
			if meta.Property == "dcterms:modified" && strings.TrimSpace(meta.Refines) == "" {
				modified++
				value := strings.TrimSpace(meta.Value)
				if value == "" {
					return fmt.Errorf("dcterms:modified is empty")
				}
				if !validEPUBModifiedTimestamp(value) {
					return fmt.Errorf("dcterms:modified %q must match YYYY-MM-DDThh:mm:ssZ", value)
				}
			}
		}
		for _, item := range pkg.Manifest.Items {
			if hasOPFProperty(item.Properties, "nav") {
				nav++
			}
		}
		if modified != 1 {
			return fmt.Errorf("package dcterms:modified meta element count = %d, want 1", modified)
		}
		if nav != 1 {
			return fmt.Errorf("EPUB 3 manifest nav item count = %d, want 1", nav)
		}
	}
	if len(pkg.Manifest.Items) == 0 {
		return fmt.Errorf("manifest is empty")
	}
	if len(pkg.Spine.Itemrefs) == 0 {
		return fmt.Errorf("spine is empty")
	}
	manifestIDs := make(map[string]struct{}, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Href) == "" || strings.TrimSpace(item.MediaType) == "" {
			return fmt.Errorf("manifest item is missing id, href, or media-type")
		}
		if _, exists := manifestIDs[item.ID]; exists {
			return fmt.Errorf("manifest item id %q appears more than once", item.ID)
		}
		manifestIDs[item.ID] = struct{}{}
	}
	spineIDs := make(map[string]struct{}, len(pkg.Spine.Itemrefs))
	for _, item := range pkg.Spine.Itemrefs {
		if _, ok := manifestIDs[item.IDRef]; !ok {
			return fmt.Errorf("spine itemref %q does not resolve to manifest", item.IDRef)
		}
		if _, exists := spineIDs[item.IDRef]; exists {
			return fmt.Errorf("spine itemref %q appears more than once", item.IDRef)
		}
		spineIDs[item.IDRef] = struct{}{}
	}
	if err := validateMetadataRefines(pkg); err != nil {
		return err
	}
	return nil
}

func validEPUBModifiedTimestamp(value string) bool {
	if len(value) != len("2006-01-02T15:04:05Z") || !strings.HasSuffix(value, "Z") {
		return false
	}
	_, err := time.Parse("2006-01-02T15:04:05Z", value)
	return err == nil
}

func validateEPUBCheck(content []byte) error {
	epubcheckPath, err := exec.LookPath("epubcheck")
	if err != nil {
		return fmt.Errorf("epubcheck is required for EPUB validation: %w", err)
	}
	workDir, err := os.MkdirTemp("", "serial-sync-epubcheck-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	epubPath := filepath.Join(workDir, "book.epub")
	if err := os.WriteFile(epubPath, content, 0o644); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, epubcheckPath, epubPath).CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("epubcheck validation timed out: %w: %s", ctx.Err(), string(output))
	}
	if err != nil {
		return fmt.Errorf("epubcheck validation failed: %w: %s", err, string(output))
	}
	return nil
}

func validateMetadataRefines(pkg opfPackage) error {
	ids := map[string]struct{}{}
	metaPropertiesByID := map[string]string{}
	for _, element := range pkg.Metadata.DCElements {
		addXMLAttrID(ids, element.Attrs)
	}
	for _, identifier := range pkg.Metadata.Identifiers {
		if strings.TrimSpace(identifier.ID) != "" {
			ids[identifier.ID] = struct{}{}
		}
		addXMLAttrID(ids, identifier.Attrs)
	}
	for _, meta := range pkg.Metadata.Meta {
		attrs := opfMetaAttrs(meta)
		addXMLAttrID(ids, attrs)
		if id := xmlAttrID(attrs); id != "" {
			metaPropertiesByID[id] = strings.TrimSpace(meta.Property)
		}
	}
	for _, raw := range pkg.Metadata.Raw {
		addRawElementIDs(ids, raw)
	}
	for _, item := range pkg.Manifest.Items {
		if strings.TrimSpace(item.ID) != "" {
			ids[item.ID] = struct{}{}
		}
	}
	for _, item := range pkg.Spine.Itemrefs {
		addXMLAttrID(ids, []xml.Attr{{Name: xml.Name{Local: "id"}, Value: item.ID}})
	}
	for _, meta := range pkg.Metadata.Meta {
		refines := strings.TrimSpace(meta.Refines)
		if !strings.HasPrefix(refines, "#") {
			continue
		}
		target := strings.TrimPrefix(refines, "#")
		if _, ok := ids[target]; !ok {
			return fmt.Errorf("metadata meta refines missing target %q", refines)
		}
		if strings.TrimSpace(meta.Property) == "collection-type" && metaPropertiesByID[target] != "belongs-to-collection" {
			return fmt.Errorf("metadata collection-type refines %q, want belongs-to-collection target", refines)
		}
	}
	return nil
}

func addRawElementIDs(ids map[string]struct{}, raw opfRawElement) {
	for _, token := range raw.Tokens {
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		addXMLAttrID(ids, start.Attr)
	}
}

func addXMLAttrID(ids map[string]struct{}, attrs []xml.Attr) {
	if id := xmlAttrID(attrs); id != "" {
		ids[id] = struct{}{}
	}
}

func xmlAttrID(attrs []xml.Attr) string {
	for _, attr := range attrs {
		if attr.Name.Local != "id" || strings.TrimSpace(attr.Value) == "" {
			continue
		}
		return attr.Value
	}
	return ""
}

func hasOPFProperty(properties, property string) bool {
	for _, value := range strings.Fields(properties) {
		if value == property {
			return true
		}
	}
	return false
}

func validateXMLDocument(content []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	for {
		_, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %q: %w", file.Name, err)
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read zip entry %q: %w", file.Name, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close zip entry %q: %w", file.Name, closeErr)
	}
	return data, nil
}
