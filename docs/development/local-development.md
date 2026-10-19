# Local development

## Requirements

Go 1.25 or newer, Docker with Compose v2 (for image builds and the emulator stack), `curl`, `git`. Python 3 is needed only for `scripts/check_access.py` and `scripts/check_docs.py`.

## Build and run without Docker

```bash
go build -o bin/zaks-drive ./cmd/adapter
go build -o bin/graphmock ./cmd/graphmock
```

Start the emulator, then point ZAKS Drive at it. Easiest is the Compose stack, which does both:

```bash
docker compose -f loadtest/docker-compose.yml up -d --build graphmock adapter
curl -s localhost:9090/readyz
```

The stack uses [`deploy/config.mock.yaml`](../../deploy/config.mock.yaml) (API key `loadtest-key`, repository `DMS`, SAP repository `Z1` with `signature: none`). Changes to Go code need `up -d --build`.

## The Graph emulator

`internal/graphmock` imitates the parts of Microsoft Graph that ZAKS Drive uses: tokens with `roles`, sites, drives, `/users/{id}/drive`, items, upload sessions, versions, throttling and latency (`MOCK_LATENCY_MS`, `MOCK_THROTTLE_RATE`), and quirks seen on the real service (`$select` dropping the download URL). It is for tests; it is not a Microsoft product and does not prove behaviour on real SharePoint.

## Against a real tenant

Copy `deploy/.env.example` to `deploy/.env` and `deploy/config.example.yaml` to `deploy/config.yaml`, fill in the values ([installation](../installation.md)), and use a test library. Both files are git-ignored. Never paste secrets into issues, commits or logs.

## Repository layout

See [architecture](../architecture.md#components). Error codes are defined in `internal/apperr/catalogue.go`; after changing them regenerate the documentation:

```bash
go run ./cmd/adapter errors > docs/error-codes.md
```

A test fails if `docs/error-codes.md` is out of date.

## Conventions

- Keep API routes, response contracts and configuration keys stable; see [branding and compatibility](branding-and-compatibility.md).
- Every user-facing error uses a catalogue code with a cause and a fix.
- Add tests with changes; see [testing](testing.md). Contribution rules: [CONTRIBUTING](../../.github/CONTRIBUTING.md).
