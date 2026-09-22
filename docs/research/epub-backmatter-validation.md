# Chapter EPUB back matter

The September 22, 2026 cleanup keeps standalone publications free of generated
About pages. Embedded author/source metadata remains portable. Only assembled
books/volumes can receive one generated author page. A book label or attachment
role is not evidence of complete-book coverage.

## Observed artifact

Read-only inspection selected the latest published Actus EPUB by its embedded
publication date. It contained one `serial-sync:about` marker, one generated
XHTML resource, one spine reference, and one TOC entry. The page contained an
author biography and links labeled with their raw URLs. The original story
resource did not contain the generated biography.

A local Foliate reader at a 390 by 740 CSS-pixel reading area showed the long URL
running beyond the text area. Headings had normal spacing. Neither duplicate
resources nor the pasted squashed heading text was reproduced in that copy.
The rebuilt chapter ended at the original story text, with no About section in
the reader or TOC. The assembled volume had one final author page with spaced
headings, biography text, and descriptive links that fit the reading area.
This is a desktop browser reader check, not a Readest phone check.

The real EPUB and screenshots remain in private temporary evidence. No paid
chapter text or real-feed fixture is committed here.

## Implementation and regression boundaries

- Text, EPUB attachment, and Calibre PDF publication share embedded metadata
  enrichment. Standalone publications do not request a visible author page.
- Cleanup uses `serial-sync:about` markers, removing every marked resource,
  spine reference, and generated navigation entry. Original author back matter
  and configured post-prefaces survive.
- EPUB 3 preface normalization retains the ownership markers. The first Docker
  upgrade trial exposed their previous removal: counting metadata markers alone
  falsely passed while an unmarked generated page survived in the spine. The
  regression now exercises wrapping through `Materializer.Plan` and checks
  actual resources and reading order.
- EPUB 2/3 tests exercise multiple marked pages, duplicate TOC entries, original
  resource preservation, safe descriptive links, portrait/cover capture, source
  metadata, separate biography/synopsis fields, and repeat decoration. Compact
  OPF serialization avoids accumulating whitespace in retained raw guide XML.
- EPUB output revision 3 and volume assembly revision 5 make the explicit
  rebuild planner notice the change. Preserve output retains its existing
  revision and byte-preserving/wrapping contract. Ordinary sync leaves older
  editions in place pending rebuild. Explicit rebuild also updates an obsolete
  recipe when the resulting EPUB bytes are identical, so future previews can
  converge to unchanged.

## Offline replacement exercise

The disposable Docker exercise uses `/config/config.toml`, `/state`, and
`--network none`. The baseline runs the retained `serial-sync:publish-better`
image. The replacement image uses this checkout's Linux binary in the same
runtime. Inputs comprise a copied Actus EPUB, synthetic text chapters assembled
into a volume, and the repository's valid PDF reader fixture. The copied EPUB
is processed as an attachment with a deliberately configured post-preface.

The exercise checks unchanged ordinary sync; read-only `setup preview --stored`
and rebuild preview; replacement from captured inputs without mounting provider
fixtures; stable destination filenames and original story bytes; no generated
standalone About resource or TOC entry; one final volume author page; and zero
publications with identical published hashes on repeat rebuild. EPUBCheck runs
at materialization, including the Calibre conversion path.

Local evidence directory: `/tmp/serial-sync-backmatter-UmtBSb`.
The `upgrade.py` driver, config, image-build log, individual command logs,
`upgrade-summary.json`, and before/after reader screenshots describe the trial.
Temporary evidence is machine-local and may be removed; the synthetic behavior
regressions live in `internal/artifact/publication_test.go` and
`internal/app/publication_test.go`.

## Completed checks

- `go test ./... -timeout 20m` passed in the Docker test image with EPUBCheck
  and Calibre. The format-upgrade regression also passed with an explicit
  assertion that identical bytes cause zero publications.
- `go run ./cmd/serial-sync --config ./examples/config.demo.toml setup check`
  passed without creating local state.
- The Docker offline preview/rebuild exercise passed. The final image repeated
  the rebuild with zero publications and three skipped destinations.
- Local Foliate inspection covered the real chapter ending and the assembled
  volume author page. Screenshots are retained with the private evidence.
- Skill validation, local documentation links, Go formatting, and
  `git diff --check` passed.

## Existing reader copies

This checkout does not update the deployed image or replace the BookOrbit
library. Readest downloads remain unchanged. Use the scoped offline
[rebuild procedure](../config.md#portable-publication-metadata) after deployment,
then reconcile the library through the configured publisher and redownload
previously downloaded copies. Stable chapter resource paths help retain
locators, but actual BookOrbit file matching and saved Readest progress after
replacement have not been verified by this cleanup.
