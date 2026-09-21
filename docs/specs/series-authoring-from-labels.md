# Spec: Series authoring from labels

Status: implemented

All five slices are implemented. Validation on 2026-09-21 covers the full container suite, offline fixture workflows, and the private compatibility and correctness gates below. Prateek agreed to its direction on 2026-09-20, including discovery candidates kept in SQLite with a small `setup candidates dismiss` command. Written 2026-09-19 and checked on 2026-09-20 against `main` at `0201d5e`. The first draft went through a no-context adversarial review the same day, and this draft rebuilds the design around the findings that held up against the code and the stored data. The first draft, the review and the evidence are in the private `~/.local/share/serial-sync/private/series-authoring-revamp/research/config-revamp/` archive; creators are described here by the shape of their feed and named only there. This spec builds on `docs/specs/reader-ready-library-output.md`, which landed on `main` as `0201d5e` with books, sequence overrides, the shared sequence detector and `run --rebuild`. Each dependency on it is named where it occurs.

## Problem Statement

I subscribe to seven paid creators. One is configured, in 411 lines of title regexes I wrote by hand. Covering all seven with the matchers that exist today takes 1,395 lines for 45 series.

- A new series goes unnoticed. One sat in the review bucket for 16 chapters because no rule named it, and nothing told me.
- A typo in a title drops a chapter. "Chaper 790" and "Roonbound" fell into review until I loosened a regex. One creator's unlabelled feed has "Chaptger 204", "Chapeter 480" and "Chapter -186".
- The creator's own labels are strong evidence, and I can't use them safely. Collections cover the five current series of one creator completely, 750 chapters of 750. But a label rule files everything that carries the label: on another creator a tag rule publishes 13 news posts as chapters. A title rule fails the other way and misses 18 preludes, epilogues and oddly titled chapters that the tag catches. Labels are also sometimes wrong: one chapter is tagged for one series and titled for another.
- Label names change. One creator renames a collection to `<name> [completed]` when a story ends, and another names the books of one series inconsistently. Matching is by exact name, so a rename silently stops matching.
- I can't see what a config change will do. Nothing replays stored posts through two configs and compares them. I had a throwaway tool written for that.
- Preview and run read different files. I author rules in a dump workspace's `series.toml`, then copy them into the main config by hand.
- Bad rules pass validation. A regex that fails to compile never matches and nothing reports it. My live config uses a release role that does not exist. Checking a config initializes the live database.
- Every series repeats the same output block and author. About two thirds of the 1,395 lines are repetition.
- Adding a creator is slow. I can't list my memberships without dumping them, and `setup dump --force` deletes the workspace, authored rules included, before it checks that I'm logged in.

## Solution

One rule model, less of it to write, and a detector that watches every post for a series I haven't mapped.

- serial-sync looks for new series in every post it observes, including posts a rule already claimed. A new collection, an unexplained "Chapter 1", or a new title family inside a catch-all becomes one grouped candidate that I confirm with a config edit or dismiss with a reason. It never enrolls or publishes on a guess.
- The rule model stays the one that exists: prioritized inputs, first match wins. An input can name several collections, tags and patterns at once, and can carry guards: a minimum body length, and tags or title patterns that send the post onward. "This label, and enough body text to be a chapter" becomes one input.
- Output settings, author and content strategy come from defaults. Review is built in, and a small `[[review]]` rule routes administrative posts there without a review series per creator.
- One post can be overridden by its provider ID, with a reason.
- `setup preview` replays stored posts offline under a candidate config and compares it with my live config post by post. It reads the same file `run` reads. It reports classification and output-policy changes, and uses the rebuild planner to say what the published library would change.
- Collections are captured by provider ID, so a rename keeps matching and is reported.
- Checking a config and previewing are read-only. A failed dump refresh loses nothing. These land first, because a creator migration leans on them.
- Existing configs keep classifying as they do now.

## User Stories

