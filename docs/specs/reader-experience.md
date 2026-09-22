# Reader experience plan

Product decisions accepted on 2026-09-21. Portable metadata, explicit metadata rebuilds, controlled import, grouped notifications, and hourly operation are implemented. The initial deployment tested a narrow reader patch; the subsequent user decision selects **stock BookOrbit 3.0.0**, a full historical metadata rebuild, and removal of Grimmory. The patch remains an optional [Serial Reader experiment](../../integrations/bookorbit/reader/README.md). Its Previous/Next controls and correction safeguards are not features of stock BookOrbit. The [validation record](../research/reader-experience-validation.md) distinguishes the original trials from the follow-up rollout.

The outcome is a mobile reading library with useful descriptions and artwork, automatic chapter delivery, and convenient movement through a series. Published EPUBs should carry the information needed to read and organize them independently of the library server.

The [glossary](../../CONTEXT.md) defines the terms. The [ownership decision](../adr/0001-portable-publication-metadata.md) records why curation belongs to serial-sync. The [evidence record](../research/reader-experience-evidence.md) separates recovered history, current code, source inspection, and untested behavior. The [existing reader-output spec](reader-ready-library-output.md) remains the implemented baseline; this plan does not silently change its frozen-volume policy.

The accepted product contract is:

| Area | Decision |
| --- | --- |
| Reading surface | A usable mobile website plus OPDS or equivalent acquisition is sufficient. No particular native client is required. |
| Navigation | Ideally, Previous/Next moves through the series inside the reading view, including newly arrived chapters. This is a preference to pursue and test, not a claim that either stock reader already supports it. |
| Reading position | Ordinary arrivals preserve the exact location in an existing chapter in the mobile web reader. A rare correction to the current chapter may return to its beginning with a notice. |
| External apps | Provide self-contained downloadable EPUBs. Automatic progress synchronization between arbitrary reading apps is outside the first version. |
| Metadata ownership | Serial-sync retains curated author profiles, artwork, descriptions, and overrides. The reader owns reading progress. Supplementary library enrichment is reproducible from retained choices. |
| EPUB contents | Embed publication metadata, suitable cover art, and a usable table of contents. Add one short About page at the end with available author biography/portrait and source links. Missing enrichment does not delay chapters. |
| Existing EPUB inputs | Publish enriched copies while preserving captured originals and the author's story text/layout. Apply this through EPUB output; retain the documented `preserve` mode contract. |
| Curation | Use configuration edits with the existing offline dump/preview workflow. Explicit overrides win; preserve suitable embedded metadata and fill gaps from clearly matched creator sources. Ambiguous public-web matches need review. |
| Sources | Patreon profiles and captured material are starting points; author websites and other public sources are eligible. Portraits, campaign banners, and series/book covers retain their distinct meanings. |
| Refresh | New publications use current curated choices. Changes to already-published copies require explicit preview/rebuild. A profile or artwork refresh alone must not replace a reading copy. |
| Operations | Poll hourly through the existing request budget and cooldown path. New chapters appear automatically; import failures remain retryable. |
| Notifications | Prefer one phone notification per sync for newly readable chapters, grouped by series with a reading link. Quiet on unchanged runs; actionable alerts for failures requiring intervention. Trial exe.dev first, with a free, simple alternative if necessary. |
| Reader customization | Test stock readers first. A small maintained patch for navigation is acceptable if needed. A custom reader is outside the first version. |

The following stages preserve the accepted implementation and validation requirements. The trial selected stable copies: growing EPUB replacement exposed stale navigation in the stock reader. The runtime baseline was the actually deployed Grimmory 3.4.1, rather than the initially proposed 3.5.0 comparison.

