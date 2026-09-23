# Spec: Reader-ready library output

Status: implemented on `prateek/session-history-research`, based on `7728815`. Physical-reader rollout validation remains pending; no production library was migrated. Written and revised 2026-09-19. The user selected author-book grouping with a configurable 50-chapter fallback, and keeping incomplete groups as singles until gaps are filled or explicitly marked intentional.

## Problem Statement

I sync a Patreon creator's serials with serial-sync and get a published folder of EPUBs that all pass EPUBCheck. When I load them into Calibre or onto an e-reader, I can't read from them.

- Every chapter of a series has the same book title. A series with 283 chapters shows up as 283 books all called "Return of the Runebound Professor". The chapter number and chapter title appear nowhere in the metadata.
- Nothing tells the library that these books belong to one series or what order they go in. There is no series field and no position.
- One file per chapter means reading a 1,000-chapter serial is opening 1,000 files.
- Order breaks when the creator mistypes a title. A post titled "Chaper 790" gets a date-based filename and sorts ahead of every numbered chapter.
- Filenames carry a five-digit chapter number and an always-present release-id suffix (`...-ch00858-r146516947.epub`). I asked for short, sortable names with no suffix.
- When output behavior changes, already-synced chapters keep their old form. Live runs only look at recent posts, so the only way to rebuild everything today is to edit the state database by hand.

## Solution

Published output becomes a library I can read from.

- Each generated or transformed chapter EPUB is titled with its own chapter title, names its series, and carries its known position in that series, in the metadata fields Calibre and EPUB 3 readers use. Pass-through originals retain their bytes.
- A series can opt into volumes with a chapter table of contents. Use the author's books when their boundaries are known; otherwise use configurable 50-chapter ranges. An unexplained missing chapter keeps the group as singles. Completed volumes stay byte-identical during normal sync; an explicit offline rebuild applies corrections.
- A publisher retires covered singles only after successfully delivering their replacement volume. Captured inputs and previous artifacts remain in the state archive.
- Chapter detection accepts supported abbreviations and observed misspellings, so a title such as "Chaper 790" still sorts correctly.
- Filenames are short and sortable: series, optional book, chapter number padded to four digits. A stable identity suffix appears only when names would otherwise collide at the destination.
- One command rebuilds all published output from what serial-sync already has stored, without contacting the provider.

## User Stories

