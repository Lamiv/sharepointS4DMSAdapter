# syntax=docker/dockerfile:1.7
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/adapter ./cmd/adapter && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/graphmock ./cmd/graphmock &&     mkdir -p /out/spool

# Graph emulator (dev / load testing only)
FROM gcr.io/distroless/static-debian12:nonroot AS graphmock
COPY --from=build /out/graphmock /graphmock
EXPOSE 8000
ENTRYPOINT ["/graphmock"]

# Production adapter image (default target)
FROM gcr.io/distroless/static-debian12:nonroot AS adapter
COPY --from=build /out/adapter /adapter
# Disk-backed spool for uploads without Content-Length (mount a volume here).
COPY --from=build --chown=65532:65532 /out/spool /var/spool/adapter
EXPOSE 8080 8090 9090
USER nonroot:nonroot
ENTRYPOINT ["/adapter"]
CMD ["-config", "/etc/adapter/config.yaml"]