1. Prove reading behavior and choose the reader/output approach.

   Pin BookOrbit v3.0.0 and Grimmory v3.5.0 for the initial comparison, or explicitly record any changed versions. Run containerized instances with disposable state and representative captured content. Start with BookOrbit and use Grimmory as the baseline. Inspect the actual mobile viewport and save results for each scenario.

   Compare the current singles/completed-volume behavior with a throwaway EPUB that grows by appending a chapter. Record a location partway through chapter 53, deliver chapter 54 through the intended import route, and verify both the current open session and reopening. Test Previous/Next, the table of contents, completion state, and movement across a completed volume/book boundary. Stable existing chapter resources and spine order are hypotheses to test, not guarantees of retained position.

   Include an author-supplied EPUB with its own cover/TOC and a release containing multiple chapters. Existing volume assembly has one entry per release, so a single-chapter fixture would miss a real limitation. Preserve internal chapter navigation where available; report ambiguous segmentation rather than inventing chapter boundaries. Exercise missing chapters and unknown author-book endpoints without presenting an incomplete capture as a complete book.

   Open an enriched EPUB offline and import it into a clean library using only that file. Check readable content, cover, author, description, series order, language, and About page. Separately test supplemental author-profile API updates; reading the file must not depend on them.

   Produce an evidence matrix and a reader/output decision. If a stock reader supports a growing copy with exact position retention, specify its append and closure rules before implementation. If it fails, evaluate a small Previous/Next patch while preserving stable reading copies. If neither approach meets the position contract at reasonable scope, return with the observed trade-off before weakening that contract or undertaking a larger fork. Switching to BookOrbit is conditional on the results.

2. Retain and resolve portable metadata.

   Model author profiles separately from series/book metadata. Keep author identity stable across sources; names alone are insufficient for matching. Retain selected fields, asset bytes, and source/capture provenance so preview and rebuild work offline. Start with raw Patreon campaign/user/collection metadata already captured; separately acquire referenced images. New Patreon requests use the shared request budget.

   Resolve overrides and suitable embedded fields before using clearly matched creator material to fill gaps. Chapters may inherit series/book artwork. External candidates without a clear identity link go through offline review. Dump refresh preserves authored choices. Configuration is the durable editing surface; reader edits are not a second source to synchronize back in the first version.

   Keep provider extraction inside the provider and metadata selection shared by preview, normal sync, and rebuild. Separate upstream content identity, curated metadata identity, and edition identity. Profile enrichment must not create a false story-content change, while metadata/cover changes must affect publication comparison during explicit rebuild. Current single-artifact reuse and volume-edition hashing need changes to satisfy both rules. Keep compact references behind the store boundary, with captured material and assets on disk.

3. Produce self-contained EPUB editions.

   Implement the navigation approach selected by the trial, including the append/closure lifecycle or the narrowly scoped reader patch it requires. Cover forward and backward movement across publication boundaries and the arrival of new content after reaching the current end. Keep the reader-specific change separate from portable metadata selection and EPUB construction.

   Extend the shared artifact path for generated chapters, enriched EPUB attachments, converted PDFs, and volumes. Preserve suitable original metadata unless an override supersedes it. Embed compatible series/order fields and selected assets without changing story text or breaking existing links. Apply decoration at the final publication boundary so a volume receives one cover and one About page, rather than a copy after every member chapter.

   Snapshot publication inputs with each edition. A metadata-only edit must appear in rebuild preview without replacing existing copies during ordinary sync. An unchanged rebuild must reproduce unchanged output. Pending deliveries continue using their saved bytes even if newer metadata or corrections arrive.

   Any growing-copy design must explicitly pin existing publication inputs: appending chapter 54 cannot silently incorporate a correction to chapter 52 or a refreshed cover. Those changes belong to a separately previewed transition. Completed editions retain the explicit-rebuild contract. Preserve the two output modes, `preserve` and `epub`; enriching original attachments belongs to EPUB publication, with an explicit config/rebuild choice for existing byte-preserving series.

   Validate original archive hashes and retain story XHTML, styles, images, and nested navigation. The current assembler uses ordinal member paths and drops member navigation documents; stable book identity alone cannot protect reader locators across structural changes. Test real EPUB 2/3 attachments with EPUBCheck and a clean reader import.

