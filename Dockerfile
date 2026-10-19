# syntax=docker/dockerfile:1.7
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
# The server binary is "zaks-drive". "adapter" is its name before the rename and stays
# available (as a second copy; COPY follows symlinks) so existing compose files, health checks
# and scripts keep working. It will be removed in a future minor release (see CHANGELOG).
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/zaks-drive ./cmd/adapter && \
    ln -s zaks-drive /out/adapter && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/graphmock ./cmd/graphmock && \
    mkdir -p /out/spool

# Graph emulator (development and load testing only)
FROM gcr.io/distroless/static-debian12:nonroot AS graphmock
COPY --from=build /out/graphmock /graphmock
EXPOSE 8000
ENTRYPOINT ["/graphmock"]

# ZAKS Drive (default target)
FROM gcr.io/distroless/static-debian12:nonroot AS adapter
ARG VERSION=dev
LABEL org.opencontainers.image.title="ZAKS Drive" \
      org.opencontainers.image.description="Self-hosted content gateway connecting SAP S/4HANA and enterprise applications to SharePoint Online and OneDrive for Business." \
      org.opencontainers.image.source="https://github.com/Lamiv/zaks-drive" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/zaks-drive /out/adapter /
# Disk-backed spool for uploads without Content-Length (mount a volume here).
COPY --from=build --chown=65532:65532 /out/spool /var/spool/adapter
EXPOSE 8080 8090 9090
USER nonroot:nonroot
ENTRYPOINT ["/zaks-drive"]
CMD ["-config", "/etc/adapter/config.yaml"]
