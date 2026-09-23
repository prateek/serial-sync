package artifact

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"path"
)

// epubPackage is one opened reading copy: every archive entry, the parsed package
// document, and the directory its hrefs resolve against. Edits in this package mutate
// an open session; openEPUBPackage and (*epubPackage).write are the only code here that
// touches a zip.
type epubPackage struct {
	Files       map[string][]byte // every archive entry by name, exactly as the zip held it
	Package     opfPackage        // the parsed package document
	PackagePath string            // its archive path, e.g. "OEBPS/content.opf"
	Dir         string            // the directory hrefs resolve against; "" at the archive root
}

// openEPUBPackage unzips content and parses its package document. It does not repair or
// default anything: opening is not normalizing, and the stages that normalize say so.
func openEPUBPackage(content []byte) (*epubPackage, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, err
	}

	files := map[string][]byte{}
	for _, file := range reader.File {
		data, err := readZipFile(file)
		if err != nil {
			return nil, err
		}
		files[file.Name] = data
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
	packagePath := container.RootFile.RootFile[0].FullPath
	packageData, ok := files[packagePath]
	if !ok {
		return nil, fmt.Errorf("epub missing package document %q", packagePath)
	}
	var pkg opfPackage
	if err := xml.Unmarshal(packageData, &pkg); err != nil {
		return nil, err
	}

	dir := path.Dir(packagePath)
	if dir == "." {
		dir = ""
	}
	return &epubPackage{Files: files, Package: pkg, PackagePath: packagePath, Dir: dir}, nil
}

// entry resolves a manifest href to an archive entry name.
func (s *epubPackage) entry(href string) (string, error) {
	return resolveManifestHref(s.Dir, href)
}

// write encodes the package document back into the archive and rezips it, running the
// structural validation every EPUB serial-sync writes must pass.
func (s *epubPackage) write(encoding packageEncoding) ([]byte, error) {
	var data []byte
	switch encoding {
	case packageIndented:
		data = mustXML(s.Package)
	case packageCompact:
		encoded, err := xml.Marshal(s.Package)
		if err != nil {
			return nil, err
		}
		data = append([]byte(xml.Header), encoded...)
	default:
		return nil, fmt.Errorf("unknown package encoding %d", encoding)
	}
	s.Files[s.PackagePath] = data
	return writeStructurallyValidatedEPUBArchive(s.Files)
}

// packageEncoding keeps the two historical package-document encodings apart. The
// normalize, preface and volume-assembly stages have always written indented XML; the
// publication stage has always written compact XML, and it is the stage that runs last,
// so it is the encoding every stored reading copy's hash actually depends on. Unifying
// them is a Correction that re-delivers the whole catalog, not a cleanup.
type packageEncoding int

const (
	packageIndented packageEncoding = iota // xml.MarshalIndent(value, "", "  "), xml.Header prefix
	packageCompact                         // xml.Marshal(value), xml.Header prefix
)