4. Reconcile the library and deliver notifications.

   Build the narrow adapter needed for the selected reader around existing filesystem/exec publishers. Track stable downstream identity, acknowledged file versions, and readiness. Use one controlled import/refresh sequence; historical watcher/refresh overlap produced duplicate author associations. Verify overwrite and retirement behavior before automated replacement.

   Honor the at-least-once delivery protocol: retain event IDs and downstream receipts, make retries safe, and preserve user-modified or unrelated files. The current hook is per artifact and has no batch-complete readiness event. Add the smallest orchestration needed to aggregate verified availability for a sync. A file copy or accepted refresh request is insufficient; the reader must acknowledge imported content before its predecessor can be retired or it can be announced as ready.

   Trial exe.dev's notify integration first. Verify its API, VM attachment, service limits, and reading-link behavior. If unsuitable, use hosted ntfy with minimal summaries and authenticated reader URLs. Prove delivery to a locked/backgrounded phone and navigation after tapping. Dedupe ordinary retries and suppress unchanged runs; a metadata-only rebuild is maintenance, not new chapters. Notification failure must not undo chapter delivery. Record any API limitation on deduplication rather than promise exactly-once phone delivery.

5. Validate and roll out one series before the library.

   Exercise the chosen path in Docker, from captured material through EPUB generation and reader import to a readable link. Validate correction fallback separately from ordinary arrivals. Run an unchanged hourly cycle and an interrupted/retried delivery; confirm output and notification behavior.

   Preview a rebuild for one representative series and retain a rollback path to previous files/configuration. Before a reader switch, rehearse migration from the actual deployed version and sampled existing reading positions in a disposable instance. Published files alone cannot restore reader-owned progress, so include reader state in the migration/rollback rehearsal.

   Enable hourly operation after the pilot succeeds. Expand by series while checking metadata, duplicate entries, readiness, and retained positions. Revalidate the deployed reader and local configuration first; historical counts and source inspections are not a current deployment check.

The acceptance cases for implementation are:

| Scenario | Required observation |
| --- | --- |
| EPUB used by itself | Story, embedded artwork, About page, and navigation work offline; a clean import gets supported catalog fields without sidecars or a prefilled database. |
| Chapter 54 arrives | It appears through normal hourly operation; a saved position partway through 53 remains exact. Desired Previous/Next behavior is recorded explicitly. |
| Volume/book boundary | Available next content is reachable without losing place; incomplete or ambiguous coverage remains visible. |
| Multi-chapter release | Internal chapters remain readable and navigable; unsupported segmentation is recorded, not hidden by a release-level TOC. |
| Current-chapter correction | Retain position or return to that chapter's beginning with a notice, using the selected correction/rebuild path. |
| Metadata/cover change | Content identity stays stable; preview shows affected publications; existing editions change only on rebuild. Repeating the rebuild is a byte-stable no-op. |
| Append after metadata/correction refresh | Existing reading-copy inputs stay pinned; ordinary append does not silently adopt unrelated changes. |
| No suitable artwork or ambiguous author | Chapters publish; no unrelated artwork/profile is silently selected. |
| Lost acknowledgement or restart | Retry delivers the saved bytes and converges without duplicate library entries or routine duplicate notifications. |
| Library import failure | No false ready notification or premature retirement; failed work remains actionable and retryable. |
| New chapters ready on phone | One grouped summary reaches the backgrounded phone and opens an authenticated reading page. Unchanged cycles stay quiet. |
| Reader migration, if selected | Representative existing progress survives; rollback restores both files and reader state. |

For implementation, run `go test ./...`, the demo `setup check`, fixture-backed preview/run, and relevant Docker end-to-end checks. Observe preview filesystem/state immutability and verify changed EPUBs with the image's EPUBCheck/Calibre tools. Update `README.md`, `docs/config.md`, `docs/rules.md`, and the rule-authoring skill with workflow changes; update hook documentation if its contract changes. Test user-visible behavior and keep selection logic shared across normal, preview, and rebuild paths.

The publication strategy keeps existing singles stable as chapters arrive. Stock BookOrbit supplies the mobile reader and OPDS; moving between separately published chapters uses its library interface. The optional experiment demonstrated bidirectional reader navigation and stale-resource correction handling, but those behaviors are not part of the selected stock deployment. Arbitrary volume regrouping requires a separately verified progress migration; the adapter refuses unsafe same-path coverage changes and retirement of a partly read copy. ntfy is the active notification transport after exe.dev tests opened only the exe.dev app. The user confirmed that the ntfy phone test arrived and opened BookOrbit. Historical metadata changes require an explicit rebuild. See the validation record and the infrastructure runbook for measured rollout results.
