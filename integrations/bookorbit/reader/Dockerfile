FROM ghcr.io/bookorbit/bookorbit:3.0.0@sha256:571ea47b036a8b371db9c54d54a3cea0fb96245f66c09584efac799afd339716
ENV APP_VERSION=3.0.0-serial-reader.1
LABEL org.opencontainers.image.title="Serial Reader" \
      org.opencontainers.image.version="3.0.0-serial-reader.1" \
      org.opencontainers.image.description="Experimental modified BookOrbit; modified September 21, 2026"
COPY --chown=node:node server-dist/ /app/dist/
COPY --chown=node:node client-dist/ /app/public/
COPY --chown=node:node serial-sync-source.tar.gz /app/public/serial-sync-source.tar.gz