1. As a reader, I want a new series reported at its first captured chapter, so that I never again find 16 chapters sitting in review.
2. As an operator, I want that report raised even when an existing rule already claimed the posts, so that a broad fallback or a catch-all collection can't hide a new story.
3. As an operator, I want a collection, its tags and its title family reported as one candidate, so that one new story is one item.
4. As an operator, I want to dismiss a candidate with a reason, and have it return only when its evidence changes, so that the report stays short.
5. As an operator, I want to mark a label as ignored for discovery without excluding its posts, so that genre tags and per-chapter tags stop raising candidates.
6. As a reader, I want nothing published into a guessed series, so that my library changes only when I change the config.
7. As an operator, I want to opt a broad rule into holding posts that carry a first-chapter marker or an unknown collection, so that a second story isn't filed into the first while I decide.
8. As an operator onboarding a creator, I want the same detector run over a fresh dump with no mappings, so that the candidate list is my first draft of that creator's series.
9. As an operator, I want one input to name several collections, tags and patterns, so that a series is a few lines.
10. As an operator, I want an input to require a minimum body length, so that notices filed under a story label fall through to review.
11. As an operator, I want an input to skip posts carrying given tags or title patterns, so that comics and indexes under a story tag stay out without a separate higher-priority rule.
12. As a reader, I want a chapter with a typo'd title to reach its series through its label, so that I don't find a gap weeks later.
13. As a reader, I want preludes, epilogues and interludes without a chapter number to join their series, so that a number is never the price of admission.
14. As an operator, I want the order of my inputs to decide between a label and a title that disagree, so that I choose which evidence wins for each creator.
15. As an operator, I want a post whose label and title point at different series reported, so that a mis-tagged chapter is visible whichever way it was filed.
16. As an operator, I want to override one post by its provider ID with a reason, so that a single exception needs no regex.
17. As an operator, I want output format, preface mode, author and content strategy to default, so that I state them once.
18. As an operator, I want unmatched posts to land in built-in review, and a `[[review]]` rule for posts I send there on purpose, so that I declare no review series.
19. As an operator, I want a post I routed to review reported when it looks like fiction, numbered or not, so that a tag like `poll` on a real chapter doesn't silently drop it.
20. As an operator, I want collections matched by provider ID, so that a rename to `<name> [completed]` keeps matching.
21. As an operator, I want preview to print each collection's ID beside its name, so that I can pin one without reading raw JSON.
22. As an operator, I want a chapter-like post whose file is missing to keep its series and show as unavailable, so that a missing attachment reads as a problem and not as an absence.
23. As an operator, I want a post with several book files reported, so that I know only one was used.
24. As an operator, I want to replay all stored posts under a candidate config with no provider contact, so that authoring costs no Patreon traffic.
25. As an operator, I want that replay compared with my live config post by post, so that I see every post whose series, role, selected content or output policy would change.
26. As an operator, I want the comparison to say plainly what it cannot know yet, so that I don't mistake a classification diff for a library plan.
27. As an operator, I want preview to read the same config file `run` reads, so that promotion is replacing one file.
28. As an operator, I want `setup check` and preview to work with config and state mounted read-only, so that checking can't disturb the library.
29. As an operator, I want an invalid regex, enum value, glob or key rejected by `setup check`, `setup preview` and `run` alike, before any provider call, so that a broken rule can't pass as "no match".
30. As an operator with an existing config, I want it accepted and classifying as before, so that upgrading changes nothing until I change the config.
31. As an operator, I want to list my memberships with no source configured, so that I can start a new machine from an auth profile alone.
32. As an operator, I want a failed or interrupted dump refresh to keep my previous capture and authored files, so that a login problem can't destroy work.
33. As an operator, I want a moved dump to preview with its attachments, so that a capture survives a changed mount.
34. As a contributor, I want the config reference, the series guide, the README, `AGENTS.md` and the rule-authoring skill updated in the same change, so that guidance matches behavior.

## Implementation Decisions

### Vocabulary

The repo has no glossary. These terms are used here with one meaning each, beside the ones the reader-output spec lists (source, series, series input, release, release role, artifact, publisher, run, author book).

