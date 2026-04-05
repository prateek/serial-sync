# Patreon Notes

The built-in Patreon provider now supports both the fixture demo path and a live `username_password` path.

Implemented:

- creator-feed URL validation
- persisted Patreon session reuse from `session_path`
- isolated Chromium bootstrap in a headed browser session for the non-challenge login case
- creator-feed discovery through Patreon’s web JSON endpoints
- steady-state live syncs that stop after a recent known-id boundary instead of re-walking the full corpus every run
- normalization of title, post type, visibility, tags, collections, attachments, and text content
- lazy attachment caching for live download URLs only when a classified release actually needs the selected attachment
- attachment lookup via local fixture paths for the demo flow

Operational notes:

- live bootstrap uses a dedicated Chromium profile beside the configured `session_path`
- on Linux with no display, the runtime starts Xvfb automatically so containerized bootstrap still uses headed Chrome
- later runs reuse the saved session over plain HTTP until Patreon expires it
- the sync content hash ignores attachment `download_url` and `local_path`, so signed URLs do not force pointless updates

Deferred:

- session-bundle import
- richer challenge providers beyond clear `challenge_required` failures
- richer daemon-specific auth refresh and health helpers
