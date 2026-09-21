# Existing Solutions Research

What is already out there, and what is genuinely missing, for the problem in `serial-sync-prd.md`: pull a patron's own paid Patreon feeds into series-grouped, ordered books on their Calibre-driven devices, unattended, starting from username + password.

The PRD's Prior Art section ([[serial-sync-prd.md#prior-art]](serial-sync-prd.md#/L118-L199)) names five tools. This note re-checks each against the tool's own source as of this research date, plus the Patreon access surfaces those tools actually use.

## Tool overview

| Tool | Primary surface it talks to | Auth | Post text | Attachments | Collections | Calibre sink | Headless from creds? |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `gallery-dl` (Patreon extractor) | `www.patreon.com/api/*` (web JSON) | `session_id` cookie | Not emitted by default | Yes (media) | Yes (`/api/posts?filter[collection_id]=…`) | No | No — needs a real browser session to obtain the cookie |
| `patreon-dl` | `www.patreon.com` HTML + `__NEXT_DATA__` | `--cookie` string | Not a first-class output | Yes (files, images, video) | Yes (collection URL) | No | No |
| `WebToEpub` (Patreon parser) | Logged-in tab → post detail `__NEXT_DATA__` | Browser user session | Yes (innerHTML into EPUB) | Images inlined; files not handled | One collection/creator feed → one EPUB | No (writes EPUB to disk) | No — requires an interactive Chrome extension |
| `PatreonDownloader` (AlexCSDev) | `www.patreon.com` logged-in session (Chromium-driven) + `api/badges` | Browser session on `patreon.com/login` (cookie) | Yes (saves HTML of posts) | Yes (files, attachments, external links) | Not documented in README | No | No — requires opening a logged-in browser session |
| `FanFicFare` | HTML story-site adapters, no Patreon | None (public sites) | Sites it supports | Links in the site format | Site-level "story" = one book | Bundled in-Calibre GUI plugin + AutomatedFanfic (headless) | Not applicable — no Patreon support |
| `AutomatedFanfic` | FanFicFare subprocess + Calibre library | Delegates to FanFicFare | Via FanFicFare | Via FanFicFare | Per-story; not per-creator | Yes (Calibre library is the sink) | Yes for supported sites, but no Patreon at all |

## Patreon access surfaces

