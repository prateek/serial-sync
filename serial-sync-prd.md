# Serial Sync PRD

## Summary

Build a standalone Go utility for syncing serialized reading content from authenticated sources into durable artifacts and downstream publishers.

This product should be generic at its core, with Patreon as the first provider in v1. It should not be a FanFicFare feature first. The core problem is source sync, release normalization, artifact selection, and publishing, not just “one story URL in, one book out.”

The system should own:

- provider auth and session lifecycle
- source discovery and release enumeration
- normalized release storage
- story-track classification
- canonical artifact selection
- stateful, idempotent sync
- replayable publishing to generic targets
- strong observability and support tooling

FanFicFare remains useful as an optional downstream renderer or linked-site handler, but it should not be the host abstraction.

Working name for now: `serial-sync`.

## Why Standalone

Tools like FanFicFare are strongest when the input is already a story URL. Many creator platforms are not shaped like that. They emit mixed release streams:

- text posts
- attachment-driven releases
- announcements
- schedules
- previews
- multiple serials from one creator

From the sampled Patreon creators:

- BlaQQuill behaves like one source with attachment-driven chapter releases.
- Plum Parrot behaves like one source with multiple story tracks visible through tags like `AA3` and `AO2`.
- Actus behaves like one source with multiple story tracks where the split is mostly in titles.

That means the core abstraction is:

- `source`: an authenticated input feed
- `story track`: the logical serial or work being followed
- `release`: one upstream unit emitted by the source
- `artifact`: the canonical output chosen for that release
- `publisher`: the sink that receives published artifacts

This shape fits a sync utility much better than an adapter bolted into a downloader.

## Product Positioning

This should be:

- a standalone Go CLI with daemon-friendly operation
- a provider-agnostic core with a Patreon-first implementation
- a sync engine first, not a library manager first
- a filesystem-first publisher with hook-based extension
- open-source-friendly and easy to self-host

This should not be:

- a Calibre-specific product
- a browser extension
- a mandatory web app
- a Patreon-only architecture baked into every layer

## Goals

- Build a standalone Go CLI and service-friendly runtime for serialized content sync.
- Make Patreon the first provider in v1 without making the core Patreon-specific.
- Support XDG defaults for config, cache, runtime files, and state.
- Keep the configuration declarative and contained in one config file.
- Support both one-shot CLI use and repeated scheduled execution.
- Make repeated runs idempotent and cheap.
- Support dry-run and planning commands.
- Support multiple story tracks per source.
- Support text-post, attachment-preferred, and mixed-content sources.
- Keep the storage layer abstract so SQLite is only one backend choice.
- Keep provider-specific logic isolated so more sites can be added later without reshaping the domain model.
- Provide strong debuggability, traceability, and support tooling.
- Be easy to use, document, and contribute to as an open-source project.
- Ship cleanly in Docker for local server deployment and cloud deployment.

## Non-Goals

- Building a generic multi-provider creator sync engine in v1.
- Supporting every Patreon media format in v1.
- Building a rich web app before the CLI and sync engine are stable.
- Making Calibre mandatory.
- Coupling the application layer to SQLite.
- Relying on browser extensions as runtime dependencies.
- Claiming that fully unattended auth challenge handling will always work.
- Shipping a plugin SDK in v1.

## Product Principles

### Generic core, provider-specific edges

The core domain should be generic. Provider adapters should do provider-specific auth, fetch, and normalization work before handing data into the application core.

### Explicit state over implicit behavior

Every important step should have persisted state and observable outcomes. This matters for retries, support, and operator trust.

### Easy to operate

The default experience should work on a home server, NAS, or cheap container with minimal ceremony.

### Easy to debug

Every run should leave behind enough information to explain what happened, including successful runs.

### Open-source-friendly

The project should be documented, permissively licensed, and contribution-friendly from the start.

## Prior Art

This project should borrow aggressively from prior tools, but only in the layers where they are actually strong.

### FanFicFare

Take inspiration from:

- mature adapter and metadata concepts for linked fiction sites
- output artifact conventions and EPUB-oriented metadata handling
- the idea that downstream publishing should be optional and configurable