| Term | Meaning |
| --- | --- |
| Label | A collection or tag the provider attaches to a release. A collection has a provider ID and observed names; a tag has a name. |
| Selector | The part of an input that matches a release: collections, tags, title patterns or attachment patterns. |
| Guard | A condition an input adds to its selectors. A release that fails a guard passes to the next input. |
| Review | The built-in unmatched, unpublished state. It exists today as `unmatched`. |
| Override | A per-release decision keyed by source and provider release ID, with a reason. |
| Candidate | A grouped set of evidence that an unmapped series, book or story may exist. |
| Replay | Classifying stored releases offline under a given config. |

### One rule model

The first draft added a second, compact config language with a fixed evaluation order, switched per source. That is withdrawn. A fixed order already misfiles a stored post whose tag and title disagree, a per-source switch blocks the compact form for any source that shares a multi-source series, and two evaluation semantics in one file are hard to reason about. Everything below extends `[[series.inputs]]`; prioritized first-match evaluation stays the only semantics, and top-level `[[rules]]` keep compiling into the same list.

- An input may replace `match_type` and `match_value` with selector lists: `collections`, `tags`, `title_patterns`, `attachment_patterns`. A release matches the input when any selector matches. `match_type` and `match_value` stay valid; an input uses one form or the other.
- An input may carry guards. `min_body_chars` requires that much body text. `unless_tags` and `unless_title_patterns` reject a release that carries one. A release that matches the selectors and fails a guard continues to the next input, and preview records which guard failed.
- `[defaults]` supplies `format`, `preface_mode`, `release_role`, `content_strategy`, `attachment_glob`, `attachment_priority` and `min_body_chars`. `[[sources]]` may set `author` and a `[sources.defaults]` table with the same keys. Inheritance runs defaults, then source defaults, then series output, then the input. These are inherited defaults and nothing more; routing decisions stay on inputs, so the `AGENTS.md` rule that series inputs decide classification stands, with "sources may carry defaults their inputs inherit" added.
- A series with no `authors` uses the source's `author`, which defaults to the provider's creator name.
- Unmatched releases land in built-in review, as they do today, so a review series with a `fallback` input is unnecessary. A top-level `[[review]]` entry routes releases there on purpose. It takes a `source`, a `priority`, the same selectors and guards as an input, and a required `reason`. Existing review series keep working.
- `[[overrides]]` entries carry `source`, `release_id`, either `series` or `review = true`, and a required `reason`. Overrides are evaluated before any input. The key names match the reader-output spec's sequence overrides.
- When `priority` is omitted, inputs keep today's default, which orders them by position. The docs recommend the pattern the corpus supports: specific title shapes, then labels with a body guard, then broad backstops.
- The role enum gains `extra`, which the deployed config already uses for bonus stories. It means fiction that fills no chapter slot. This ships with validation, in the first slice, so the deployed config never fails a check.

- A series may set `source`, which its inputs inherit. An input that names its own source overrides it, so multi-source series work as before.

Measured on a sketch with no code behind it, this shape writes the seven creators in 563 lines against 1,395, a 60 percent cut. The withdrawn compact form measured 431. The difference is the `[[series.inputs]]` header and priority that each input keeps, which is the price of one evaluation model.

### Body length is a guard and never a verdict

- `min_body_chars` counts Unicode characters of the visible body text after entity decoding and whitespace normalization. The prototype measured UTF-8 bytes of the provider's plain text, so its numbers overstate slightly. The provider's HTML-to-plain conversion is fixed to decode entities as part of this work.
- The guard keeps short notices out of label-matched series. It does not identify fiction. In the corpus it separates every labelled notice from every chapter for four creators with a wide gap, needs 2,000 for a fifth, and cannot help the sixth, where Q&A and reference posts of 4,440 to 8,990 characters sit inside story collections and the shortest chapter is 4,910. Long non-fiction needs `unless_tags`, `unless_title_patterns` or an override.
- The default is 1,500. A chapter number is never required to join a series: requiring one would drop 28 stand-alone stories and every unnumbered epilogue in the corpus.
- A release whose input wants an attachment and has none keeps its series assignment and is reported as unavailable, which is today's `attachment_only` behavior. A guard failure means "probably a notice"; a missing file means "a chapter I can't read yet". They are different outcomes.
- The decision records which input matched, by which selector, which guards ran and the selected content reference. Preview, preparation and materialization use that one reference, so a filename pattern that matched one file can't lead to a different file being published.

