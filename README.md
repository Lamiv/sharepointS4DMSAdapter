# SharePointAdapter

An adapter that lets SAP S/4HANA 2025 Private Cloud store and retrieve images and documents in SharePoint Online through Microsoft Graph. It is a single Go binary in a distroless container, built for a Docker host on Ubuntu.

```
                 ┌──────────────────────── adapter container ────────────────────────┐
 Fiori / apps ──►│ Document REST API :8080 ─┐                                         │
                 │                          ├─► storage service ─► transfer engine ─┐ │
 S/4 KPro/DMS ──►│ Content Server   :8090 ┘   (repos, scope,     (simple/chunked,  │ │──► Microsoft Graph
                 │   (SAP HTTP 4.5 interface)    metadata cache)    memory budget)   │ │    / SharePoint
                 │                                                  Graph client ◄──┘ │
                 │ auth (API key / JWT in, client-credentials out) · observability     │
                 │ admin :9090  /metrics /healthz /readyz                              │
                 └─────────────────────────────────────────────────────────────────────┘
```

Both interfaces sit on the same internal components:

| Component | Package | What it does |
|---|---|---|
| Graph storage client | `internal/graph` | Drive item calls, upload sessions and downloads. Retries 429/5xx and honours `Retry-After` (one back-off for the whole process). Caps concurrent Graph calls. |
| Transfer engine | `internal/transfer` | Uploads ≤ 4 MiB go in one PUT; larger ones use a resumable session with 5 MiB chunks. A process-wide memory budget bounds RAM. Bodies of unknown length spool to disk. Downloads stream with `Range` support. |
| Storage service | `internal/storage` | Maps repositories to SharePoint libraries and folders. Every item must lie inside its repository root. Caches item metadata. |
| Authentication | `internal/auth` | Inbound: API keys (with per-repo read/write/delete) or JWT validated against JWKS. Outbound: Entra ID client credentials with a cached token, refreshed by a single request. |
| Observability | `internal/observability` | JSON logs, request/correlation IDs (also sent to Graph as `client-request-id`), Prometheus metrics, health/readiness, overload protection. |
| Document REST API | `internal/restapi` | Fiori-oriented REST API ([OpenAPI](docs/openapi.yaml)). |
| SAP Content Server interface | `internal/contentrepo` | SAP Content Server HTTP 4.5 interface (pVersion 0045–0047) for OAC0 "HTTP content server": DMS, GOS, ArchiveLink. secKey (PKCS#7) verification, putCert plus certificate activation. See [setup guide](docs/content-server.md). |

## Quick start (no SharePoint needed)

```bash
docker compose -p spadapter-lt -f loadtest/docker-compose.yml up -d --build graphmock adapter
curl -s -H "X-API-Key: loadtest-key" --data-binary @README.md \
  "http://localhost:8080/api/v1/repositories/DMS/objects/BUS2081/5105600001/documents?fileName=readme.md"
curl -s -H "X-API-Key: loadtest-key" http://localhost:8080/api/v1/repositories/DMS/objects/BUS2081/5105600001/documents
```

`graphmock` is a Microsoft Graph emulator for development and load testing. It can add latency and throttling.

## Production deployment (Docker on Ubuntu)

1. **Entra ID app registration.** Use your existing client ID and secret.
   - Recommended: the application permission **`Sites.Selected`**, with admin consent. Then grant the app `write` on each site it may use:
     ```http
     POST https://graph.microsoft.com/v1.0/sites/{site-id}/permissions
     { "roles": ["write"], "grantedToIdentities": [{ "application": { "id": "<client-id>", "displayName": "SAP SharePoint Adapter" } }] }
     ```
   - `Sites.ReadWrite.All` also works, but it grants access to every site in the tenant.
2. **Configure.**
   ```bash
   cp deploy/.env.example deploy/.env                 # GRAPH_TENANT_ID, GRAPH_CLIENT_ID, GRAPH_CLIENT_SECRET, API keys
   cp deploy/config.example.yaml deploy/config.yaml   # repositories -> driveId or siteUrl+driveName, rootPath
   ```
3. **Run.**
   ```bash
   docker compose -f deploy/docker-compose.yml up -d --build
   curl -s localhost:9090/readyz
   ```
4. **TLS.** Terminate TLS at a reverse proxy (nginx, Traefik, SAP Web Dispatcher), or set `server.tlsCertFile` and `server.tlsKeyFile`. S/4HANA RISE reaches the VM through your private connectivity. Import the server certificate chain into STRUST.

### Configuration highlights

| Setting | Default | Notes |
|---|---|---|
| `graph.maxConcurrency` | 64 | Simultaneous Graph calls per instance. Lower it if SharePoint throttles. |
| `graph.metadataCacheTtl` | 60s | Reuses item metadata and the pre-authenticated download URL. A repeat read then costs one SharePoint hop. |
| `transfer.simpleUploadMaxBytes` | 4 MiB | Uploads above this size use resumable sessions. |
| `transfer.chunkSizeBytes` | 5 MiB | Rounded down to a multiple of 320 KiB, as Graph requires. |
| `transfer.memoryBudgetBytes` | 512 MiB | Hard ceiling on transfer buffers. Callers wait instead of the process running out of memory. |
| `transfer.downloadMode` | `proxy` | `redirect` returns a 302 to SharePoint, so content bypasses the adapter. |
| `server.maxInFlight` | 1000 | Requests beyond this get 503 with `Retry-After`. |

Environment overrides: `ADMIN_TOKEN`, `GRAPH_TENANT_ID`, `GRAPH_CLIENT_ID`, `GRAPH_CLIENT_SECRET`, `GRAPH_BASE_URL`, `GRAPH_AUTHORITY_URL`, `GRAPH_MAX_CONCURRENCY`, `METADATA_CACHE_TTL`, `DOWNLOAD_MODE`, `LOG_LEVEL`, `ADAPTER_INTERFACES`. YAML values can also reference `${VAR}`.

## Document REST API

Base path: `/api/v1`. Authenticate with `X-API-Key: <key>`, `Authorization: ApiKey <key>`, or `Authorization: Bearer <JWT>` when `auth.jwt` is configured.

| Method & path | Purpose |
|---|---|
| `GET /repositories` | Repositories visible to the caller |
| `POST /repositories/{repo}/documents?folder=&fileName=&conflict=` | Upload (raw body or multipart) |
| `GET /repositories/{repo}/documents/{id}` | Metadata |
| `GET /repositories/{repo}/documents/{id}/content` | Download (`Range`, `If-None-Match`, `?disposition=inline`, `?mode=redirect`) |
| `PUT /repositories/{repo}/documents/{id}/content` | New version (`If-Match` supported) |
| `DELETE /repositories/{repo}/documents/{id}` | Delete |
| `GET /repositories/{repo}/documents/{id}/thumbnail?size=` | Thumbnail rendered by SharePoint |
| `GET /repositories/{repo}/documents/{id}/versions` | Version history |
| `GET\|POST /repositories/{repo}/objects/{type}/{key}/documents` | Business-object attachments (e.g. `BUS2081/5105600001`) |
| `GET /repositories/{repo}/children?path=` · `GET /lookup?path=` · `POST /folders` · `GET /search?q=` | Browse |

Fiori notes:
- `sap.m.upload.UploadSet` works with either raw PUT/POST plus a `Slug` header, or multipart.
- Set `server.corsOrigins` if the launchpad calls the adapter directly. Otherwise route through the Web Dispatcher or an ABAP proxy.
- Image lists can use `/thumbnail`. The `ETag` on content is the SharePoint `cTag`, so browsers revalidate with a 304.

## Observability

- `GET :9090/metrics`. Key series:
  - `adapter_http_request_duration_seconds`
  - `adapter_graph_requests_total{op,code}`
  - `adapter_graph_retries_total{reason}`
  - `adapter_graph_concurrency_wait_seconds`
  - `adapter_transfer_buffer_wait_seconds`
  - `adapter_transfers_active`
  - `adapter_bytes_total`
  - `adapter_http_rejected_total`
- `GET :9090/healthz` checks liveness. `GET :9090/readyz` checks Graph token and drive reachability every 30 s.
- Each request logs one JSON line with `request_id`. The ID comes from `X-Request-ID` or `X-CorrelationID`, or is generated. It is returned to the caller and forwarded to Graph as `client-request-id`, so Microsoft support can trace it.

## Testing

```bash
# unit + integration tests (full stack against the in-process Graph emulator), race detector on
docker run --rm -v "$PWD:/src" -w /src golang:1.25 go test -race ./...

# load test at 10/25/50/100 concurrent clients -> loadtest/results/REPORT.md
./loadtest/run.sh
METADATA_CACHE_TTL=0s OUT=results/cache-off ./loadtest/run.sh   # worst case: no metadata cache
SCENARIO=contentserver.js OUT=results/contentserver ./loadtest/run.sh   # SAP Content Server interface
```

Results: [docs/load-test-results.md](docs/load-test-results.md).

## SAP Content Server interface

S/4HANA stores documents through OAC0 storage type **HTTP content server**, pointed at `http(s)://<host>:8090/ContentServer/ContentServer.dll`. The implementation follows SAP's *Content Server HTTP 4.5 Interface* documentation for ABAP Platform 2025, and accepts `pVersion` 0045, 0046 and 0047. The [setup guide](docs/content-server.md) covers:
- OAC0, OACT and OAC3 steps
- certificate activation (`/adapter certs activate Z1 CN=<SID>`)
- the signature modes
- the SharePoint storage layout
- command support (all commands except `attrSearch` and `getCert`)
- the points to confirm on the real system

CMIS was ruled out because SharePoint Online has no CMIS endpoint. The background is in [docs/content-repository-interface-research.md](docs/content-repository-interface-research.md).
