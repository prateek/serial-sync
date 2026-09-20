# Deployed library fixture

`library-7728815.tar.gz` was captured by running the unmodified `serial-sync:7728815`
image against `testdata/fixtures/patreon/actus`, with networking disabled. The
`nightmare` series uses text posts and preserve output. It contains chapters 17
and 42 with the old five-digit, release-suffixed filenames and metadata.

`epub-7728815.tar.gz` was captured with the same original image, fixtures and
network isolation, using EPUB output. It verifies that enabling volume bundling
after an upgrade leaves legacy singles intact until explicit rebuild.

The catalog is a SQLite SQL dump, not assertions about the current schema. Absolute
state paths were replaced with `@ROOT@` so a CLI migration test can restore the
deployed state into a disposable directory. Logs and provider session files are
excluded. All post content comes from the repository's synthetic fixtures.