### Finding new series

Discovery is separate from classification and reads every observed release: assigned, routed to review, and unmatched. A release being claimed by a rule never exempts it.

Signals, each with its limit:

| Signal | Use | Limit |
| --- | --- | --- |
| A collection ID or name no input, review rule or ignore list references | Strong evidence of a new unit | Could be a book, an art collection or an archive |
| A specific new tag | Supporting evidence | Tags are reused and inconsistent |
| A first-chapter marker ("Chapter 1", "Prologue") or a numbering reset the assigned series can't explain | Immediate evidence of a start | Could be a repost, a new book or a teaser |
| A new title family inside an existing label or fallback | A second story hidden by a catch-all | Title-format changes look the same |
| Label and title pointing at different series | A mis-tag, or a rule in the wrong order | Needs a human either way |
| Substantial body or a book file with no match | A possible story | Long announcements look the same |
| A link-only post with a chapter-shaped title | An off-platform serial | Content is not local |

Behavior:

- One versioned feature extractor serves preview and sync. It records title stem, sequence reading, first-chapter markers, label identities, post type, content measurements, attachment references and link evidence. It stores no body text.
- Correlated evidence groups into one candidate: a collection, its tags and its title family on the same releases are one item. Later releases join a candidate by label, stem or numbering continuity. A bare "Chapter 1" with nothing else creates an anonymous candidate anchored to its release ID.
- One qualifying release is enough to raise a candidate. More releases strengthen it. Candidates have three states: possible, needs decision, resolved. There is no score.
- Candidates live in SQLite behind the store boundary, with SQLC queries: stable ID, source, kind, correlation key, member release IDs, first-observed and last-evidence-change times, an evidence fingerprint, the extractor version, status, dismissal reason and the fingerprint last reported. The existing `discovered_at` is overwritten on every upsert and is not used as first-seen. The first run over a backlog produces one grouped baseline report; later runs report changes.
- Confirmation is a config edit: a new series, a new book, an override for a bonus story, a repost or edition decision. `ignore_labels` on a source marks a label as ignored for discovery and leaves its posts alone. Preview shows which candidate members an edit resolves.
- `setup candidates` lists candidates, and `setup candidates dismiss <id> --reason` records a dismissal against the current evidence. New members, a new title family or changed content reopen it. Preview never changes candidate state.
- `run` prints new and materially changed candidates first, then a few unresolved ones, then a count. A config change that exposes an old problem counts as new. The report never changes the exit code.
- Sync makes no extra request for discovery. A collection that appears on no captured post can't be found from local data, and the report says what capture scope it covers.
- The detector ships advisory. Separately, an input or review rule may set `hold_candidates = true`, which keeps a release in review, reported, when it would match that input and also carries a first-chapter marker, a numbering reset or an unreferenced collection. That is a behavior change an operator opts into per input, and the broad backstops are where it belongs.
- Run over a fresh dump with no mappings, the same detector yields the candidate list for a new creator, which preview can print as a config fragment to review.

What it would do in the cases that matter:

| Case | Result |
| --- | --- |
| A new series in a new collection | One candidate at the first captured post. Nothing publishes until a series is mapped. |
| An unlabelled "Chapter 1" on a feed with a broad backstop | A candidate at once. With `hold_candidates`, the post stays in review; without it, the post is filed and reported. |
| A new story under a reused tag or a catch-all collection | A new title family or numbering stream raises a candidate although a rule matched. |
| A second series on a feed with no labels and text in attachments | Attachment names or a numbering restart raise a candidate. With generic names and no restart, this is missed. |
| A new book of a mapped series in a new collection | A candidate proposing a book entry. Chapters keep publishing through the series' title patterns. |
| A bonus story, a teaser, a reposted chapter | A possible candidate, a tentative one, and repost evidence when the release ID or content hash matches. None becomes a series automatically. |
| Chapters published off Patreon | A candidate marked content-external. Nothing publishes and nothing is fetched. |