1. As a reader, I want each chapter EPUB to have its own title, so that I can tell chapters apart in my library.
2. As a reader, I want each chapter EPUB to name its series, so that my library groups a serial's chapters together.
3. As a reader, I want each chapter EPUB to carry its position in the series, so that my library sorts chapters in reading order.
4. As a reader, I want the chapter's original publish date in the metadata, so that chapters without a number still sort sensibly.
5. As a reader, I want the author recorded consistently across every chapter and volume of a series, so that author views in my library aren't fragmented.
6. As a Calibre user, I want the series and position written in the form Calibre imports, so that I don't have to edit metadata by hand after adding books.
7. As a user of a standards-based EPUB 3 reader, I want the series and position written in the standard EPUB 3 collection form, so that it works outside Calibre too.
8. As a reader, I want to bundle a series into volumes, so that I open one book for fifty chapters instead of fifty books.
9. As a reader, I want a volume to have a table of contents listing each chapter, so that I can jump to where I stopped.
10. As a reader, I want a volume's title to say which chapters it covers, so that I can pick the right volume.
11. As a reader, I want volumes numbered and positioned within the series, so that they sort in order.
12. As a reader, I want each chapter inside a volume to keep its author's note preface, so that bundling loses nothing I get from single chapters.
13. As a reader, I want a completed volume to stay byte-identical during normal sync, with corrections applied only by an explicit rebuild, so that routine runs don't replace the book I am reading.
14. As a reader keeping up with a live serial, I want chapters that don't fill a volume yet published as single files, so that I can read the newest chapter the day it arrives.
15. As a reader, I want those single files removed from each published folder after their replacement volume is successfully delivered there, so that failed delivery cannot leave me without a readable copy.
16. As a reader of a serial the creator organizes into numbered books, I want volumes to follow the creator's book boundaries, so that my volumes match the published books.
17. As a reader of a serial with no known book grouping, I want configurable fixed chapter ranges, defaulting to 50, so that bundling still works.
18. As an operator, I want to choose the fallback chapter count per volume for each series, so that short and long chapters both make reasonably sized books when there are no author-book boundaries.
19. As an operator, I want bundling to be off unless I turn it on for a series, so that existing configs keep producing single chapters.
20. As an operator, I want bundling configured at the series output level, so that it lives with the other output settings.
21. As a reader, I want a chapter whose title misspells "chapter" to still get its number, so that it sorts with its neighbors.
22. As a reader, I want chapters titled with a number word ("Chapter Twelve") to keep working, so that the fix for typos doesn't break what works.
23. As an operator, I want a post with no detectable number to still publish with a date-based name, so that nothing is dropped.
24. As an operator, I want short filenames with a four-digit chapter number, so that names are readable and still sort past chapter 999.
25. As an operator, I want a stable release-identity suffix only for colliding names, with the same result regardless of discovery order, so that files never overwrite each other.
26. As an operator, I want a file that gets a new name to replace the old one in the published folder, so that renames don't leave orphans.
27. As an operator, I want the exec publisher to be told when a previously published file is superseded, so that my hook can remove it downstream.
28. As an operator, I want a rebuild command that re-plans every stored release without contacting the provider, so that an output change reaches my whole library without risking rate limits.
29. As an operator, I want the rebuild to skip anything whose output is unchanged, so that a second rebuild is fast and publishes nothing.
30. As an operator, I want a dry-run rebuild, so that I can see how many files would change before anything is written.
31. As an operator, I want the rebuild to report missing stored inputs while retaining the previous output, so that I can recover those inputs separately.
32. As an operator, I want volumes to pass EPUBCheck before they are stored, so that bundling keeps the validity guarantee chapters already have.
33. As an operator, I want pass-through attachments in `preserve` mode left byte-identical, so that this change doesn't touch files serial-sync doesn't generate.
34. As an operator, I want `setup preview` to show the detected chapter number and the volume each post would land in, so that I can check ordering while authoring series rules offline.
35. As an operator, I want an incremental run with no new inputs after a rebuild to report nothing changed, so that I know the output is stable.
36. As a contributor, I want the config reference, the series authoring guide, the README and the rule-authoring skill updated in the same change, so that guidance matches behavior.
37. As a reader, I want an incomplete book or chapter range to remain as singles with an explanation of its gaps, so that a partial archive is never presented as a finished volume.
38. As an operator, I want to mark an author-intended gap explicitly in config and see it in preview, so that intentional numbering skips do not block completion forever.
39. As an operator, I want a failed or interrupted replacement to resume safely per target, so that retries neither lose chapters nor repeatedly deliver unchanged files.

## Implementation Decisions

### Chapter metadata and reading order

- The artifact module's EPUB builder takes a per-book metadata value (title, author, series title, position, publish date) instead of just a title and author. The same value feeds generated chapters, wrapped EPUB attachments, Calibre-converted PDFs and volumes.
- A chapter's book title is the release title, trimmed. The series title is the series config title.
- EPUB 3 collection metadata carries series membership and reading position. Also write Calibre's `calibre:series` and `calibre:series_index` where EPUBCheck accepts them. A standards-only fallback must still pass a real Calibre import test; silently dropping usable series ordering is not an acceptable fallback. Resolve the exact encoding in the metadata slice before implementing volumes.
- Keep series, book identity/order, and chapter number as separate values. For globally numbered chapters, the series position is the chapter number. For numbering that restarts per book, flatten the order using each book's expected chapter span: Book 1 chapters 1–80 occupy positions 1–80; Book 2 chapter 1 occupies position 81. An explicit per-book starting series position supports a partial archive or nonstandard numbering. Intentional gaps reserve their positions.
- Derive these positions from declared ranges, never the number of chapters currently captured. If earlier book ranges or the starting position are unknown, preview reports the missing mapping. Do not give two books' chapter 1 the same position. Volume publication requires an unambiguous series position.
- The reader-facing series index written to `calibre:series_index` and `group-position` is separate from that scalar position (decided 2026-09-23 after BookOrbit sorted unindexed chapters last). It follows the author's numbering: the chapter number, or `book.chapter` when numbering restarts per book. A release without its own number repeats the index of the release before it, and the reader orders the tie by publish date. BookOrbit accepts only `^\d+(\.\d+)?$`, so letter suffixes are not possible. See [config](../config.md#series-index).
- A volume uses its first covered chapter's series index, so it sorts alongside open singles. Its title identifies the author book or the fixed volume number and chapter range; the table of contents uses original chapter titles.
- Chapter identifiers retain their existing provider/source/release identity. A volume has a separate stable identifier based on its series and grouping identity: the stable book ID for an author book, or the inclusive chapter range for a fixed volume. Correcting the same group retains its identifier and records a new content hash; regrouping into different ranges creates new identities and retires the old output safely.

