# SharePointAdapter

> **Start here:** follow the [step-by-step setup](#step-by-step-setup) below. SAP is optional; use the REST API as a storage backend for your own document-management application.

A self-hosted document storage adapter for SharePoint Online through Microsoft Graph. Use its REST API for your own document-management tools, or its SAP Content Server interface to connect SAP S/4HANA DMS and attachments. It is a single Go binary in a distroless container, built for a Docker host on Ubuntu.

This repository provides the storage API and SAP integration, not a ready-made DMS web interface. You need a Microsoft 365 organizational tenant with SharePoint Online (or OneDrive for Business); the current client-credentials implementation does not provide personal Microsoft account sign-in for consumer OneDrive. Azure hosting is optional: an Ubuntu server with Docker and suitable connectivity is enough. Microsoft 365 storage and hosting are provisioned separately.

- [Step-by-step setup and required permissions](#step-by-step-setup)
- [Updating an installation](#updating-an-existing-installation)
- [SAP troubleshooting and compatibility](#troubleshooting-from-sap-integration-tests)
- [REST API](#document-rest-api) and [development tests](#testing)

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
| Storage service | `internal/storage` | Maps repositories to SharePoint libraries and folders. Every item must lie inside its repository root. Content server folders are reserved (invisible to REST). Paging cursors are HMAC-bound to repository and folder. Caches item metadata. |
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

## Step-by-step setup

Follow steps 1–6 for the REST API. Continue with steps 7–10 for SAP. Commands below run in Bash on the Docker host, from `~/sharepoint-adapter`, unless marked otherwise. Replace example tenant, site, host and repository values with your own.

### 1. Prepare accounts, authorizations and connectivity

| Area | Required access / preparation |
|---|---|
| Microsoft 365 | An organizational tenant, an available SharePoint site and document library, and someone authorized to manage their access. |
| Entra app registration | Permission to register an application and manage its credentials; tenant policy may require an administrator to create it for you. |
| Graph application consent | An authorized administrator such as **Privileged Role Administrator** must consent to Microsoft Graph application permissions. Application Administrator / Cloud Application Administrator alone do not grant consent to Graph application roles. See [Microsoft's consent requirements](https://learn.microsoft.com/en-us/entra/identity/enterprise-apps/grant-admin-consent). |
| Selected-site grant | With `Sites.Selected`, an authorized administrator must separately grant the app access to the chosen site. The administrative Graph client needs the permissions required to manage site grants; see step 2. |
| Azure subscription | Not required for the adapter when hosted elsewhere. Azure VM/network permissions are needed only if you choose to provision hosting there; subscription Owner/Contributor does not replace Entra consent or SharePoint grants. |
| Docker host | SSH access, Docker Engine with the Compose plugin, Git, curl and OpenSSL; permission to run Docker and configure network/TLS. See [Ubuntu preparation](SETUP.md#4-prepare-the-ubuntu-server). |
| SAP (optional) | Basis access to maintain OAC0, view the signing certificate in STRUST and run SE38/RSCMST; DMS customizing access to OACT/DC10 and test access to CV01N/CV02N/CV03N as applicable. Have Basis assign the appropriate roles; a transaction code alone is not an authorization grant. |

Allow outbound HTTPS from the adapter to Microsoft Entra, Microsoft Graph and your SharePoint/download hosts. Building also needs access to container registries and Go dependencies. SAP application servers must reach the Content Server endpoint; front-end document retrieval may also require SAP GUI/browser clients to reach it. Keep server clocks synchronized for signed SAP URLs.

### 2. Register the Microsoft Entra application and grant access

In the [Entra admin center](https://entra.microsoft.com):

1. Open **App registrations → New registration**. Create a single-tenant application; no redirect URI is needed for client credentials.
2. Record the **Directory (tenant) ID** and **Application (client) ID**.
3. Under **Certificates & secrets**, create a client secret. Save its **Value**, not its Secret ID, and record its expiry.
4. Under **API permissions → Add a permission → Microsoft Graph → Application permissions**, choose the storage permission below.
5. Have an authorized administrator select **Grant admin consent**. Adding a permission without consent is insufficient.

| Storage configuration | Application permission | Access scope |
|---|---|---|
| SharePoint site, recommended | `Sites.Selected` plus a site grant with role `write` | Explicitly granted sites |
| SharePoint site, broader alternative | `Sites.ReadWrite.All` | All site collections in the tenant |
| OneDrive for Business via `userId` | `Files.ReadWrite.All` | Broad file access; use an organizational user's provisioned drive |

These are **application**, not delegated, permissions. The adapter calls Graph as the app; it does not impersonate the SAP user. API keys and SAP signatures control inbound access separately. See the [Graph permissions reference](https://learn.microsoft.com/en-us/graph/permissions-reference).

For `Sites.Selected`, use an authorized administrative Graph client to resolve the site and grant access:

```http
GET https://graph.microsoft.com/v1.0/sites/<tenant>.sharepoint.com:/sites/<site>?$select=id

POST https://graph.microsoft.com/v1.0/sites/<site-id>/permissions
Content-Type: application/json

{
  "roles": ["write"],
  "grantedToIdentities": [{
    "application": {
      "id": "<adapter-client-id>",
      "displayName": "SharePoint Adapter"
    }
  }]
}
```

The caller creating the site grant needs the elevated permissions described in [Microsoft's selected-permissions documentation](https://learn.microsoft.com/en-us/graph/permissions-selected-overview), including `Sites.FullControl.All` for site permission management. This is a setup operation by the administrative client, not a reason to give the runtime adapter full control. `Sites.Selected` alone grants no site access; a `read` site grant cannot upload/delete. Keeping `Sites.ReadWrite.All` alongside `Sites.Selected` still gives broad access.

### 3. Download and create local configuration

```bash
git clone https://github.com/Lamiv/sharepointS4DMSAdapter.git ~/sharepoint-adapter
cd ~/sharepoint-adapter
cp -n deploy/.env.example deploy/.env
cp -n deploy/config.example.yaml deploy/config.yaml
chmod 600 deploy/.env
chmod 644 deploy/config.yaml
```

For an existing installation, keep its configuration and use the [update procedure](#updating-an-existing-installation). Both local configuration files are ignored by Git. Keep secrets in `.env`; `config.yaml` must be readable by the container's non-root user.

Generate three independent values with `openssl rand -hex 32`, then edit `deploy/.env`:

```dotenv
GRAPH_TENANT_ID=<directory-tenant-id>
GRAPH_CLIENT_ID=<application-client-id>
GRAPH_CLIENT_SECRET=<client-secret-value>
SP_DRIVE_ID=
ADAPTER_API_KEY_S4=<generated-read-write-delete-key>
ADAPTER_API_KEY_FIORI=<different-generated-read-only-key>
ADMIN_TOKEN=<different-generated-admin-token>
REST_PORT=8080
CS_PORT=8090
ADMIN_PORT=19090
```

The API key names are defaults from the template; non-SAP applications can use them too. Never publish their values or put a privileged API key in browser-delivered code. `SP_DRIVE_ID` can remain empty when using `siteUrl` and `driveName` below.

This guide uses **host port 19090** for administration to avoid conflicts such as Cockpit on 9090. The container still listens on 9090. Compose binds the admin port to localhost only.

### 4. Configure the library and interfaces

Edit `deploy/config.yaml`. Keep the template's Graph, transfer and authentication sections. Replace its entire `repositories` list with your own, removing the unused `ATTACHMENTS` example:

```yaml
repositories:
  - id: DMS
    siteUrl: https://contoso.sharepoint.com/sites/PersonalDMS
    driveName: Documents
    rootPath: SAP_DMS
```

`driveName` is the library's display name, not necessarily its URL segment. `DMS` is the adapter's repository identifier. For OneDrive for Business, replace `siteUrl` and `driveName` with `userId: user@contoso.com`; see [storage configuration alternatives](SETUP.md#62-settings-deployconfigyaml).

For **REST only**, set `server.interfaces: [rest]` and set `contentServer.repositories: []`. You can skip the SAP steps.

For **SAP plus REST**, keep `server.interfaces: [rest, contentrepo]` and use:

```yaml
contentServer:
  allowedNetworks: []
  clockSkew: 5m
  autoActivateCertificates: false
  repositories:
    - contRep: Z1
      description: SAP DMS originals
      repository: DMS
      folder: ContentServer/Z1
      signature: required
```

`Z1` must match OAC0; `repository: DMS` points to the storage configuration above. SAP documents, metadata and certificates live below `SAP_DMS/ContentServer/Z1`. That folder is reserved from REST access. Use other folders for REST uploads and let the adapter manage SAP's folder contents.

An empty `allowedNetworks` list imposes no source-IP restriction. Configure permitted SAP/client networks or an upstream firewall before exposing the endpoint. For production use HTTPS, either through a reverse proxy or mounted certificates configured with `server.tlsCertFile` / `server.tlsKeyFile`. Match SAP's HTTP/HTTPS settings to that endpoint and import the TLS chain into STRUST. See [network and TLS setup](SETUP.md#8-restrict-network-access).

### 5. Start and verify SharePoint access

```bash
docker compose -f deploy/docker-compose.yml up -d --build adapter
docker compose -f deploy/docker-compose.yml ps
curl -sS http://localhost:19090/readyz
```

Repeat the readiness request until it returns `ready`. If initialization fails, inspect:

```bash
docker compose -f deploy/docker-compose.yml logs --tail=80 adapter
```

`ready` means the Graph/drive readiness check passes. It does **not** prove write/delete permission, SAP certificate activation or completion of SAP tests. Docker's health check checks liveness, not the whole integration.

Run the access checklist:

```bash
docker compose -f deploy/docker-compose.yml exec adapter /adapter check
```

For the writable repository above, expect no FAIL or SKIP results: credentials, token roles, library resolution, small upload/download, upload-session creation, version history and deletion. This command creates/removes test files and cleans up files with its test prefix from previous runs. Session creation is not a completed large-file transfer.

### 6. Test the REST API (also works without SAP)

Using the read/write/delete API key, run:

```bash
read -r -s -p 'Adapter API key: ' API_KEY
printf '\n'
export API_KEY
REST_URL=http://localhost:8080 bash scripts/e2e-smoke.sh
unset API_KEY
```

The script uploads/downloads test data, including a large resumable upload, checks ranges and other operations, and attempts cleanup. It uses the `e2e-test` folder; reserve that folder for tests. Inspect failures and leftover test data before retrying. Set `REST_URL` to your published REST endpoint if it differs.

For a custom DMS frontend, use the [REST API reference](docs/openapi.yaml). The default read-only key only reads repository `DMS`; uploads need write permission. Keep privileged credentials in your backend or configure the supported JWT validation. Enabling the REST interface does not install a Fiori app or automatically configure SAP attachment services.

### 7. Verify the SAP Content Server endpoint

Skip this and subsequent SAP steps for a REST-only installation.

```bash
docker compose -f deploy/docker-compose.yml port adapter 8090
curl -sS 'http://localhost:8090/ContentServer/ContentServer.dll?serverInfo&pVersion=0047'
```

Use the published Content Server port if you changed `CS_PORT`. Expect `serverStatus="running"` and a `contRep="Z1"` entry. Use HTTPS instead when configured. SAP uses this endpoint, **not** the REST port or admin port 19090. Confirm reachability from the SAP application server, not just from the Docker host.

### 8. Create the SAP content repository in OAC0

Have SAP Basis create an unused repository ID matching your configuration:

| OAC0 field | DMS test value |
|---|---|
| Content repository | `Z1` |
| Description | Your description |
| Document Area | `DMS` — Document Management System |
| Storage type | `04` — HTTP content server |
| Version | `0047` |
| HTTP server | Adapter hostname/IP reachable from SAP; no URL scheme or path |
| Port / SSL port | Actual published HTTP port or HTTPS endpoint port |
| HTTP script | `ContentServer/ContentServer.dll` |
| HTTPS frontend/backend | Match the deployed endpoint and client connectivity |
| No signature (if shown) | Leave unchecked for `signature: required` |

Save, then run OAC0's **Connection Test** (identify it by its tooltip; icon positions vary). Check the exact SAP message and adapter logs. For ArchiveLink, use the appropriate document area and OAC3 assignments instead of assuming the DMS configuration applies.

The OAC0 physical/backend path is a SAP application-server path for relevant local operations. It is not the SharePoint storage destination. Do not enter a SharePoint URL there; see the SG813 troubleshooting entry below.

### 9. Send, verify and activate SAP's certificate

In OAC0 for `Z1`, choose **Send certificate**. Then run:

```bash
docker compose -f deploy/docker-compose.yml exec adapter /adapter certs list
```

Expect a record with `contRep: Z1`, an `authId`, `sha256Fingerprint` and `active: false`. The list deliberately omits the certificate bytes (`certificate: null`); that is normal.

Compare the SHA-256 fingerprint with the signing certificate sent by SAP in STRUST. Once verified, use the **exact authId from the list**, quoted as one argument:

```bash
docker compose -f deploy/docker-compose.yml exec adapter /adapter certs activate Z1 '<authId from certs list>'
docker compose -f deploy/docker-compose.yml exec adapter /adapter certs list
```

Expect `active: true`. Do not copy another system's distinguished name. SAP's signing certificate is separate from the HTTPS server certificate. A successful `putCert` upload alone does not activate it.

Immediately after startup, `certs list` can return `404` before certificate routes are registered. Check readiness and Content Server startup, then retry; readiness may become green before Content Server initialization finishes. A persistent 404 needs logs and verification that `contentrepo` is enabled.

### 10. Run SAP tests, then a real DMS document

1. In **SE38**, run **RSCMST** for `Z1`.
2. Execute the relevant HTTP tests and open their detailed results, especially **RSCMSTH2 (docProt)**. A green connection/signature indicator alone does not mean every operation passed.
3. If a test fails, collect its exact operation, expected/actual result and matching adapter log lines. Keep signatures enabled while investigating.
4. In **OACT**, create/assign a DMS storage category pointing to `Z1`; configure the applicable document type in **DC10** with Basis/DMS administrators.
5. In **CV01N**, create a test document with a small original, select the configured storage category and check it in. Reopen/download it through the applicable DMS transaction and compare the content.
6. Test an update/new version, a larger original and authorized deletion using disposable documents. Confirm the actual document opens from the intended SAP GUI/Fiori client network.

For GOS/ArchiveLink, configure the appropriate **OAC3** object/document-type relationships and test that consumer separately. Passing DMS tests does not automatically validate every attachment service.

## Updating an existing installation

Keep `deploy/.env` and `deploy/config.yaml`; do not replace them with templates. Back up these files securely and review local changes before updating:

```bash
cd ~/sharepoint-adapter
git status --short
git pull --ff-only
git log -1 --oneline
docker compose -f deploy/docker-compose.yml up -d --build adapter
curl -sS http://localhost:19090/readyz
```

If Git reports a conflict or cannot fast-forward, resolve that before continuing. Pulling source alone does not update the running binary. Wait for initialization, rerun `adapter check`, then `certs list` and the relevant SAP tests. Certificates are persisted in SharePoint and should survive container recreation.

For configuration or secret changes, use `docker compose -f deploy/docker-compose.yml up -d --force-recreate adapter`. Rotate expiring Entra secrets and keep their values out of Git and issue reports.

## Troubleshooting from SAP integration tests

| Symptom | Meaning and next action |
|---|---|
| `ready`, but an upload fails | Readiness is narrower than write access. Run `/adapter check`; inspect its first failure and the request log. |
| `certs list` returns `404` just after rebuild | Admin starts before certificate routes. Wait for Content Server initialization and retry; check `server.interfaces` / `ADAPTER_INTERFACES` if persistent. |
| `certs list` shows `"value": []` | Inspect logs while sending from OAC0. No `putCert` means check endpoint/network; a successful `putCert` followed by read errors means investigate storage reads. An empty list alone does not prove SAP never sent it. |
| `ignoring unreadable certificate record` with `Get "": unsupported protocol scheme ""` | Older code could download an empty Graph URL. The fallback fix is in `148879a`; update and rebuild, then list the existing certificate before resending. |
| `certificate ... is not activated` | Verify fingerprint and activate the exact `authId` for that repository. |
| `secKey verification failed` / `URL expired` | Verify the signing certificate/identity and clock synchronization; inspect the detailed error rather than disabling signatures. |
| RSCMSTH2 `create: DOC_E (Empty)` → `part without X-compId` | Multipart compatibility handling was added in `8826358`. Update/rebuild and rerun. If it persists, collect request diagnostics; the precise live SAP body still needs verification. |
| RSCMSTH2 Content-Type comparison fails | `2f48574` preserves SAP's component Content-Type, including an absent header. Update/rebuild and create fresh test documents; previously stored metadata is not automatically rewritten. |
| CS Admin → Settings: `adminContRep` | Settings administration is unsupported. Current code returns **501 Not Implemented** (`daf4e83`); older builds returned generic 400. Edit `deploy/config.yaml` and use `/adapter certs` for certificates. This is not a supported CSADMIN settings UI. |
| SG813: logical file `KPRO_IMPORT_EXPORT` has no physical path | SAP-side file validation, separate from an HTTP multipart error. In transaction **FILE**, inspect the logical filename's assigned logical path and the physical-path assignment for the SAP OS syntax group. Basis should configure an approved, existing, accessible SAP directory. This does not change the final SharePoint destination. |
| `port is already allocated` | Change the corresponding host port in `.env` and recreate. `ADMIN_PORT=19090` avoids the common 9090 conflict; keep admin bound to localhost. |

For SG813, a UNIX path assignment for an approved `/usr/sap/<SID>/SYS/global/` directory would be `/usr/sap/<SID>/SYS/global/<FILENAME>`. Preserve the logical file's other attributes and use your system's actual path; do not create a directory on the adapter host to fix a SAP-side validation error. See [SAP's logical-path documentation](https://help.sap.com/saphelp_autoid2007/helpdata/EN/8d/3e4ec2462a11d189000000e8323d3a/content.htm?no_cache=true).

Collect logs while reproducing an error:

```bash
docker compose -f deploy/docker-compose.yml logs -f --tail=50 adapter
```

Current code logs refused requests with command, status, reason and request ID. For multipart diagnosis, temporarily set `LOG_LEVEL=debug` in `deploy/.env` and recreate the container. Debug logs include query/header details with `secKey` redacted and no request bodies; review them for document identifiers or other sensitive data before sharing. Restore `LOG_LEVEL=info` and recreate after diagnosis. Never share `.env`, API keys, tokens or signed URLs.

### Compatibility status

Recent changes cover missing Graph download URLs, SAP empty/unnamed multipart parts, detailed refusal logs and exact component Content-Type round-tripping. Automated regression tests cover these cases. The integration discussion confirmed live Graph access, SAP `serverInfo`/`putCert`, certificate listing after rebuild and several SAP document operations. It did **not** establish a completely passing SAP test suite after the latest changes. Run the acceptance steps above on your own system; this repository does not claim SAP certification.

`adminContRep`, `attrSearch` and `getCert` remain unsupported. See the [command support table](docs/content-server.md#6-command-support) for the implementation's scope.

## Configuration reference

| Setting | Default | Notes |
|---|---|---|
| `graph.maxConcurrency` | 64 | Simultaneous Graph calls per instance. Lower it if SharePoint throttles. |
| `graph.metadataCacheTtl` | 60s | Reuses item metadata and the pre-authenticated download URL. A repeat read then costs one SharePoint hop. |
| `transfer.simpleUploadMaxBytes` | 4 MiB | Uploads above this size use resumable sessions. |
| `transfer.chunkSizeBytes` | 5 MiB | Rounded down to a multiple of 320 KiB, as Graph requires. |
| `transfer.memoryBudgetBytes` | 512 MiB | Hard ceiling on all transfer buffers: upload (size-classed), chunk and download buffers. Callers wait instead of the process running out of memory. Spooled uploads go to disk (`spoolDir`). |
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

## Verifying a deployment

```bash
docker compose -f deploy/docker-compose.yml exec adapter /adapter check   # access checklist: PASS / FAIL / SKIP per permission
API_KEY=<key> bash scripts/e2e-smoke.sh                                # REST checks against real SharePoint
```

For optional Content Server smoke checks, use a separate disposable repository with `signature: none` and pass `CS_REPO=<test-repository>` (and `CS_URL` if the port differs). Keep it isolated and remove it afterwards; do not disable signatures on `Z1`. These unsigned smoke checks do not replace signed SAP tests. See [SETUP.md](SETUP.md) for additional deployment examples.

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
- command support and limitations (`adminContRep`, `attrSearch` and `getCert` are unsupported)
- the points to confirm on the real system

CMIS was ruled out because SharePoint Online has no CMIS endpoint. The background is in [docs/content-repository-interface-research.md](docs/content-repository-interface-research.md).

## License

Copyright (c) 2026 Vimal Vidyadharan. Released under the [MIT License](LICENSE).

Free to use, modify and distribute, including commercially, provided you retain the copyright and license notice in copies or substantial portions of the software. Attribution in your project's documentation is appreciated; no additional attribution requirement is imposed beyond the MIT License.