False positives will include new books, bonus scenes, teasers, reissues and title-format changes; grouping and evidence-scoped dismissal keep them cheap. False negatives remain when a creator starts a second series with the same labels, names, numbering and cadence as the first. Metadata can't separate those, and the docs say so.

### Replay and comparison in `setup preview`

- `setup preview` reads the config given by the global `--config` flag, the file `run` reads. `--series-file` stays for the dump-workspace flow. `--workspace` reads a dump and `--stored` reads the catalog's current normalized releases, resolved through catalog references so that obsolete paths and duplicates are not replayed. Both feed one code path.
- `--compare <config>` classifies a fixed corpus under both configs, covering the union of their sources, so a removed source still shows.
- The comparison has three parts and names them. Classification: series, role, deciding input and selector, guard results, selected content, eligibility. Output policy: effective format, preface, author, series title, book, sequence and destinations. Applied library state, the additions, replacements and retirements that would follow, comes from the planner behind `run --rebuild --dry-run`, which already reclassifies stored releases under the current config and opens the store read-only. Ordinary runs revisit only recent releases and ignore a mapping change on unchanged content, so the comparison says which changes need `run --rebuild` to take effect.
- The output is bound to both config hashes, the software and normalizer versions and the capture inventory.
- Replay is offline and read-only: no provider or bootstrap call, no database initialization, no run record, no directory creation. `setup check` loads and validates the config and does nothing else. Both work with config and state mounted read-only.
- Promotion is replacing the live config with the previewed file.

### Label identity

- The provider already reads collection IDs from `data.relationships.collections.data[].id` and discards them after resolving names. It keeps them, keyed by provider, campaign, resource type and ID, with every observed name. A relationship with no included name still yields an identity. The included-resource index is keyed by type and ID.
- Collection references are stored as enrichment outside the hashed content, and normalization gains a version. Without that, adding the field would change every content hash and make every revisited release look changed.
- A `collections` selector entry is a name, or a table with `id` and `name`. An ID match survives a rename. Name matching stays case-insensitive.
- Reports say "possible drift": a pinned ID seen under a new name, or a name-matched collection absent from recent captures while new releases match the same series by title. A collection that stops receiving posts is not reported as deleted.
- Stored releases are enriched offline from their stored raw JSON, in memory for preview and durably through an explicit step. Nothing is refetched. That ID stays stable across a rename is inferred from the captured data; it has not yet been observed across two captures.

### Books

- Series membership, book identity, chapter coverage and publication grouping are separate. This spec touches the first two.
- The `[[series.books]]` definition from the reader-output work gains optional `collection` and `tag` fields. A book's label is a selector for the series that also sets the release's `book_id`. A label gives a book its identity and never its boundaries: expected first and last chapters stay declared, as that spec requires, and a volume can't complete without them.
- A release that joins a series with a book marker no declared book explains has an unresolved book, which is reported as a candidate and keeps the release out of fixed-range volumes.
- The shared detector is `artifact.DetectSequence`, which accepts `chapter`, `chap`, `ch` and `chaper` and returns integer book and chapter values with the matched text. Discovery reads it; no classification rule depends on it. The cases this work adds to its table are `Name 3.60`, `B6C57`, `[Part K8]`, `Ch. 24D`, `61.5`, `Chapter -186`, `Chaptger 204`, `Chapeter 480` and a bare `737 - Title`. Decimal and suffixed forms need the result type extended first, so that they keep their meaning and are not flattened to an integer.

### Validation