### Sequence detection and book mapping

- Accept bounded chapter markers: `chapter`, `chap`, `ch`, optional terminal periods, and the observed typo `chaper`, case-insensitively, followed by a number or number word. Preserve existing supported numeric forms and book detection. `Character 3` and `Challenge 2` are not chapter-number matches.
- Detection has one result type shared by filenames, metadata, volume assignment and `setup preview`. Report the matched text and whether a value came from detection or an override. Number detection never changes a release's classification to `chapter` by itself.
- Add optional book identity to series-input mapping, with the winning input's explicit value taking precedence over title detection. Book definitions belong to the series and provide stable identity, reading order, optional title, and expected first/last chapter numbers. Match detected book numbers to these definitions. Unknown endpoints remain unknown; seeing Book 2 does not prove Book 1 ended at its highest captured chapter.
- Allow explicit per-release sequence overrides keyed by source and release identity for ambiguous titles, unnumbered interludes, and duplicate chapter numbers. An override can choose the chapter number/book, select which duplicate fills a chapter slot, or leave a release as a single. Conflicting book definitions and overlapping assignments fail validation before publication.

### Filenames

- Chapter files: series slug, optional `bkNN`, then `chNNNN`. Author-book volumes use `series-bkNN`; fixed-range volumes use `series-volNN`. Undetected releases keep the date-and-title form. Four chapter digits are the chosen filename convention. Padding is a minimum and never truncates larger numbers; sort using the parsed sequence.
- Compute collision groups over the full retained catalog for the destination, not just releases discovered in this run. For a unique name, omit the suffix. For a collision, every member gets a suffix derived from the provider, stable source identity, and provider-native release ID, not a newly generated catalog-row UUID. Do not pick an unsuffixed winner based on arrival order. The suffix encoding must be filename-safe and distinguish all members; a shortened digest must be checked for collisions.
- If a second release arrives later, rename the previously unsuffixed file through the replacement procedure below. Incremental discovery, reversed discovery, and a fresh offline rebuild of the same catalog must converge on identical paths. Include destination normalization and names from different series that share a folder in collision checks. Never overwrite an unrelated file. If resolving a collision would rename a frozen volume or a legacy artifact awaiting migration, report a blocked collision until explicit rebuild.

### Volume grouping and completion

