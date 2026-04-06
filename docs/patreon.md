# Patreon Notes

The built-in Patreon provider now supports both the fixture demo path and a live `username_password` path.

Implemented:

- creator-feed and collection URL validation
- source dumping from active Patreon memberships through `setup dump`
- persisted Patreon session reuse from `session_path`
- session-bundle import validation through `setup auth --import-session`
- isolated browser bootstrap in a headed session, using Google Chrome on `amd64` or Chromium on `arm64`, including longer waits for interactive challenge gates before the login form appears
- optional TOTP-assisted login when `totp_secret_env` is configured
- creator-feed discovery through Patreon’s web JSON endpoints
- collection discovery through authenticated Patreon HTML pages plus post-detail fetches
- steady-state live syncs that stop after a recent known-id boundary instead of re-walking the full corpus every run
- normalization of title, post type, visibility, tags, collections, attachments, and text content
- lazy attachment caching for live download URLs only when a classified release actually needs the selected attachment
- attachment lookup via local fixture paths for the demo flow

Operational notes:

- live bootstrap uses a dedicated browser profile beside the configured `session_path`
- stale Chromium singleton lock files in that dedicated profile are cleared before bootstrap retries
- on Linux with no display, the runtime starts Xvfb automatically so containerized bootstrap still uses headed Chrome
- the bundled Docker auth wrapper can expose that Xvfb-backed browser over noVNC for headless servers
- interactive Cloudflare-style challenges can be completed in that noVNC session or a visible host browser session, then reused from Docker via the saved session bundle
- later runs reuse the saved session over plain HTTP until Patreon expires it
- the sync content hash ignores attachment `download_url` and `local_path`, so signed URLs do not force pointless updates
- `setup dump` defaults to all paid memberships and writes a reusable local workspace for offline rule iteration

Deferred:

- richer non-TOTP challenge providers beyond clear `challenge_required` failures
- richer daemon-specific auth refresh heuristics