- One strict loader serves `setup check`, `setup preview` and `run`, for the main config, top-level `[[rules]]` and standalone series files, before any provider work.
- It rejects unknown keys, invalid enum values, regexes and globs that fail to compile, references to unknown sources, series or books, an input that mixes `match_type` with selector lists, and an override or review rule without a reason.
- Supported legacy configs keep their behavior. Errors that were silently ignored before get a deliberate diagnostic naming the file, owner, field and fix.
- Warnings cover the cheap cases: a fallback placed above other inputs, identical selectors on two inputs, and overlaps observed during replay. General shadowing analysis is left out.

### Memberships and dump safety

- `setup memberships` lists creator, URL, paid or free, and whether the config has a source for it, whether that source is enabled, and whether any input references it. With a valid saved session it makes one metadata request; with an expired one it says so and points at `setup auth` without starting a browser. It and `setup dump` work from a config with an auth profile and no sources.
- `setup dump` builds a complete new capture generation beside the old one and switches the manifest to it atomically after success. Authored files live apart from capture generations and are never deleted or rewritten. It refuses a directory that is not a dump workspace. The docs stop recommending `--force`.
- Manifests and the attachment paths recorded inside normalized posts are stored relative to the workspace and resolved from the directory actually opened. A workspace-local file wins over an old absolute path that still exists.

### Slices and order

1. Safe authoring: dump generations and relocation, read-only `setup check`, the shared strict loader, and the `extra` role.
2. Seeing: `setup preview --stored` and `--compare` with the classification and output-policy parts, complete decision explanations, and advisory discovery with durable candidates.
3. Less to write: defaults, grouped selectors, guards, `[[review]]`, overrides, author default.
4. Identity and content: collection IDs as enrichment, label-against-title conflict reporting, the selected-content reference, `hold_candidates`, `setup memberships`.
5. Books from labels and the applied-state comparison, on top of the landed reader-output work.

### Docs

- Implementation updates `README.md`, `AGENTS.md`, `docs/config.md`, `docs/rules.md`, `docs/first-source.md`, `examples/config.demo.toml` and `skills/serial-sync-rule-authoring/SKILL.md` together, with exact syntax.

## Testing Decisions

A good test here classifies posts the way an operator would see them classified and checks the visible result: which series and role each post got, why, what content was selected, and which candidates were raised. It does not assert on rule structs, priority numbers or call order.

Three seams, chosen with Prateek:

- `setup preview` in JSON form over a committed synthetic dump fixture, extending `TestPreviewRulesUsesDumpedWorkspace`. That test exercises the service directly today; this work adds tests through the CLI boundary as well. The fixture has invented creators and filler text shaped like the measured feeds, chosen for disagreement between signals and not only for the happy path. It holds no paid content and no real names.
- The fixture-backed lifecycle test (`TestSyncAndPublishLifecycle`) proves that `run` classifies the fixture as preview does and that the run summary carries candidates. Agreement between the two is a consistency check; both can agree on a wrong answer, which is what the expectation set below is for.
- Table tests on `internal/classify`, which has none today, through `Explain` and its decision: selectors, each guard at its boundary, overrides, review rules, ID and name matching, and legacy inputs behaving as before.