- Bundling is a series output setting with values `none` (default) and `volume`, plus a positive chapters-per-volume count (default 50). It is separate from output format; the only formats remain `preserve` and `epub`. Volume bundling requires `format = "epub"`; reject bundling with `preserve` rather than silently altering originals.
- An author book is one volume regardless of its length. Use the configured or detected book identity and the declared expected range. A known book with an unknown endpoint stays open as singles until the boundary is supplied; do not silently split it into fixed ranges.
- Where there is no book grouping, use chapter-number ranges 1–50, 51–100, and so on, or the configured size. A range can complete as soon as all its expected chapters are available. Chapter 51 is neither required to close 1–50 nor sufficient to close a range containing a gap. A short final range needs an explicit final endpoint to complete. Chapters beyond that endpoint remain singles.
- Explicit author-book mappings take precedence. Fixed fallback ranges must not mix mapped book chapters with unassigned chapters, span a known book boundary, or treat chapters that restart at 1 as globally numbered. Ambiguous mixed captures stay as singles with a mapping explanation in preview. New boundaries that affect an already completed volume require an explicit rebuild.
- Only releases classified as `chapter` with an unambiguous sequence and readable stored inputs can fill chapter slots. Unnumbered interludes and ambiguous duplicates remain singles with a reason. Two releases at the same book/chapter number block that group's completion until a mapping chooses the slot's release; the other remains a single and is not deleted.
- Completion requires a known inclusive expected range, unambiguous ordering, and every expected slot present and materializable, except gaps explicitly marked intentional in series config. A late-starting capture does not lower the expected start. Never infer intentional gaps from an upstream error, entitlement, or the presence of a later chapter.
- Intentional gaps identify chapter numbers within a book or fixed range and include a reason. Show them in preview and volume front matter. If a chapter later fills such a gap, it becomes an available slot; any existing completed volume follows the late-chapter policy below.
- `setup preview` shows grouping identity, expected range, present chapters, missing slots, intentional gaps, and why a group remains open. Dry-run also shows the proposed volume path and the exact singles it would replace. Preview works from the dump without live discovery.
- An open group or pending correction is an informational/review-needed outcome when all currently required singles or frozen volumes were delivered. It does not make every ongoing serial run fail. A requested replacement that cannot finish, an ownership conflict, or a delivery failure produces the incomplete/nonzero outcome described below.

### Completed volumes and corrections

- Completing a volume freezes an edition: the ordered member identities and input hashes, its grouping/sequence mapping, output settings, and validated artifact bytes. Normal sync may capture corrections, but must not rebuild, rename, or alter that completed volume. Detect differences and report that an explicit rebuild is available.
- A correction to an existing member is retained in the archive and reported as pending; do not publish a duplicate corrected single for that member. A newly discovered chapter absent from the frozen membership, such as a chapter that fills an intentional gap, is published as a single and reported as awaiting a rebuild. A failed or unchanged sync cannot remove the completed artifact.
- Explicit rebuild reevaluates the selected catalog using current mappings and stored inputs. It can replace a completed volume with corrected bytes or regroup it. The dry-run names affected volumes and warns that replacing a delivered book can affect reading position. Retain the previous artifact in the archive; replace its published copy only through the transition below.
- Changing volume size, mappings, or disabling bundling does not silently dismantle completed volumes on a normal run. Report the required rebuild and continue unrelated groups; do not also bundle frozen members into overlapping new volumes. New chapters in the affected scope remain singles. If revised mappings no longer form a complete volume, explicit rebuild publishes the affected chapters as singles before retiring the old volume. If required inputs are missing, retain the old published output and report the blocked transition.
- Store a volume as an artifact with a grouping identity and ordered membership, rather than a fake source release. Snapshot member hashes so later catalog changes cannot change the meaning of the stored edition. Keep compact membership and transition state in SQLite using migrations and SQLC queries; detailed plans/events and artifact bytes remain on disk.

### Publish replacements before retiring old files

Replacement applies to completed volumes, corrected editions, and renamed chapters. It is tracked separately for each publisher target; success on one target never authorizes deletion on another.

1. Plan the complete replacement set and persist enough state to resume it. Validate every new generated EPUB before it can supersede anything. A pending transition keeps using the same artifact bytes and identifiers on retry.
2. For a filesystem target, stage the replacement on that filesystem, verify its content hash, and atomically install each file. Confirm the installed files match the plan before recording delivery success. A previous database success row alone is insufficient if the file is absent or has different bytes.
3. Once all replacement files for that transition are delivered, retire only covered paths recorded as owned by that target and still matching their previously published hashes. Never delete paths belonging to the final desired set, user-modified files, unrelated files, captured inputs, or archived artifacts. When replacing at the same owned path, verify ownership before atomic replacement and retain the previous artifact in state.
4. Persist unfinished cleanup and resume it on retry. A crash after installation may temporarily leave both a volume and its singles; it must not leave neither. Missing old paths count as already retired. An ownership conflict leaves the file intact and is reported for resolution.
5. Delivery or cleanup failures produce nonzero exit and truthful failed/partial run status with the affected target and pending work. Keep successes on other targets. Fix the current publisher's success-on-error behavior as a prerequisite to this lifecycle.

