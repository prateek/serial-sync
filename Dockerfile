FROM golang:1.27-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/serial-sync ./cmd/serial-sync

FROM --platform=$BUILDPLATFORM debian:trixie-slim AS epubcheck
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    default-jdk-headless \
    unzip \
    wget \
  && rm -rf /var/lib/apt/lists/*
COPY scripts/install-epubcheck /src/scripts/install-epubcheck
COPY third_party/epubcheck /src/third_party/epubcheck
RUN BIN_DIR=/out/bin /src/scripts/install-epubcheck /opt/epubcheck

FROM debian:trixie-slim
ARG TARGETARCH
ENV DEBIAN_FRONTEND=noninteractive \
    SERIAL_SYNC_BROWSER_USER=serialsync \
    SERIAL_SYNC_CHROME_NO_SANDBOX=true \
    SERIAL_SYNC_CONTAINER=1
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    chromium \
    chromium-sandbox \
    default-jre-headless \
    fluxbox \
    fonts-liberation \
    gosu \
    gnupg \
    novnc \
    poppler-utils \
    python3 \
    tini \
    unzip \
    websockify \
    wget \
    x11vnc \
    xvfb \
  && install -d -m 0755 /etc/apt/keyrings \
  && if [ "$TARGETARCH" = "amd64" ]; then \
       wget -qO- https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /etc/apt/keyrings/google-chrome.gpg; \
       echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list; \
       apt-get update && apt-get install -y --no-install-recommends google-chrome-stable; \
       apt-get purge -y chromium; \
     fi \
  && rm -rf /var/lib/apt/lists/* \
  && groupadd --gid 10001 serialsync \
  && useradd --uid 10001 --gid 10001 --create-home --home-dir /home/serialsync --shell /bin/bash serialsync \
  && mkdir -p /config /state /work
COPY --from=epubcheck /opt/epubcheck /opt/epubcheck
COPY --from=epubcheck /out/bin/ /usr/local/bin/
WORKDIR /work
COPY --from=build /out/serial-sync /usr/local/bin/serial-sync
COPY scripts/container/google-chrome /usr/local/bin/google-chrome
COPY scripts/container/serial-sync-with-novnc /usr/local/bin/serial-sync-with-novnc
RUN chmod +x /usr/local/bin/google-chrome /usr/local/bin/serial-sync-with-novnc
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/serial-sync"]