### Official API (api.patreon.com)
- Patreon publishes API docs at [docs.patreon.com](https://docs.patreon.com/). The docs cover OAuth2 client registration, OAuth redirects, token refresh, error model, and client and edge rate-limit tiers.
- The authenticated-endpoint base visible in the doc nav is `https://api.patreon.com/oauth2/v2/...` (identity, campaigns, pledges, posts, products, benefits).
- The two grant flows documented there are **client credentials** (creator's own app acting on their campaign) and **authorization-code / implicit-style OAuth for users**. There is no password grant a patron can call with just a username and password, and no public "list my memberships and their posts" endpoint for arbitrary patrons in the docs I read this session.
- Practical consequence: a self-hosted tool that starts from only `PATREON_USERNAME` + `PATREON_PASSWORD` cannot call the official API directly. A human-authorizing OAuth app is required the first time, and that authorization flow itself cannot complete on a headless box that has not already been through a browser.
- Rate limits: the docs split them into *client and token* limits (per app, per token) and *edge* limits (per user/IP). Exact numbers were not captured in this session; a production tool should treat every call as rate-limited and not page through a full creator backlog in one burst.

### First-party web JSON endpoints
- The same data the Patreon web app uses (`/api/posts`, `/api/v2/posts`, the collection pages' embedded `__NEXT_DATA__` JSON, `filter[campaign_id]`, `filter[collection_id]`, `filter[contains_exclusive_posts]=true`, `filter[is_draft]=false`) is what all four Patreon-aware tools in this survey actually call.
- `gallery-dl` builds these URLs byte-for-byte in `gallery_dl/extractor/patreon.py` (`_build_url` around line 266, creator/collection extraction around lines 380–462). It checks `cookies_check(("session_id",), subdomains=True)` in `PatreonExtractor` (line 29), i.e. it wants the logged-in `session_id` cookie and nothing else.
- `WebToEpub`'s `PatreonParser.js` runs XHR from inside the user's logged-in tab and reads `script#__NEXT_DATA__` out of the post detail page (`plugin/js/parsers/PatreonParser.js` around line 81).
- `patreon-dl`'s README documents `--cookie` as the way to get patron-only content (`README.md`, line 10 and line 114 of the CLI reference). The downloader does the rest.
- Because these endpoints are not a promised API, breakage shows up as tool issues, not as Patreon announcements. The maintenance pattern is: Patreon ships a frontend change, the endpoint shape shifts, the tool repo gets a fix within days. Any self-hosted consumer should expect a patch cycle, not zero maintenance.
- Cloudflare-style challenges can interpose on first-page loads and on headless clients. Evidence in the surveyed tools: `patreon-dl`'s `src/downloaders/InitialData.ts` (line 57) detects a Cloudflare challenge redirect at `/login-sync-domains` and falls back to Puppeteer; `gallery-dl`'s `gallery_dl/util.py` (line 342–352) classifies a `cloudflare` server as "Cloudflare challenge"; `PatreonDownloader`'s README (line 25) documents that Cloudflare protection rejects connections below TLS 1.3. The practical counter is a real browser profile persisted once on a headed machine and then replayed. This is the same approach `serial-sync` itself takes (see `docs/patreon.md`, "isolated browser bootstrap … including longer waits for interactive challenge gates" and "bundled noVNC auth wrapper").

### Auth options usable from a headless box
- **Official API OAuth**: requires a browser and human action at least once per app-per-user pairing. Not doable from username+password alone.
- **Web `session_id` cookie**: what all four Patreon tools above actually use. Bootstrapping requires a real browser session once; the cookie can then be replayed from a headless machine until it expires or Patreon rotates it. `patreon-dl`'s [`--cookie`](https://github.com/patrickkfkan/patreon-dl#readme) flag is the minimal contract for this.
- **TOTP-authenticated login**: Patreon does ask for 2FA for some accounts. A username+password-only bootstrap silently fails on those accounts; `docs/patreon.md` notes serial-sync handles this with an optional `totp_secret_env`.
- **Browser-automation bootstrap with session persistence**: `serial-sync`'s noVNC + headed-Chromium + Xvfb flow (see `docs/patreon.md` "Operational notes") is one concrete instance; nothing else in the surveyed tools implements this end-to-end.

## Existing tools in detail

### `gallery-dl` — Patreon extractor
- Repo: https://github.com/mikf/gallery-dl, GPL (license file at repo root), latest release `1.32.12` on 2026-09-12 (`git log -1` on the cloned checkout in `/tmp/gallery-dl`).
- Extractor: `gallery_dl/extractor/patreon.py`.
  - Creator extractor pattern: `patreon.com/c/USER` and `patreon.com/USER`, around line 407.
  - Collection extractor: `patreon.com/collection/<id>` with `filter[collection_id]` and `filter[contains_exclusive_posts]=true` in the posts query (around lines 361–395).
  - User "following" extractor: `patreon.com/home` with `filter[is_following]=true` (around line 482).
  - Query parameter pass-through: arbitrary `filters[...]` query strings on a URL are forwarded to the posts endpoint (line 457 `_get_filters`).
  - `order-posts` config key flips timestamp sort direction (line 308, 389).
- Post text: the extractor pulls the full post JSON, but the shipped writer outputs media (images, video, files). Post body text is not a first-class output in the extractor.
- Calibre / reader sink: none. Output is a directory tree of files.
- Unattended from username + password: **no**. The extractor does not log in. It requires a pre-existing `session_id` cookie (line 29 check).
- Licensing: GPL. License file at `gallery-dl/LICENSE` in the repo.

### `patreon-dl`
- Repo: https://github.com/patrickkfkan/patreon-dl, MIT (repo `license`), latest release `v3.9.0` on 2026-05-16.
- Auth: README line 10 states "Access to patron-only content through cookie. This refers to content you have access to under your account. It does not include locked content that you don't have a subscription for." The CLI exposes `--cookie <string>` (`README.md` line 114, also `src/downloaders/Downloader.ts` line 310 accepts `cookie` in options).
- What it fetches: attachments and media from posts (files, images, videos including DRM-protected YouTube-embedded streams, per README line 39). Post body text is not surfaced as a first-class output in the README sections I read.
- Series mapping: one run, one URL (creator feed or collection). The tool does not split a creator's feed into multiple series.
- Calibre / reader sink: none.
- Unattended from username + password: **no**. The README's cookie-obtaining wiki is manual.

### `WebToEpub`
- Repo: https://github.com/dteviot/WebToEpub, GPL-3.0-only (`package.json` `"license": "GPL-3.0-only"`), latest auto-bump `1.0.14.38` on 2026-09-16.
- Model: Chrome extension. `plugin/js/parsers/PatreonParser.js`:
  - Uses XHR from the *user's logged-in tab* to fetch the post detail HTML and parse `script#__NEXT_DATA__` (line 81).
  - Pulls `content.innerHTML` (line 78) and writes it into the EPUB chapter body, i.e. post body text is preserved by construction.
  - Title comes from `h1[data-tag='post-title']` (line 77).
- Series mapping: one feed or collection → one EPUB. This is the tool's core assumption; a creator who runs two series in one feed has to author two separate collection-based definitions and the tool will produce two separate EPUBs.
- Output: EPUB files. No Calibre sink; the user saves the EPUB and drops it in manually or via an external Calibre tool.
- Unattended from username + password: **no**. The whole model is a human's logged-in browser.

### `PatreonDownloader` (AlexCSDev/PatreonDownloader)
- Repo: https://github.com/AlexCSDev/PatreonDownloader, MIT (`LICENSE.md`, "MIT License Copyright (c) 2019-2026 Aleksey Tsutsey & Contributors"), latest commit `f8004ae` on 2026-03-08 ("Fix description not being saved."), banner on the README top: *"Current state of the project: critical fixes only"*.
- Platform: cross-platform .NET CLI tested under Windows and Linux, driven by a Chromium browser (README "Supported features").
- Auth: authenticates against `https://www.patreon.com/login` and validates the session via `https://www.patreon.com/api/badges` (constant `LoginPageAddress` / `LoginCheckAddress` in `PatreonDownloader.Implementation/Models/PatreonDownloaderSettings.cs` lines 52–53); the README states "You need a valid patreon account to download both free and paid content." No username + password login flow is implemented in the source.
- What it fetches (README "Supported features"):
  - Downloader for files from posts and files from attachments
  - Saves the HTML contents of posts
  - Saves metadata of embedded content
  - Saves raw API responses "mostly for troubleshooting purposes" — useful as evidence it talks to the same `api/...` JSON surface as the other tools, not as a first-class output
  - External link extraction with built-in C# plugins (Google Drive, Mega.nz, Dropbox) and direct-link fallback
- Known gaps per README "Known not implemented or not tested features": audio files, embedded Vimeo videos, YouTube and imgur external links, gallery posts.
- Series mapping: not documented in README. One creator/page is the unit of work; the CLI takes a page URL (creator `/posts` or `user?u=#numbers#`) and downloads the whole page tree.
- Calibre / reader sink: not part of the main repo.
- Unattended from username + password: **no**. Auth requires an interactive Chromium session logged in to Patreon; the README gives no path to log in headlessly with only credentials.

### `FanFicFare`
- Repo: https://github.com/JimmXinu/FanFicFare, Apache-2.0 (`LICENSE` / `LICENSE.spdx` in the repo). Latest release `4.61.3` on 2026-09-11.
- Patreon support: **none**. No `adapter_patreon` in `fanficfare/adapters/`. The adapter list covers public story sites (the file `fanficfare/adapters/adapter_royalroadcom.py` is a representative scrape-HTML adapter for a fictional public site).
- Model: one story URL in, one book out. The tool's domain object is a "story," not a creator feed. A creator who runs multiple series on Patreon has no equivalent object in FanFicFare to map to.
- Series handling: within one story, chapter `order` is derived from the source site's chapter list. Across separate stories (i.e. what would be separate series in the Patreon case), the tool has no concept of "read story A before book 2 of story B."
- Calibre / reader-side sink: FanFicFare ships its own in-Calibre GUI plugin (`calibre-plugin/fff_plugin.py`, GPLv3, 2021, Jim Miller) that imports / updates story URLs as books from within Calibre; for headless use the `fanficfare` CLI and `AutomatedFanfic` (below) write directly via `calibredb`.

### `AutomatedFanfic`
- Repo: https://github.com/MrTyton/AutomatedFanfic, latest commit `8673af8` on 2026-09-15 (bump to 3.10.1).
- What it actually is, per README: a Python orchestrator that (a) watches an email inbox for new story additions, (b) invokes FanFicFare to render them, and (c) writes the result into a Calibre library via `calibredb` CLI subprocess calls (`CalibreDBClient` in `root/app/calibre_integration/calibredb_utils.py`).
- Patreon: **not supported.** It does not add any Patreon-specific pipeline. It is strictly "story URL in Calibre and/or in email, book out in Calibre."
- Series ordering: inherited from FanFicFare; no cross-story "book before chapter" ordering.
- Calibre integration: yes, this is the sink. Docker images are the recommended deployment path (README "Execution" section, `docker pull mrtyton/automated-ffdl`); non-Docker requires `calibredb` on the system path.
- Unattended: yes, for the sites it supports — that is exactly why it exists. But it does not apply to Patreon.

## Feed-side and reader-side context

### Royal Road
- There is no official, documented public API for serialized chapters on Royal Road in the primary sources I could reach in this session.
- FanFicFare's `fanficfare/adapters/adapter_royalroadcom.py` scrapes `https://www.royalroad.com/fiction/<id>` HTML directly. No `api.royalroad.com` URL appears in that adapter.
- Community "api.royalroad.com/api/v1/..." endpoints exist and are consumed by some private tools, but that is not a documented contract Royal Road has committed to. Any tool built on top of those endpoints is built on an explicitly unofficial shape.
- Relevance to this problem: multiple sample creators post the same series on Royal Road a few weeks later than on Patreon. Until an official API appears, the practical integration is either (a) scrape the HTML the same way FanFicFare does, or (b) treat Royal Road as a best-effort secondary source and not block serial-sync's Patreon flow on it.

### Calibre and the reader pipeline
- **iPhone / iPad**: Calibre's [Apple iOS app](https://manual.calibre-ebook.com/apps/apple-ios.html) is the canonical route from a Calibre library to iOS devices. The manual at the URL above documents the app. The exact sync mechanisms (Bluetooth, Wi-Fi, USB) I did not enumerate in this session; verify against that page before quoting specific pairing steps.
- **Xteink X4**: vendor documentation for a Calibre-specific sync on this model was not reachable in this session. Treat "Xteink X4 accepts a Calibre library directly" as **not verified** until the vendor manual is checked. The generic MTP-over-USB path for Android-derived e-ink devices is plausible but is not itself a Calibre contract.
- `calibre2kobo`-style share servers are only relevant to Kobo hardware; not applicable to an Xteink X4. Do not build on them for this problem.

## Gap analysis

Measured against the "solved" bar from the PRD and the hard constraints (unattended headless, username + password start, self-hostable):

### 1. Can a pledge-paying patron pull their paid posts + attachments programmatically today?

Yes, via the first-party web JSON endpoints, with a persisted `session_id` cookie. Every tool in the survey that gets Patreon patron content does exactly this. The official API does not give a patron a "list my memberships and their posts" endpoint in the docs I read; the cookie route is the only practical path for a non-creator caller.

Breakage risk is real and should be planned for:
- Patreon ships frontend changes without deprecating the JSON shape first.
- Cloudflare-style challenges interpose on cold starts and on headless user agents; the workarounds in the wild are all "use a real browser profile once, replay the cookie."
- Exact rate-limit numbers were not captured in this session; treat every endpoint as throttled and do full-corpus re-walks sparingly.

### 2. Does any existing tool do the seven things in the PRD?

| Requirement | Today's answer |
| --- | --- |
| (a) Map one creator's mixed feed into multiple series | **No tool surveyed does this.** WebToEpub can author two collections → two EPUBs, but the user must curate the collections on Patreon; title-prefix or tag rules are not a feature of any of the five tools. |
| (b) Preserve book-before-chapter ordering across numbered books | **No tool surveyed does this.** FanFicFare's ordering is per-story; for a creator whose "book 1" and "chapter" posts come from the same feed, there is no cross-story ordering. |
| (c) Keep posted notes / body text with the chapter | WebToEpub and PatreonDownloader (HTML) keep post body text. gallery-dl and patreon-dl treat post text as secondary to media. |
| (d) Cheap incremental re-checks (state across runs) | PatreonDownloader keeps per-post state; gallery-dl's archive mode does file-level dedupe; serial-sync itself adds a "recent known-id boundary" stop condition (see `docs/patreon.md`). None of the five tools surveyed does this natively as a first-class, visible state model. |
| (e) Feed Calibre or e-ink readers directly | Only AutomatedFanfic (via Calibre, no Patreon) and any downstream calibre-plugin glue around FanFicFare. Nothing in the surveyed set handles Patreon → Calibre end-to-end. |
| (f) Unattended from username + password including first login | **No tool surveyed does this.** Every Patreon-aware tool in the table requires a human to complete the first browser sign-in. serial-sync's noVNC + headed-Chromium + Xvfb + session-bundle flow (see `docs/patreon.md`) is the only design in the surveyed set that gets to a headless steady state, and it still requires a human (or the noVNC operator) once per challenge. |

### 3. What Patreon's official posture implies for a self-hosted tool

- The official API is **creator-side and app-authorized**. A patron cannot get an "list my paid memberships" call from the documented endpoints. A self-hosted tool has to go through the first-party web session (cookie) for member-only content.
- Cloudflare-style interposition on the web session is a known, recurring behavior. Tools that hard-require a headless HTTP-only flow eventually break; tools that persist a real-browser profile once and replay it, with a noVNC or headed fallback for re-authentication, are what survives in practice.
- Rate limits exist at the client and the edge and are published with tiers. A tool that pages through an entire creator backlog in a single burst will get throttled; the correct shape is (a) one full walk on first run, then (b) an incremental walk that stops at a known-id boundary every subsequent run — which is the exact shape `docs/patreon.md` records serial-sync has implemented.
- Patreon has not announced a public deprecation path for the web JSON endpoints. They are not promised to stay, but they have stayed long enough across many shipped frontend rewrites that a self-hosted tool can depend on them for one release cycle and patch to the next.

### What is genuinely missing
- A creator-feed → multi-series mapper (title-prefix and user-defined tag rules) with a clear precedence.
- Cross-series book/chapter ordering so "book 2 chapter 1" sorts after "book 1 chapter 20."
- State-aware incremental re-checks that survive process restarts.
- A Calibre-library publisher that consumes the renderer's EPUB output and writes it in under the right series/tag slots.
- A first-login path that can complete on a headless box (or that has an honest human-facing "complete once in a browser, import a session bundle" step) — the same shape serial-sync already ships.
None of the five surveyed tools covers all of these together. That is the space serial-sync fills.

## Unverified in this session
- Exact Patreon API rate-limit numbers — the docs pages I fetched did not enumerate them; re-read `https://docs.patreon.com/` "Rate Limits" section before relying on a specific figure.
- Exact Calibre iOS sync mechanism (Bluetooth vs USB vs Wi-Fi) — the Apple iOS manual page exists at `manual.calibre-ebook.com/apps/apple-ios.html`; the specific sync section was not enumerated in this session.
- Xteink X4 Calibre/USB/Wi-Fi support — vendor documentation not reachable in this session.