For exec targets, retain the existing release-oriented payload for existing hooks. Add an explicitly configured, versioned contract for hooks that handle volumes and retirement: a publish event names the artifact/group identity and members; a supersede event names the old target receipt/path and its replacement. Exit zero acknowledges an event. Send supersede only after all replacement publish events succeed for that target. Give retries the same event ID; delivery is at least once, so participating hooks must deduplicate and handle an already-retired item. A legacy hook may continue receiving ordinary chapters, but selecting volume output or a required retirement for that hook must fail validation with an upgrade instruction, before any replacement or deletion. Do not send an unexpected action to an existing hook.

### Offline rebuild and rollout

- `run` gains an explicit rebuild mode, without a new top-level command. It replans stored releases from normalized payloads and captured attachments, then publishes. It makes no provider or bootstrap calls and does not advance a sync cursor. Source and target filters apply; include a series filter so correcting one volume does not require replacing every series.
- An unchanged rebuild does not regenerate timestamps or replace identical artifacts. Reuse stored validated bytes when input hashes and output settings match. The next ordinary incremental run also leaves the rebuilt output unchanged.
- Missing inputs are reported by release/path, never fetched. Block the affected replacement as a unit, leave its old output intact, and continue independent work with an incomplete/nonzero result. Rebuild is not authorization to prune an entire destination directory or delete outputs excluded by a narrower filter.
- Rebuild dry-run is offline and read-only: no catalog/run-record changes, artifact writes, directory creation, hook calls, or publication. Emit the plan to stdout. Show selected scope, additions, replacements, retirements, unchanged items, and blocked transitions; fail early on invalid targets/config. This requires a read-only initialization path rather than the current mutating service bootstrap.
- A source-filtered rebuild may use only that source's inputs. If a volume or required replacement also depends on an excluded source, report it as blocked and retain the old files. Never silently widen the requested scope.
- Rebuild and its preview exclude disabled sources. Existing output for those sources stays intact; a shared volume requiring disabled inputs is blocked.
- Existing libraries are deployed. First preview the migration, validate a small Calibre/reader sample, then explicitly rebuild the chosen scope. The first normal run after upgrading must not mass-rename old output; legacy artifacts remain until rebuild. Preserve prior artifact bytes and mapping records needed to recover a replaced edition.
- Handle an explicitly present `anthology_mode` in both series-input and legacy-rule config: `false` is accepted with a deprecation diagnostic; `true` fails validation with instructions to opt into series-level bundling. An absent flag produces no warning. Never silently reinterpret or ignore a previous `true` value. New examples omit the flag.

### Docs

- Implementation must update `README.md`, `docs/config.md`, `docs/rules.md`, `docs/first-source.md`, `docs/hooks.md`, and `skills/serial-sync-rule-authoring/SKILL.md` together. Document exact config syntax and container commands, including gap declarations and per-book position overrides.

## Testing Decisions

A good test here runs the service the way an operator does and checks what lands in the published folder: file names, which files exist, and the metadata and table of contents read back out of the EPUBs. It does not assert on internal structs, call order or log lines.

Use the fixture-backed service lifecycle with real temporary storage and filesystem targets. Exercise hook compatibility through the existing exec-publish seam, and CLI exit/read-only behavior through disposable command runs. Fast sequence and naming cases can stay in the existing supporting tests; do not require a Calibre subprocess for every parser example.