| Scenario | Required result |
| --- | --- |
| A thin notice in a mapped collection with a body guard | The notice passes to review and preview names the failed guard. Chapters join. |
| A typo'd title inside a mapped collection | Joins through the label. |
| News posts sharing a series tag; an unnumbered prelude and epilogue with the tag | The news posts fail the guard. The prelude and epilogue join. |
| A post tagged for one series and titled for another | Filed by whichever input the operator ordered first, and reported as a conflict. |
| A post routed to review by tag that has a chapter-sized body, with and without a number | Stays in review and is reported both times. An override files it and settles the item. |
| Long non-fiction in a story collection | Joins until `unless_tags`, `unless_title_patterns` or an override excludes it. |
| A title-matched post that later gains an unrelated attachment, in a series with per-input strategies | Still publishes the body. |
| A filename pattern matches one file while the format preference favors another | The matched file is the selected content everywhere. |
| A chapter post missing its file; a post with six book files | The first keeps its series and shows as unavailable. The second is reported and materializes one file, as today. |
| Body measurement: entities, whitespace-only bodies, non-ASCII text, HTML and plain text disagreeing | Counts follow the stated definition. |
| Discovery, fed chronologically: first post, second chapter, repeated lookback, restart, a long gap, dismissal, evidence change | One candidate at the first qualifying post, no duplicate counts from lookback, state survives restart, dismissal holds until evidence changes. |
| Discovery, counterfactual: remove an existing series mapping from the fixture config | The detector raises it at that series' first release, using nothing learned from later ones. |
| Discovery inside a catch-all collection and behind a broad backstop, with and without `hold_candidates` | A candidate in both. The post is held only when opted in. |
| The seven cases in the discovery table | Each behaves as the table says, including the stated miss. |
| Ninety-odd per-chapter and genre tags on mapped posts | One `ignore_labels` edit clears them, and none of their posts is excluded. |
| A renamed collection, matched by name and then by ID | By name, matching stops and possible drift is reported. By ID, matching continues. |
| Adding collection references to stored releases | No content hash changes and nothing republishes under an untouched config. |
| A legacy config: series inputs, top-level rules, a standalone series file, the `extra` role | Accepted, and every release classifies as before. |
| An unknown key, a bad regex, an unknown role, mixed input forms, an override without a reason | `setup check`, `setup preview` and `run` each fail before any provider call and name the file, owner and field. |
| Comparing two configs that differ only in author, format, a removed source, or a disabled source | Each difference appears in the output-policy part. Identical configs list nothing. |
| `setup check` and replay with config and state mounted read-only and networking disabled | Full output, no writes, no schema initialization, no run record. |
| A dump refresh that fails before install, and one interrupted during install; a moved dump with the original directory present and absent | The previous generation and authored files survive. Preview reads the moved copy, attachments included. |
| `setup memberships` from a profile-only config, with a valid and an expired session | A listing from one request; or a clear reauthentication message and no browser. |

The real corpus is a local acceptance gate, outside CI, read in place with nothing copied into the repo. The private archive named above contains the capture locations, replay tools, and expectation set; these inputs are not distributed with the public repository. Contributors use the synthetic fixtures for CI:


1. Compatibility: the live config classifies the 1,505 stored releases of the configured creator exactly as they are stored, before and after every slice. The prototype shows 0 differences today.
2. Regression baseline: `~/.config/serial-sync/config.proposed.toml` uses only today's matchers and was checked by replay. A config rewritten with the new selectors and guards must match it on all 5,427 stored releases, except for a reviewed list of intended differences. This proves the rewrite preserved decisions. It does not prove the decisions are right.
3. Correctness: a hand-adjudicated expectation set keyed by release ID, kept local beside the research notes, covering the cases where signals disagree: short fiction, long non-fiction, mis-tagged chapters, bonus scenes, reissued editions and deliberate exclusions. Both configs are tested against it.
4. Discovery: with the mapping for the series that went unnoticed removed, and its releases fed in posting order, a candidate is raised at the first qualifying release. The stored snapshot shows all 16 in the collection today; it does not show which labels the first post carried when first captured, so historical day-one detection remains unproven. The offline gate passed on 2026-09-21: a candidate appeared at the first captured member, using only the chronological prefix, with all 16 members identified in the current snapshot.

Implementation validation passed on 2026-09-21: the full Go suite in the intended
container runtime, `go vet ./...`, demo `setup check`, authoring-skill validation,
and fixture-backed preview, sync, enrichment, and rebuild. With networking disabled
and config/state mounted read-only, comparison planned one book addition and two
single-file retirements; the writable offline rebuild applied that plan, and a
subsequent comparison reported no changes. Collection IDs survived a fixture rename.
The private gates retain zero legacy assignment differences across 5,427 releases
and all 23 confirmed expectations pass. Live config and published content were not
modified. Browser authentication and physical reader acceptance were not exercised.

