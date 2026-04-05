FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/serial-sync ./cmd/serial-sync

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/serial-sync /usr/local/bin/serial-sync
ENTRYPOINT ["/usr/local/bin/serial-sync"]