| Scenario | Required result |
| --- | --- |
| Numbered chapter, `Chaper 790`, `Chapter Twelve`, and an unnumbered post | Correct title/author/date and series metadata; detected numbers agree across preview, filenames and EPUBs; unnumbered output remains available. `Character 3` and `Challenge 2` do not acquire chapter numbers. |
| Complete fixed range with size 3 | Chapters 1–3 produce Volume 1 without waiting for chapter 4. Chapter 4 remains a single. TOC order and author notes survive bundling. |
| Missing chapter 2, with chapters 1, 3 and 4 present | No Volume 1; all available chapters remain singles and preview names the gap. Capturing 2 completes the volume and retires only 1–3 after delivery. |
| Intentional gap at chapter 2 | Explicit config allows completion and discloses the gap. Later arrival of 2 produces a single and a pending-rebuild report; normal sync preserves the volume hash. Explicit rebuild includes 2 and safely retires that single. |
| Author books with different lengths and chapter numbering restarting at 1 | One volume per complete book, regardless of the fallback size; declared spans give correct series positions. An unknown endpoint, missing earlier span, or late-starting capture stays open with an explanation. Seeing a later book alone never closes an earlier one. |
| Interlude, duplicate number, mixed book mapping, or a short final range | Singles remain available until explicit mapping resolves the ambiguity or supplies the endpoint. No arbitrary duplicate is chosen and no omitted single is retired. |
| Changed content or author note in a completed member | Normal sync captures the correction but leaves volume bytes/path and membership unchanged; explicit rebuild replaces that edition and retains the old artifact in state. |
| Changed volume size/book mapping, or bundling disabled | Normal sync retains frozen volumes and reports rebuild required without creating overlapping volumes. Rebuild installs the complete replacement set, including singles when needed, before retirement. |
| Collisions discovered together, in reverse order, and incrementally | Identical final names; all colliding releases get stable suffixes. Old unsuffixed paths disappear only after replacements succeed. Unrelated destination files remain untouched. |
| Materialization failure or missing stored input | Affected replacement is blocked; old files remain readable and independent groups can finish. No provider fetch occurs during rebuild; result identifies incomplete work. |
| Two targets, with one failing delivery or cleanup | The failed target keeps its necessary old copies, returns nonzero and records failed/partial status. The successful target completes independently. Retry finishes only pending work. |
| Interruption after installation but before recording success, or during retirement | Restart resumes from durable state, verifies installed bytes, and completes cleanup without data loss or repeated acknowledged delivery. An absent replacement file is repaired rather than skipped because of an old success row. |
| User-modified owned path or an unrelated file at the destination | No overwrite/deletion; conflict is reported. A same-path corrected edition replaces only the known owned bytes and keeps the previous artifact in state. |
| Legacy hook and opted-in versioned hook | Legacy chapter payload stays compatible; unsupported retirement/volume work fails before delivery. Versioned events acknowledge replacements before superseding, with stable IDs across retries and safe duplicate handling. |
| Correct a chapter from A to B, then restore A through a deduplicating versioned hook | Restoring retired content gets a new delivery event ID; all revisions leave the requested chapter readable. An upgrade retains IDs of already-pending events. |
| Swap book membership so replacement paths form a cycle | Reject the graph before freezing new work. A rebuild under a distinct title can recover, including from an undelivered cyclic plan saved by an older version. |
| Final endpoint 2 with chapters 1–3 present; open book overlapping a later explicit position | Bundle only chapters 1–2 and retain chapter 3 as a single. Reject conflicting book starts; omit ambiguous chapter positions where an open book reaches a later book's range. |
| EPUB attachment with URI-escaped resource names | Preserve chapter content and valid navigation links when assembling the volume. |
| Rebuild with networking disabled; repeat rebuild and then ordinary unchanged sync | Correct output from captured inputs, unchanged cursor, no duplicate publication or byte changes on repetition. A narrowed source/target/series filter cannot delete excluded output or silently widen scope. |
| Rebuild dry-run with state and library mounted read-only | Complete destination/replacement plan, zero writes and no hook/provider calls. Unknown target or invalid mapping fails before planning work begins. |
| Existing deployed library and old `anthology_mode` config | Upgrade alone leaves legacy output intact; explicit rebuild performs migration. `false` produces a deprecation diagnostic; `true` is rejected with migration guidance. |
| Pass-through `preserve` output | Identical original bytes. Combining `preserve` with bundling is rejected. |

Every newly generated or transformed EPUB must pass EPUBCheck before publication, including replacement volumes. Untouched pass-through originals remain exempt from transformation and retain their bytes. Use the intended Docker image for EPUB/Calibre end-to-end checks; keep all fixtures and state disposable, with networking disabled for offline assertions.