## Out of Scope

- Fetching chapters that a creator publishes off Patreon. Discovery reports them; nothing fetches them.
- Splitting a post that holds several chapters or several book files. The report flags these; the policy belongs to the reader-output work.
- Enrolling a series automatically, publishing on inference, prose analysis, embeddings and any external inference service.
- Fuzzy title matching that publishes. Similarity may propose a correction in a candidate.
- Volumes, EPUB metadata, filenames and the offline rebuild, which the reader-output spec owns.
- Importing a dump into the operational catalog so that a new creator's backlog isn't refetched. It is worth its own spec: today `run` ignores dumps.
- A home and backup for the config file.
- A local HTML review page.
- Removing `match_type` inputs or top-level `[[rules]]`.

## Further Notes

- Measured shapes across the corpus, under a config that uses today's matchers and was checked by replay:

| Feed shape | Posts | In a story series | What it needs from this spec |
| --- | ---: | ---: | --- |
| Collections for current series, tags for older ones, title regexes for one-offs | 1,505 | 1,080 | Label selectors with a 2,000-character guard; `unless_tags` |
| Tags until a switch to collections, 742 unlabelled numbered posts, side fiction, one mis-tagged chapter | 2,249 | 2,129 | Title shapes ahead of guarded labels; conflict reporting; `hold_candidates` on the broad backstop |
| One collection per book; the body duplicates the attached PDF | 531 | 400 | Books from labels; body as content |
| One series, a collection per book, 16 chapters never filed | 362 | 330 | Books from labels; a title backstop |
| No labels; text only in attachments; a re-issued edition; about 96 per-chapter and genre tags | 312 | 241 | Attachment strategy as a source default; `ignore_labels` |
| A collection per story, renamed on completion; polls inside chapters; long Q&A posts in story collections | 216 | 126 | Collection IDs; `unless_title_patterns`; no tag-based exclusion |
| Chapters published off Patreon; three early test chapters with full bodies, published as extras | 252 | 3 | Out of scope, apart from a content-external candidate |

- Measured by replay through the repo's classifier: the counts above; 0 of 1,505 releases change under the label-first rewrite of the live config.
- Measured by queries over replay output, and weaker for it: the 742 unlabelled numbered posts fit their series' numbering with no number shared between an unlabelled and a labelled post, which supports and does not prove that every one belongs to that series; in a 12-post sample from the PDF-duplicating creator, 89 to 100 percent of the body's 8-word sequences appear in the PDF, and the per-sample numbers were not retained.
- Measured on 2026-09-21: a private rewrite using grouped selectors, source inheritance, and defaults preserves series, role, strategy, and materializability on all 5,427 captured releases and passes all 23 confirmed expectations. Its explicit zero body thresholds preserve the baseline; this does not adjudicate new guard thresholds.
- Inferred: that 1,500 characters is a good default beyond these seven creators; that collection IDs survive a rename.
- Order matters and stays the operator's choice. Three real chapters are titled with "Cover", "Reflection" and "Status Update", and one chapter is tagged as news. A keyword or tag guard placed ahead of the title shapes would drop all four.
- Agreed by Prateek on 2026-09-20: withdrawing the compact language in favor of selectors and guards on existing inputs, and discovery state in SQLite with a small mutating `setup candidates dismiss`. Smaller calls this draft makes and an implementer may revisit with evidence: `hold_candidates` as an opt-in, 1,500 as the default guard, `extra` as a role.
- The correctness oracle has 23 release IDs. Prateek confirmed the remaining 18 proposed assignments on 2026-09-21; all 23 are now confirmed in the private `validation/expectations.confirmed.toml` file. The proposed config passes all 23; the live config fails the one bonus scene he chose to publish.
- The throwaway replay tool and the config generators are in `~/.local/share/serial-sync/private/series-authoring-revamp/research/config-revamp/tools/`. The replay tool is the prototype for the preview work in slice 2. It reads every matching file under the artifact tree, which the real command must not do.
