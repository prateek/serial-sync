# Patreon Notes

The built-in Patreon provider currently parses raw Patreon post API payload fixtures.

Implemented:

- creator-feed URL validation
- normalization of title, post type, visibility, tags, collections, attachments, and text content
- attachment lookup via local fixture paths

Deferred:

- live username/password bootstrap
- session reuse against Patreon
- challenge handling
- API crawling beyond local fixtures