Implementation validation includes `go test ./...`, the demo `setup check`, and fixture-backed `setup preview`/`run` per `AGENTS.md`. The metadata slice also needs an actual small Calibre import and a reading sample on the chosen reader before a full-library rebuild. Inspect displayed order and creator formatting/author notes; EPUBCheck alone cannot prove these. For volumes, demonstrate a correction and book transition on that workflow and record the observed reading-position effect of explicit replacement. Delivery automation remains a separate workstream.

## Out of Scope

- A Calibre library publisher (calling `calibredb` or writing into a Calibre library). Output stays a folder; the exec publisher remains the hook for that.
- Device delivery of any kind (Calibre content server, e-reader sync, send-to-device).
- A run wrapper, scheduling, or notifications.
- A report of unmatched posts that look like chapters.
- Cover images.
- Changing the default published path.
- General log retention and retrofitting isolation across existing tests; new tests for this work must use disposable state.
- The failing Calibre conversion test on the Ubuntu CI runner.
- The `setup dump --force` ordering bug.

## Implementation validation

- The full `go test ./...` suite passed in the container with networking disabled, including real EPUBCheck and Calibre conversion. Review fixes were verified through focused app, CLI and hook regressions. The demo `setup check`, build and fixture-backed container `run` passed.
- Tests exercise the three user-approved seams: app workflows and actual output files; CLI validation, failures and read-only preview; exec JSON/events and retries. Filename cases were moved from private-helper assertions to the app workflow.
- Actual Calibre import of generated chapter samples confirmed distinct titles, author, series and positions 17 and 42. EPUBCheck 5.3.0 accepts the emitted metadata. PDF conversion, bundled author notes, corrected editions, retained archives and unchanged repeats have container regressions.
- A final CLI sample ran with networking disabled: read-only container/state mounts produced the exact one-volume/two-retirement preview with zero filesystem changes; writable rebuild delivered the volume and retired the singles; repeat rebuild published nothing. Calibre imported the resulting volume with its range title, Actus author, correct series and position 1.
- Migration fixtures came from the unmodified `serial-sync:7728815` image. They cover ordinary-run preservation of both legacy HTML and EPUB output, read-only migration planning, interrupted delivery recovery, and explicit volume migration.
- Standards and spec reviews ran independently. Their findings were fixed and rechecked. The selected-source tests include both separate series and independent volumes within one shared series.
- Adversarial-review regressions cover restored hook deliveries, cyclic-regroup recovery, final chapter endpoints, overlapping book positions, escaped EPUB resource names, and disabled-source preview/apply parity. Mapped chapters that must remain singles still prevent unassigned chapters from forming fallback volumes.
- Upgrade checks used the exact previous implementation at `642bbaf` to create pending work, then resumed it with the fixes in network-disabled containers. Pending hook retries retained their event IDs; an undelivered cyclic plan recovered under a distinct series title. All state and published files were disposable fixtures.
- The Xteink/physical-reader sample and its reading-position behavior remain unverified. Complete that small rollout check before a full-library migration. Device delivery and Calibre library publishing remain out of scope.
- A cyclic regroup that would overwrite mutually dependent existing paths is blocked. Rebuild under a distinct series title first, then restore the preferred title in a later rebuild; the walkthrough documents this recovery.

## Further Notes

- Unverified: how the target e-reader (Xteink X4) takes books and which metadata it reads. The spec assumes a folder of EPUBs imported into Calibre, or copied to the device, is the delivery path. Check this with one real series before building volumes, since it decides whether volumes or well-tagged singles matter more.
- Captured attachment rebuilding was verified for the fixture PDF workflow after removing the provider cache. Missing or changed captured inputs are reported and block the related replacement; they are never fetched during rebuild.
- An explicit migration rebuild changes generated chapter metadata and many filenames. Untouched `preserve` originals retain their bytes. The earlier validation run suggests roughly 35–40 minutes per thousand EPUBs when EPUBCheck starts a JVM per file; this is an estimate, not a rebuild benchmark. Dry-run must show the affected scope before that work.
- The [domain glossary](../../CONTEXT.md) defines the reading terms. The later [reader experience plan](reader-experience.md) covers portable metadata and ongoing navigation; its [ownership decision](../adr/0001-portable-publication-metadata.md) does not change this spec's implemented output behavior.
