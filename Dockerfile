FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/serial-sync ./cmd/serial-sync

FROM debian:bookworm-slim
ENV DEBIAN_FRONTEND=noninteractive \
    SERIAL_SYNC_CONTAINER=1
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    chromium \
    fonts-liberation \
    tini \
    xvfb \
  && rm -rf /var/lib/apt/lists/* \
  && mkdir -p /config /state
WORKDIR /work
COPY --from=build /out/serial-sync /usr/local/bin/serial-sync
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/serial-sync"]
