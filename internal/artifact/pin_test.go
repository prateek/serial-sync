package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/prateek/serial-sync/internal/domain"
)

// Pin EPUB output bytes before restructuring the package edits.
//
// The EPUB pipeline is three unzip/zip stages today (identity normalize,
// preface wrap, publication metadata); the edit-session refactor must produce
// byte-identical output for unchanged inputs, so every stage is pinned here
// over every fixture builder with a fixed identity and modified time.
// Calibre-produced inputs pin their post-Calibre stages only: Calibre bytes
// vary by version, so the Calibre fixture is opaque input, never an expected
// output. Stages that must fail pin the error string.
//
// Regenerate after an intentional change with:
//
//	go test ./internal/artifact -run TestEPUBPipelineBytesArePinned -update-pins
//
// and show the diff; a byte change with no documented Correction is a bug.

var updatePins = flag.Bool("update-pins", false, "rewrite the pin table from the current pipeline")

var pinIdentityTime = time.Date(2026, 5, 6, 12, 34, 56, 0, time.UTC)

func pinFixtureBuilders() map[string]func(*testing.T) []byte {
	return map[string]func(*testing.T) []byte{
		"EPUB2":                    buildEPUB2Fixture,
		"Calibre3":                 buildCalibreEPUB3Fixture,
		"MultiIdentifier3":         buildMultiIdentifierEPUB3Fixture,
		"PrefaceCollision":         buildPrefaceCollisionFixture,
		"OPFExtraAttribute":        buildOPFExtraAttributeFixture,
		"PercentEncodedHref":       buildPercentEncodedHrefFixture,
		"RefinedModified3":         buildRefinedModifiedEPUB3Fixture,
		"MissingManifestResource":  buildMissingManifestResourceFixture,
		"DuplicateManifestID":      buildDuplicateManifestIDFixture,
		"MissingMetadataRefines":   buildMissingMetadataRefinesFixture,
		"InvalidDCTermsModified":   buildInvalidModifiedEPUB3Fixture,
		"InvalidXHTMLContentModel": buildInvalidXHTMLContentModelFixture,
		"UndeclaredRemoteResource": buildUndeclaredRemoteResourceEPUB3Fixture,
		"DublinCoreMetadata":       buildDublinCoreMetadataFixture,
		"InlineSVG3":               buildInlineSVGEPUB3Fixture,
		"InvalidCollectionType":    buildInvalidCollectionTypeFixture,
		"OPFPreservation":          buildOPFPreservationFixture,
	}
}

type pinStage struct {
	name string
	call func(t *testing.T, content []byte) ([]byte, error)
}

func pinStages() []pinStage {
	return []pinStage{
		{"normalize", func(_ *testing.T, content []byte) ([]byte, error) {
			return normalizeEPUBMetadata(content, "Tide", "Test Author", "urn:uuid:00000000-0000-0000-0000-000000000001", pinIdentityTime)
		}},
		{"preface", func(_ *testing.T, content []byte) ([]byte, error) {
			return wrapEPUBWithPreface(content, "Tide", "Test Author", "urn:uuid:00000000-0000-0000-0000-000000000001", pinIdentityTime, "<p>pinned preface</p>")
		}},
		{"publication", func(_ *testing.T, content []byte) ([]byte, error) {
			return withPublicationMetadata(content, publicationMetadata{
				Title: "Tide — Chapter 1", Author: "Test Author", Series: "Tide", SeriesIndex: "1", PublishedAt: pinIdentityTime,
			})
		}},
		{"publication-preserve", func(_ *testing.T, content []byte) ([]byte, error) {
			return withPublicationMetadata(content, publicationMetadata{
				Title: "Tide — Chapter 1", Author: "Test Author", Series: "Tide", SeriesIndex: "1", PublishedAt: pinIdentityTime,
				PreserveEmbedded: true, Publication: &domain.PublicationMetadata{Description: "pinned description", Language: "en"},
			})
		}},
	}
}

func pinOutputSHA(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func collectPins(t *testing.T) map[string]string {
	t.Helper()
	pins := map[string]string{}
	builders := pinFixtureBuilders()
	names := make([]string, 0, len(builders))
	for name := range builders {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fixture := builders[name](t)
		for _, stage := range pinStages() {
			key := name + "/" + stage.name
			out, err := stage.call(t, fixture)
			if err != nil {
				pins[key] = "error:" + err.Error()
				continue
			}
			pins[key] = pinOutputSHA(out)
		}
	}
	return pins
}

func TestEPUBPipelineBytesArePinned(t *testing.T) {
	pins := pinnedTable
	got := collectPins(t)
	if *updatePins {
		var lines []string
		keys := make([]string, 0, len(got))
		for key := range got {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, fmt.Sprintf("\t%q: %q,", key, got[key]))
		}
		body := "var pinnedTable = map[string]string{\n" + joinLines(lines) + "\n}\n"
		if err := os.WriteFile("pin_table_test.go", []byte("package artifact\n\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("wrote pin_table_test.go")
		return
	}
	if len(got) != len(pins) {
		t.Fatalf("pin count %d, table %d", len(got), len(pins))
	}
	for key, want := range pins {
		if got[key] != want {
			t.Fatalf("%s bytes drifted: got %q want %q", key, got[key], want)
		}
	}
	for key := range got {
		if _, pinned := pins[key]; !pinned {
			t.Fatalf("no pin registered for %s", key)
		}
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}