Do not copy:

- the core “one story URL in, one book out” abstraction as the host model
- provider-specific orchestration inside an adapter layer
- config complexity that leaks source-specific implementation details everywhere

### AutomatedFanfic

Take inspiration from:

- orchestration around an existing content tool
- Docker-first operation
- retry, scheduling, and worker-pool thinking
- separation between ingestion, coordination, and publishing

Relevant files inspected:

- [README.md](/tmp/AutomatedFanfic/README.md#L1)
- [config_models.py](/tmp/AutomatedFanfic/root/app/models/config_models.py#L1)
- [url_ingester.py](/tmp/AutomatedFanfic/root/app/services/url_ingester.py#L1)
- [coordinator.py](/tmp/AutomatedFanfic/root/app/services/coordinator.py#L1)

Do not copy:

- tight coupling to Calibre as the core sink
- email-driven ingestion as the product shape
- a model built around FanFicFare story objects instead of native source releases

### WebToEpub

Take inspiration from:

- browser-native authenticated fetching
- collection and feed handling as chapter lists
- the idea of a bootstrap or wizard that can inspect live creator data and suggest configuration

Relevant files inspected:

- [PatreonParser.js](/tmp/WebToEpub/plugin/js/parsers/PatreonParser.js#L1)
- [DefaultParserUI.js](/tmp/WebToEpub/plugin/js/DefaultParserUI.js#L1)
- [HttpClient.js](/tmp/WebToEpub/plugin/js/HttpClient.js#L151)

Do not copy:

- the extension packaging model as a runtime dependency
- parser and selector customization as the first abstraction
- the assumption that one feed or collection should always collapse into one EPUB

### `patreon-dl`

Take inspiration from:

- Patreon-specific fetch and filtering semantics
- support for creator feeds, collections, single posts, and attachments
- the importance of treating Patreon as a source system with multiple content modes

Do not copy:

- a hard dependency on a cookie-string-first UX
- a subprocess-centric architecture at the center of the product

### Borrowing matrix

| Behavior | Source | Decision | V1 note |
| --- | --- | --- | --- |
| linked-site rendering and metadata conventions | FanFicFare | adapt | keep optional, never the host model |
| orchestration, retries, Docker packaging | AutomatedFanfic | adapt | use as service-shape inspiration only |
| feed and collection inspection UX | WebToEpub | adapt | use for wizard and anthology inspiration, not runtime packaging |
| Patreon-specific fetch semantics and attachment awareness | `patreon-dl` | adapt | replicate behavior in native Go code where practical |

## Core Abstractions

### Provider

A source-system implementation such as Patreon in v1.

### Auth Profile

A provider-specific auth bootstrap definition and session persistence policy.

### Source

An authenticated input feed.

Examples:

- Patreon creator posts feed
- Patreon collection URL
- future story-site source page

### Story Track

A logical serial or work inside a source.

Examples:

- one serial under Plum Parrot tagged `AA3`
- another serial under Plum Parrot tagged `AO2`
- one Actus series inferred from release-title prefix

### Release

One upstream item discovered from a source.

Examples:

- a chapter post
- an attached EPUB release
- an announcement post
- a preview bundle

### Artifact

One concrete output derived from a release.

Examples:

- rendered EPUB from normalized text
- attached EPUB
- attached PDF
- HTML snapshot for debugging

### Publish Target

A sink that receives canonical artifacts.

Examples:

- local filesystem export
- shell hook publisher
- custom automation target built on top of the shell hook

### Run Record

A persisted record of one CLI or daemon-initiated operation, including timestamps, outcome, and summary counts.

### Event Record

A persisted structured event attached to a run, used for logging, auditing, and support.

## High-Level Architecture

Use a ports-and-adapters design.

### Core packages

- `internal/domain`
  - provider-agnostic models such as source, story track, release, artifact, run, and event
- `internal/app`
  - sync orchestration, planning, rule evaluation, publishing workflows
- `internal/config`
  - XDG config loading, validation, defaults, CLI overrides
- `internal/auth`
  - credential sources, session bootstrap, session persistence, challenge state
- `internal/provider`
  - provider interfaces and capability contracts
- `internal/provider/patreon`
  - Patreon-specific auth, discovery, fetch, and normalization
- `internal/classify`
  - story-track assignment and release-role detection
- `internal/artifact`
  - rendering, attachment selection, hashing, and storage
- `internal/publish`
  - filesystem and exec-hook publishers
- `internal/observe`
  - structured logging, run records, event records, support bundles
- `internal/store`
  - interfaces for repositories and leases
- `internal/store/sqlite`
  - SQLite implementation
- `internal/runtime`
  - scheduler, locks, health, and dry-run reporting

### Binary entrypoint

- `cmd/serial-sync`

Subcommands should be built over the same application services, not separate logic paths.

## Future Provider Extensibility

V1 is Patreon-only, but the domain model should already be provider-shaped rather than Patreon-shaped.

That means:

- `Source`, `Release`, `Artifact`, `StoryTrack`, `PublishRecord`, `RunRecord`, and `EventRecord` stay provider-agnostic
- provider-specific fetch and normalization logic lives behind interfaces
- provider auth bootstrap and session persistence are implementation details, not domain concepts
- the rule engine consumes normalized releases, not raw provider responses

### Provider contract

Additional providers should be addable by implementing a small set of capabilities rather than changing the application core.

At minimum, a provider implementation should supply:

- auth bootstrap
- session loading and persistence
- source validation
- release enumeration
- release fetch
- normalization into the common release shape
- optional provider-specific enrichment

Not every future provider needs the same auth path or release shape, but they should all normalize into the same core domain objects before classification and publishing.

### What should stay generic now

- store interfaces
- publisher interfaces
- artifact lifecycle and hashing
- rule evaluation
- run and event recording
- CLI planning and dry-run output

### What can remain Patreon-specific in v1

- the only built-in provider implementation
- username/password bootstrap details
- Patreon release-role heuristics and enrichment defaults

## Run Modes

### One-shot CLI

Used manually or from cron.

Examples:

- `serial-sync sync`
- `serial-sync sync --source plum-parrot`
- `serial-sync publish --target local-files`
- `serial-sync inspect release 154707033`
- `serial-sync inspect run <run-id>`
- `serial-sync plan sync --source actus`

### Daemon Mode

Long-running, low-footprint background worker.

Responsibilities:

- schedule sync jobs
- enforce source-level leases
- emit health and metrics
- optionally expose a minimal local HTTP status endpoint

This mode should remain optional. Cron plus one-shot commands should remain first-class.

### Config Wizard

Optional bootstrap helper for local use.

Responsibilities:

- generate initial config under XDG paths
- bootstrap auth
- sample followed creators or sources
- sample recent releases
- suggest story-track rules
- suggest content strategies per track

This is not required for server operation. It is a convenience layer for initial setup and rule suggestion.

## Auth and Session Model

Auth is the hardest product boundary and should be modeled explicitly.

### Principles

- Do not require users to manually export cookies as the primary path.
- Do not assume every deployment has a local browser.
- Do not assume fully automated challenge solving is always possible.
- Separate credential input from session persistence.
- Do not make Patreon’s auth shape the global assumption for all future providers.

### Supported auth inputs

#### Username and password

Required in v1 for the Patreon provider.

This is the default bootstrap path for the first provider. The application should use username and password to establish an authenticated session, persist that session state, and reuse it on later runs.

This can use a headless browser automation path, but it must fail clearly when the provider presents a challenge the configured capabilities cannot satisfy.

#### Session bundle

Optional in v1.

The system should support importing a durable session bundle generated elsewhere as an operator convenience path, but it is not the primary required auth mode.

#### Pluggable external auth provider

Reserved for later.

Examples:

- browser automation helper process
- secret-manager-backed session provider
- remote auth broker
- future OAuth helper for another provider

### V1 auth contract

The first shippable slice should treat `username_password` as the required auth path for Patreon.

For v1:

- username and password bootstrap is required
- persisted session reuse is required
- session bundle import is optional
- server-side session refresh is best-effort, not a readiness gate
- TOTP and other interactive challenge providers may be deferred if they materially delay the first slice

The runtime auth state machine should be explicit:

- `authenticated`
- `expired`
- `reauth_required`
- `challenge_required`

The application must surface those states clearly in CLI output, run records, and stored event history.

### Challenge providers

Support optional providers for:

- TOTP
- email verification code retrieval
- manual one-time approval via temporary local or tunneled UI

The application must not pretend username and password alone will always be sufficient.

### Session persistence

Persist session state separately from the main config.

By default:

- config in XDG config home
- sessions in XDG state home
- cacheable fetch data in XDG cache home

## XDG Layout

By default:

- config: `${XDG_CONFIG_HOME:-~/.config}/serial-sync/`
- state: `${XDG_STATE_HOME:-~/.local/state}/serial-sync/`
- cache: `${XDG_CACHE_HOME:-~/.cache}/serial-sync/`
- runtime: `${XDG_RUNTIME_DIR}/serial-sync/` when available

Suggested files and directories:

- `config.toml`
- `sessions/`
- `artifacts/`
- `logs/`
- `support/`

CLI flags should be able to override all of these roots.

## Declarative Configuration

Do not make rule creation primarily imperative.

The CLI should help users inspect and validate configuration, but the source of truth should be one declarative config file.

### Single config file

Use one `config.toml` containing:

- runtime settings
- auth profiles
- source definitions
- story-track rules
- publisher definitions
- observability settings

The single-file requirement matters because users will already be juggling auth, source rules, and deployment. Splitting that across multiple files makes the operator model worse.

That does not mean every secret or session blob lives inline in TOML. The single config file is the declarative control plane. It may reference:

- session bundle files under XDG state
- environment-backed secrets
- external auth providers later

## Story-Track Classification

This is the missing abstraction and should be explicit.

The system should classify each release into a story track using a prioritized rule stack.

### Rule types

- collection match
- tag match
- title regex match
- attachment filename regex match
- normalized metadata field match
- fallback default rule

### Rule outputs

Each rule should be able to set:

- `track_key`
- `track_name`
- `release_role`
- `content_strategy`
- `attachment_glob`
- `attachment_priority`
- `anthology_mode`

### Release roles

At minimum:

- `chapter`
- `release_attachment`
- `announcement`
- `schedule`
- `preview_bundle`
- `unknown`

This is more useful than a single `series_key` because it explains both grouping and how to consume the release.

### Content strategies

Each story track should support at least:

- `text_post`
- `attachment_preferred`
- `attachment_only`
- `text_plus_attachments`
- `manual`

These strategies determine which artifact becomes canonical.

### Anthology mode

Anthology mode should remain an output policy, not the identity model.

That means:

- story tracks still exist even when anthology mode is enabled
- anthology mode changes publication behavior
- a collection or feed can publish as one rolling anthology EPUB if desired
- the same underlying releases can still exist as individual release records

This keeps the domain clean while still supporting chapter-list-oriented sources.

## Data Model

The application layer should depend on interfaces, not on SQLite types.

### `Source`

- `ID`
- `Provider`
- `SourceURL`
- `SourceType`
- `CreatorID`
- `CreatorName`
- `AuthProfileID`
- `Enabled`
- `SyncCursor`
- `LastSyncedAt`

### `TrackRule`

- `ID`
- `SourceID`
- `Priority`
- `MatchType`
- `MatchValue`
- `TrackKey`
- `TrackName`
- `ReleaseRole`
- `ContentStrategy`
- `AttachmentGlob`
- `AttachmentPriority`
- `AnthologyMode`
- `Enabled`

### `StoryTrack`

- `ID`
- `SourceID`
- `TrackKey`
- `TrackName`
- `CanonicalAuthor`
- `SeriesMeta`
- `OutputPolicy`

### `Release`

- `ID`
- `SourceID`
- `ProviderReleaseID`
- `URL`
- `Title`
- `PublishedAt`
- `EditedAt`
- `PostType`
- `VisibilityState`
- `NormalizedPayloadRef`
- `RawPayloadRef`
- `ContentHash`
- `DiscoveredAt`

### `ReleaseAssignment`

- `ReleaseID`
- `TrackID`
- `RuleID`
- `ReleaseRole`
- `Confidence`

### `Artifact`

- `ID`
- `ReleaseID`
- `TrackID`
- `ArtifactKind`
- `IsCanonical`
- `Filename`
- `MIMEType`
- `SHA256`
- `StorageRef`
- `BuiltAt`

### `PublishRecord`

- `ID`
- `ArtifactID`
- `TargetID`
- `TargetKind`
- `TargetRef`
- `PublishHash`
- `PublishedAt`
- `Status`

### `RunRecord`

- `ID`
- `Command`
- `StartedAt`
- `FinishedAt`
- `Status`
- `Summary`
- `SourceScope`

### `EventRecord`

- `ID`
- `RunID`
- `Timestamp`
- `Level`
- `Component`
- `Message`
- `EntityKind`
- `EntityID`
- `PayloadRef`

## Storage Abstraction

Define store interfaces in the application layer.

Examples:

- `SourceStore`
- `TrackStore`
- `ReleaseStore`
- `ArtifactStore`
- `PublishStore`
- `RunStore`
- `EventStore`
- `LeaseStore`

### Initial backend

SQLite should be the first implementation because it is easy to ship and good for local servers.

### Deferred backends

- Postgres
- hosted Postgres variants such as Neon or Supabase
- DynamoDB or other cloud KV or document stores

The domain and orchestration layers should not care which backend is used.

## Idempotency Model

Idempotency should be based on durable keys and hashes.

### Discovery idempotency

Key on:

- provider release ID
- source ID

### Content idempotency

Key on:

- normalized content hash
- selected canonical artifact hash

### Publish idempotency

Key on:

- target
- artifact hash
- publish mode

This should make repeated runs cheap no-ops when nothing changed.

## Artifact Storage

Store artifacts outside the relational store by default.

The relational store should keep metadata and references.

Possible storage backends:

- local filesystem
- object storage later

This avoids bloating database rows with binary blobs while still allowing portable local deployments.

## Pipeline Semantics

`sync` and `publish` must be distinct.

### `sync`

`sync` is responsible for:

- discovery
- normalization
- story-track assignment
- canonical artifact selection
- artifact materialization
- persistence of releases, assignments, artifacts, runs, and events

`sync` does not imply publish by default.

### `publish`

`publish` is responsible for replayable downstream side effects from stored canonical artifacts.

That includes:

- filesystem export
- exec-hook invocation

`publish` should be able to run after an earlier `sync` without refetching source content.

### Crash and retry semantics

The system should persist enough state to recover cleanly from:

- crash after discovery but before artifact materialization
- crash after artifact materialization but before publish record persistence
- crash during an external hook

At minimum, artifacts and publish attempts should have explicit lifecycle states such as:

- `planned`
- `materialized`
- `publishing`
- `published`
- `failed`

This state machine is what makes the idempotency model real instead of aspirational.

## Publisher Model

Publishing should not be Calibre-specific in the application core.

The default built-in publishers should be:

- `filesystem`
- `exec`

### Filesystem publisher

Writes canonical artifacts and metadata sidecars to a configured directory tree.

This should be the default and the first built-in target.

### Exec publisher

Runs a user-provided command or shell script after a publishable artifact is materialized locally.

This is the customization escape hatch and should be treated as first-class, not as a hack.

The exec publisher should receive stable environment variables such as:

- `SERIAL_SYNC_SOURCE_ID`
- `SERIAL_SYNC_SOURCE_URL`
- `SERIAL_SYNC_TRACK_KEY`
- `SERIAL_SYNC_TRACK_NAME`
- `SERIAL_SYNC_RELEASE_ID`
- `SERIAL_SYNC_RELEASE_URL`
- `SERIAL_SYNC_RELEASE_TITLE`
- `SERIAL_SYNC_RELEASE_ROLE`
- `SERIAL_SYNC_ARTIFACT_KIND`
- `SERIAL_SYNC_ARTIFACT_MIME`
- `SERIAL_SYNC_ARTIFACT_FILENAME`
- `SERIAL_SYNC_ARTIFACT_PATH`
- `SERIAL_SYNC_METADATA_JSON_PATH`
- `SERIAL_SYNC_NORMALIZED_JSON_PATH`
- `SERIAL_SYNC_RAW_JSON_PATH`
- `SERIAL_SYNC_RUN_ID`

The command may also receive a structured JSON payload on stdin later, but env vars plus file paths are sufficient for v1.

This lets users wire:

- Calibre ingest or `calibredb`
- custom renaming and filing logic
- uploads to object storage
- webhook calls
- arbitrary downstream automation

The application should only consider the publish successful when the hook exits successfully.

The exec publisher is part of the product design, but it is not required for the first shippable vertical slice. The first slice only needs the contract to be specified cleanly so it can land without reshaping the rest of the system.

## Observability and Debuggability

Debuggability is a first-class product requirement.

### Principles

- Every run should be explainable after the fact.
- Successes should be logged, not just failures.
- Operators should be able to export a support bundle without digging through internal files manually.
- Secrets must never be logged in cleartext.

### Required capabilities

- structured logs with stable fields
- human-readable logs for local use
- a unique run ID for every `sync`, `publish`, `plan`, or daemon-triggered job
- persisted `RunRecord` and `EventRecord` entries
- enough event detail to explain classification, artifact selection, and publish decisions
- inspect commands for sources, releases, artifacts, publish records, and runs
- a support-bundle export command that includes redacted config, logs, run summaries, normalized payloads, and relevant raw payload references

### Minimum logging expectations

For each run, the system should record:

- start and end times
- selected source scope
- count of discovered releases
- count of changed releases
- count of materialized artifacts
- count of published artifacts
- explicit reasons for skipped or unmatched releases
- auth-state transitions
- publisher outcomes

## CLI Design

The CLI should be declarative-config first, inspection-heavy, and safe by default.

### Core commands

- `serial-sync init`
- `serial-sync wizard`
- `serial-sync config validate`
- `serial-sync source list`
- `serial-sync source inspect <source>`
- `serial-sync track inspect <track>`
- `serial-sync release inspect <release>`
- `serial-sync artifact inspect <artifact>`
- `serial-sync run inspect <run-id>`
- `serial-sync support bundle <run-id>`
- `serial-sync plan sync`
- `serial-sync sync`
- `serial-sync publish`
- `serial-sync daemon`

### Dry-run primitives

Dry-run should exist across planning, sync, and publish flows.

Examples:

- `serial-sync plan sync`
- `serial-sync sync --dry-run`
- `serial-sync publish --dry-run`

Dry-run output should show:

- discovered new releases
- changed releases
- selected tracks
- selected canonical artifacts
- planned publish actions

## Suggested Config Shape

Example only:

```toml
[runtime]
log_level = "info"
log_format = "json"
store_driver = "sqlite"
store_dsn = "file:${XDG_STATE_HOME}/serial-sync/state.db"
artifact_root = "${XDG_STATE_HOME}/serial-sync/artifacts"

[scheduler]
mode = "cron"
poll_interval = "30m"

[[auth_profiles]]
id = "patreon-default"
provider = "patreon"
mode = "username_password"
username_env = "SERIAL_SYNC_PATREON_USERNAME"
password_env = "SERIAL_SYNC_PATREON_PASSWORD"
session_path = "${XDG_STATE_HOME}/serial-sync/sessions/patreon-default.json"

[[publishers]]
id = "local-files"
kind = "filesystem"
path = "/srv/serial-sync/published"
enabled = true

[[publishers]]
id = "post-publish-hook"
kind = "exec"
command = ["/usr/local/bin/serial-sync-publish-hook"]
enabled = false

[[sources]]
id = "plum-parrot"
provider = "patreon"
url = "https://www.patreon.com/cw/plum_parrot/posts"
auth_profile = "patreon-default"
enabled = true

[[sources]]
id = "actus"
provider = "patreon"
url = "https://www.patreon.com/c/Actus/posts"
auth_profile = "patreon-default"
enabled = true

[[rules]]
source = "plum-parrot"
priority = 10
match_type = "tag"
match_value = "AA3"
track_key = "andy-again-3"
track_name = "Andy, Again 3"
release_role = "chapter"
content_strategy = "attachment_preferred"
attachment_glob = ["*.epub", "*.pdf"]
attachment_priority = ["epub", "pdf"]

[[rules]]
source = "plum-parrot"
priority = 20
match_type = "tag"
match_value = "AO2"
track_key = "aura-overload-2"
track_name = "Aura Overload 2"
release_role = "chapter"
content_strategy = "attachment_preferred"
attachment_glob = ["*.pdf"]
attachment_priority = ["pdf"]

[[rules]]
source = "actus"
priority = 10
match_type = "title_regex"
match_value = "^Nightmare Realm Summoner\\s+-\\s+Chapter\\s+"
track_key = "nightmare-realm-summoner"
track_name = "Nightmare Realm Summoner"
release_role = "chapter"
content_strategy = "text_post"
```

## Deployment Model

### Local server

Primary target.

Examples:

- home server
- NAS with Docker
- mini PC

### Always-on container

Also first-class.

The daemon mode should run comfortably in a small container.

### Cloud environment

Supported later without changing the application model.

Examples:

- always-on VM
- container platform
- scheduled job runner for one-shot sync

## Distribution

Ship at least:

- static Go binaries for common platforms
- Docker image

The Docker image should support:

- one-shot CLI execution
- daemon mode
- mounted XDG config and state directories

## Documentation, Community, and Open Source Posture

This should be easy to use and easy to contribute to.

### Required docs

- top-level README with quickstart
- Docker quickstart
- single-config reference
- tutorial for first source setup
- tutorial for publisher hooks
- troubleshooting guide
- provider-specific notes for Patreon
- architecture overview for contributors

### Community support tooling

- issue templates for bug reports and provider regressions
- instructions for exporting support bundles
- sample configs
- sample hook scripts
- a place for longer-form docs such as a wiki or docs directory

### Open-source posture

Recommend a permissive license, preferably Apache-2.0, to make self-hosting, redistribution, and contribution straightforward while keeping patent terms clear.

The repo should also include:

- `CONTRIBUTING.md`
- `CODE_OF_CONDUCT.md`
- a short provider-contribution guide

## Efficiency Targets

- no full-history refetch on every run
- no republish when canonical artifact hash is unchanged
- bounded per-source polling
- artifact generation only when required
- minimal idle resource use in daemon mode

## Minimal First Vertical Slice

The first functional slice should be:

- one provider: Patreon
- username and password auth bootstrap for the non-challenge case
- persisted session reuse
- creator-feed discovery
- title and tag based story-track assignment
- `text_post` and `attachment_preferred`
- local filesystem artifact store
- filesystem publish target
- one-shot `sync --dry-run`, `sync`, and `publish`
- structured run and event recording
- basic inspect commands
- basic quickstart docs

Do not block the first slice on:

- additional providers
- cloud backends
- Calibre direct import
- fancy wizard flows
- anthology mode
- exec publisher implementation
- session-bundle import
- TOTP and other challenge-provider automation
- a web UI

## Acceptance Gates

The design is ready for implementation when the v1 slice can meet all of these gates:

- A fresh install can run with one `config.toml` under XDG config home plus XDG state and cache directories.
- A one-shot sync can discover at least one Patreon source, normalize releases, assign them to story tracks, and write durable state without duplicate records on a second run.
- `--dry-run` shows planned release, artifact, and publish actions without mutating state.
- `text_post` and `attachment_preferred` both work end to end.
- The default `filesystem` publisher writes artifacts and metadata sidecars deterministically.
- Username and password auth bootstrap works in a headless server environment for the non-challenge case.
- Persisted session reuse works on subsequent runs.
- If the provider presents a challenge that v1 does not handle, the tool fails clearly with `challenge_required` or `reauth_required`.
- The daemon mode does not introduce behavior that cannot also be reached through one-shot sync commands.
- A second sync with no upstream changes is a no-op.
- A changed release republish happens exactly once.
- An unmatched release lands in a deterministic fallback state instead of disappearing silently.
- A crash and restart during publish does not duplicate published artifacts on the next run.
- Every run produces a run ID, persisted run summary, and persisted structured events, including successful actions.
- A support bundle can be exported without exposing secrets.
- The repo includes a README, Docker quickstart, config reference, troubleshooting doc, and contribution docs.

The PRD itself is complete enough when it also answers these review gates:

- The first functional vertical slice is explicit and smaller than the full product.
- The default deployment model is clear for home server, Docker, and cron use.
- The difference between provider, source, story track, release, artifact, publisher, run, and event is unambiguous.
- The prior-art section clearly says what to borrow and what to avoid.
- The publisher model is generic and not Calibre-specific.
- The observability and support story is explicit, not implied.
- The provider-extensibility story is real at the architectural seam, even though v1 only ships Patreon.

## Next Milestones

### Milestone 1

- Go project scaffold
- XDG config loading
- store interfaces
- SQLite store implementation
- provider interfaces
- Patreon provider skeleton
- username and password auth bootstrap
- persisted session reuse
- run and event recording

### Milestone 2

- Patreon source discovery
- normalized release persistence
- rule engine for story tracks
- artifact selection and storage
- dry-run planning output
- inspect commands

### Milestone 3

- filesystem publisher
- cron-friendly one-shot sync
- daemon mode
- structured logs and support-bundle export
- quickstart and troubleshooting docs

### Milestone 4

- exec publisher
- session-bundle import
- optional wizard
- richer hook examples and integrations
- TOTP and other challenge providers
- anthology mode

## Open Questions

- How far should username and password bootstrap go before requiring explicit manual auth assistance?
- Should anthology mode publish one rolling artifact per track or a separate anthology target alongside per-release artifacts?
- Should linked off-provider story URLs be handled inside this tool or delegated to FanFicFare immediately?
- How much release-role inference should be built into defaults versus left to declarative rules?
- When additional providers are added, do we want a formal provider contribution API or just internal package contracts?

## Recommendation

Proceed as a standalone Go sync utility with a generic core and Patreon as the first provider.

Keep FanFicFare as an optional downstream tool, not the host architecture.
Center the design on `source -> story track -> release -> artifact -> publisher`.
Make observability, supportability, and ease of use first-class requirements.
Use a permissive open-source posture from the start.

That is the right abstraction for the content patterns we saw in your Patreon subscriptions and the right operational shape for cron, Docker, and low-cost always-on deployment, while still leaving the door open for other providers later.

### Milestone 2

- rule engine for story tracks
- normalized release persistence
- artifact selection and storage
- dry-run planning output

### Milestone 3

- filesystem publisher
- exec publisher
- cron-friendly one-shot sync
- daemon mode
- structured logs and metrics

### Milestone 4

- session-bundle import
- optional wizard
- richer hook examples and integrations
- TOTP and other challenge providers
- anthology mode

## Open Questions

- How far should username and password bootstrap go before requiring explicit manual auth assistance?
- Should anthology mode publish one rolling artifact per track or a separate anthology target alongside per-release artifacts?
- Should linked off-Patreon story URLs be handled inside this tool or delegated to FanFicFare immediately?
- How much release-role inference should be built into defaults versus left to declarative rules?

## Recommendation

Proceed as a standalone Go sync utility.

Use Patreon as the only provider in v1, but keep the core domain and provider seam generic from the start.
Keep FanFicFare as an optional downstream tool.
Center the design on `source -> story track -> release -> artifact -> publisher`.

Make the built-in publishers generic, with `filesystem` as the default and `exec` as the customization layer.

That is the right abstraction for the content patterns we saw in your Patreon subscriptions and the right operational shape for cron, Docker, and low-cost always-on deployment.
