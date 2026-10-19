# SharePoint Adapter for SAP S/4HANA

**Store SAP documents and attachments in SharePoint Online, and give your own applications a fast REST API to the same library.**

[![CI](https://github.com/Lamiv/sharepointS4DMSAdapter/actions/workflows/ci.yml/badge.svg)](https://github.com/Lamiv/sharepointS4DMSAdapter/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/github/license/Lamiv/sharepointS4DMSAdapter)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/Lamiv/sharepointS4DMSAdapter)](go.mod)
[![Docker](https://img.shields.io/badge/docker-ready-2496ED?logo=docker&logoColor=white)](Dockerfile)
[![Tested with SAP S/4HANA 2025](https://img.shields.io/badge/tested%20with-SAP%20S%2F4HANA%202025-0FAAFF)](docs/content-server.md#7-verified-against-a-real-s4hana-system)

A self-hosted adapter in one small Docker container. SAP S/4HANA talks to it as a standard **SAP Content Server** (OAC0, DMS, attachments, ArchiveLink); your Fiori apps and services use its **REST API**. Both store the files in a SharePoint Online document library through Microsoft Graph, with no custom ABAP and no SAP-side add-on.

> **Start here:** try it in two minutes with the [Quick start](#quick-start) (no SharePoint needed), then follow the [step-by-step setup](#step-by-step-setup) for your tenant. SAP is optional.

**Contents:** [Preview](#preview) · [Features](#features) · [Quick start](#quick-start) · [Usage](#usage-examples) · [How it works](#how-it-works) · [Setup](#step-by-step-setup) · [Error codes](#error-codes) · [Configuration](#configuration-reference) · [REST API](#document-rest-api) · [Contributing](#contributing-and-community) · [License](#license)

## Preview

![Architecture: apps and SAP S/4HANA call the adapter's REST API and SAP Content Server interface, which share one core and use Microsoft Graph to reach a SharePoint Online library](docs/images/architecture.svg)

The built-in checklist tells you, in plain words, whether every access the adapter needs is in place:

```text
$ docker compose exec adapter /adapter check

[PASS] Access token from Microsoft Entra ID
[PASS] Application permission in the token
[PASS] [DMS] Find the library
    detail  : library URL: https://contoso.sharepoint.com/sites/SAP_DOCS/DOCS | root folder: SAP_DMS
[PASS] [DMS] Write: upload a small file
[PASS] [DMS] Read: download and compare
[PASS] [DMS] Resumable upload session (files over 4 MiB)
[PASS] [DMS] Version history
[PASS] [DMS] Delete the test file
  passed 10, failed 0, skipped 0
  All required access is in place.
```

When something is wrong, it says what, why and how to fix it:

```text
[FAIL] [DMS] Find the library
    detail  : SP_LIBRARY_NOT_FOUND: there is no library named "Nope" on this site; libraries the app can see: DOCS, Documents
    fix     : Set SHAREPOINT_LIBRARY to one of the listed names. It is the library's display name, which can differ from the URL segment.
```

## Features

- **SharePoint as your SAP content store.** DMS originals, GOS attachments and ArchiveLink through SAP's own HTTP Content Server protocol (pVersion 0045 to 0047), including SAP's signed URLs. Verified with SAP's test reports on S/4HANA 2025.
- **A REST API your apps will like.** Upload, download (ranges, ETags), replace with versions, thumbnails, search, folders, and per-business-object attachments; works with Fiori's `UploadSet`. [OpenAPI document](docs/openapi.yaml) included.
- **Fast and small.** One static Go binary in a distroless image. 100 concurrent clients use about 0.4 of a CPU core and 350 MB of memory in the [load tests](docs/load-test-results.md).
- **Large files without large memory.** Resumable uploads in chunks, streamed downloads, and a hard memory budget; slow clients wait instead of crashing the server.
- **Secure by default.** App-only access with the least permission that works (`Sites.Selected`), per-key `read` / `write` / `delete`, SAP certificate activation by an administrator, and SAP's folder in SharePoint hidden from the REST API.
- **Errors you can act on.** Every error has a [code](docs/error-codes.md), says what happened with the concrete values, and says how to fix it, in the REST response, in SAP's error header, at start-up and on the command line.
- **Easy to run and to trust.** Everything from `docker compose up`; `adapter check` and an end-to-end smoke test verify a deployment; metrics, health checks and request IDs are built in.

## Quick start

No Microsoft tenant is needed: this starts the adapter with a Microsoft Graph emulator.

```bash
git clone https://github.com/Lamiv/sharepointS4DMSAdapter.git && cd sharepointS4DMSAdapter
docker compose -p demo -f loadtest/docker-compose.yml up -d --build graphmock adapter
until curl -fs localhost:9090/readyz >/dev/null; do sleep 1; done          # prints nothing when ready
curl -s -H "X-API-Key: loadtest-key" --data-binary @README.md \
  "http://localhost:8080/api/v1/repositories/DMS/documents?folder=demo&fileName=readme.md"
```

You get the new document's metadata back as JSON (`id`, `path`, `size`, `eTag`, and links to its content and thumbnail). Stop and clean up with `docker compose -p demo -f loadtest/docker-compose.yml down -v`.

Ready for the real thing? Follow the [step-by-step setup](#step-by-step-setup): register an Entra app, put five values in `deploy/.env`, `docker compose up`, and run `adapter check`.

## Usage examples

**REST API** (replace the key with one of yours; the repository `DMS` comes from `config.yaml`):

```bash
KEY="X-API-Key: <your key>"; API=http://localhost:8080/api/v1/repositories/DMS

curl -s -H "$KEY" --data-binary @invoice.pdf "$API/documents?folder=invoices/2026&fileName=invoice.pdf"   # upload
curl -s -H "$KEY" "$API/children?path=invoices/2026"                                                      # list a folder
curl -s -H "$KEY" -o copy.pdf "$API/documents/<id>/content"                                               # download
curl -s -H "$KEY" -H "Range: bytes=0-1023" "$API/documents/<id>/content"                                  # part of a file
curl -s -H "$KEY" "$API/search?q=invoice"                                                                 # search
curl -s -H "$KEY" -X POST "$API/objects/BUS2081/5105600001/documents?fileName=scan.pdf" --data-binary @scan.pdf  # attach to a business object
```

**SAP S/4HANA:** create a content repository in OAC0 with storage type *HTTP content server*, pointing to `http(s)://<adapter-host>:8090`, script `ContentServer/ContentServer.dll`, version `0047`; send SAP's certificate, activate it with `adapter certs activate`, and run SAP's test reports. The full walk-through is in [step 8 to 10](#8-create-the-sap-content-repository-in-oac0) and [docs/content-server.md](docs/content-server.md). To check the endpoint from a shell:

```bash
curl 'http://localhost:8090/ContentServer/ContentServer.dll?serverInfo&pVersion=0047'
# serverStatus="running";serverVendorId="SharePointAdapter";...
# contRep="Z1";contRepDescription="SAP DMS originals";contRepStatus="running";...
```

**Operate it:**

```bash
docker compose -f deploy/docker-compose.yml exec adapter /adapter check            # access checklist
docker compose -f deploy/docker-compose.yml exec adapter /adapter certs list       # SAP certificates and their state
docker compose -f deploy/docker-compose.yml logs adapter | grep 'request refused'  # every refused request, with code and reason
curl -s localhost:19090/readyz                                                      # "ready", or why not
```

## How it works

Both interfaces sit on the same internal components, so a fix or a limit applies to both:

| Component | Package | What it does |
|---|---|---|
| Graph storage client | `internal/graph` | Drive item calls, upload sessions and downloads. Retries 429/5xx and honours `Retry-After` (one back-off for the whole process). Caps concurrent Graph calls. |
| Transfer engine | `internal/transfer` | Uploads ≤ 4 MiB go in one PUT; larger ones use a resumable session with 5 MiB chunks. A process-wide memory budget bounds RAM. Bodies of unknown length spool to disk. Downloads stream with `Range` support. |
| Storage service | `internal/storage` | Maps repositories to SharePoint libraries and folders. Every item must lie inside its repository root. Content server folders are reserved (invisible to REST). Paging cursors are HMAC-bound to repository and folder. Caches item metadata. |
| Authentication | `internal/auth` | Inbound: API keys (with per-repo read/write/delete) or JWT validated against JWKS. Outbound: Entra ID client credentials with a cached token, refreshed by a single request. |
| Observability | `internal/observability` | JSON logs, request/correlation IDs (also sent to Graph as `client-request-id`), Prometheus metrics, health/readiness, overload protection. |
| Document REST API | `internal/restapi` | Fiori-oriented REST API ([OpenAPI](docs/openapi.yaml)). |
| SAP Content Server interface | `internal/contentrepo` | SAP Content Server HTTP 4.5 interface (pVersion 0045–0047) for OAC0 "HTTP content server": DMS, GOS, ArchiveLink. secKey (PKCS#7) verification, putCert plus certificate activation. See [setup guide](docs/content-server.md). |


### Documentation

| Document | What it is for |
|---|---|
| [SETUP.md](SETUP.md) | Installation guide for administrators, from Entra app registration to a running server |
| [docs/content-server.md](docs/content-server.md) | The SAP Content Server interface: OAC0 setup, certificates, signature modes, storage layout, recovery |
| [docs/error-codes.md](docs/error-codes.md) | **Every error code**, its cause and its fix |
| [docs/openapi.yaml](docs/openapi.yaml) | REST API reference (OpenAPI 3) |
| [docs/load-test-results.md](docs/load-test-results.md) | Performance at 10, 25, 50 and 100 concurrent clients |
| [docs/content-server-http-spec-notes.md](docs/content-server-http-spec-notes.md) | Notes on SAP's Content Server HTTP interface specification |
| [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md) | How to contribute and how to report a vulnerability |

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
SHAREPOINT_SITE_URL=https://contoso.sharepoint.com/sites/SAP_DOCS
SHAREPOINT_LIBRARY=Documents
SHAREPOINT_ROOT_PATH=SAP_DMS
ADAPTER_API_KEY_S4=<generated-read-write-delete-key>
ADAPTER_API_KEY_FIORI=<different-generated-read-only-key>
ADMIN_TOKEN=<different-generated-admin-token>
REST_PORT=8080
CS_PORT=8090
ADMIN_PORT=19090
```

The API key names are defaults from the template; non-SAP applications can use them too. Never publish their values or put a privileged API key in browser-delivered code. 

**Where the documents go is set here, in `.env`**, not in `config.yaml`:

| Variable | Value |
|---|---|
| `SHAREPOINT_SITE_URL` | The **site** address, for example `https://contoso.sharepoint.com/sites/SAP_DOCS`. Not a link to a library page. |
| `SHAREPOINT_LIBRARY` | The document library's **display name**, for example `Documents` or `DOCS`. Empty means the site's default library. |
| `SHAREPOINT_ROOT_PATH` | The folder inside the library for everything the adapter stores, for example `SAP_DMS`. Created automatically; empty means the library root. |

This guide uses **host port 19090** for administration to avoid conflicts such as Cockpit on 9090. The container still listens on 9090. Compose binds the admin port to localhost only.

### 4. Configure the library and interfaces

The SharePoint location comes from `deploy/.env` (step 3), so `deploy/config.yaml` normally needs **no change** for it. The template's `repositories` list reads the three variables:

```yaml
repositories:
  - id: DMS
    siteUrl: ${SHAREPOINT_SITE_URL}
    driveName: ${SHAREPOINT_LIBRARY}
    rootPath: ${SHAREPOINT_ROOT_PATH}
```

`DMS` is the adapter's own repository identifier (used in REST URLs and by the SAP settings below). `driveName` is the library's display name, not necessarily its URL segment. For OneDrive for Business use `userId: user@contoso.com` instead of `siteUrl` and `driveName` (see [storage configuration alternatives](SETUP.md#62-settings-deployconfigyaml)). If a variable is empty the adapter refuses to start and names it ([`CFG_REPO_LOCATION_MISSING`](docs/error-codes.md#cfg_repo_location_missing)); a wrong library name is answered with the list of libraries that exist ([`SP_LIBRARY_NOT_FOUND`](docs/error-codes.md#sp_library_not_found)).

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

Every message the adapter produces has a **code**: look it up in [Error codes](#error-codes) first. The table below covers problems seen while running SAP's own test reports.

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

## Error codes

Every error the adapter returns or prints has a **stable code**, tells you **what happened** (with the concrete names and values) and **how to fix it**. The full list, with the cause and fix of each code, is in [docs/error-codes.md](docs/error-codes.md). It is generated from the code itself, and a test fails if it is out of date.

| Where you see it | Form |
|---|---|
| REST API | `application/problem+json` with `code`, `title`, `detail`, `hint` (the fix), `type` (a link to the code) and `requestId` |
| SAP (OAC0, RSCMST, CCMS, logs) | The `X-ErrorDescription` header: `CODE: what happened. Fix: what to do`, plus an `X-ErrorCode` header; the HTTP status follows SAP's specification |
| Start-up and `adapter` commands | `[CODE] what happened`, then `fix:` and `more:` lines |
| Logs | Refused requests are logged with `code`, `reason` and `request_id`: `docker compose logs adapter \| grep 'request refused'` |

```json
{
  "type": "https://github.com/Lamiv/sharepointS4DMSAdapter/blob/main/docs/error-codes.md#name_invalid",
  "title": "Invalid file or folder name",
  "status": 400,
  "code": "NAME_INVALID",
  "detail": "the name \"a:b.txt\" contains characters SharePoint does not allow: :",
  "hint": "Rename it. The detail shows the offending name.",
  "requestId": "70ea77318262ff277c71e7a5"
}
```

```text
X-ErrorDescription: CS_CONTREP_UNKNOWN: content repository "QQ" is not configured; configured: Z1. Fix: The ID in OAC0 must equal contentServer.repositories[].contRep in config.yaml ...
```

```text
The adapter cannot start because of its configuration:
  [CFG_REPO_LOCATION_MISSING] repository "DMS" has no SharePoint location: siteUrl, userId, siteId and driveId are all empty (config.yaml references ${SHAREPOINT_SITE_URL}, which is empty or not set: define it in deploy/.env and recreate the container)
      fix: Set SHAREPOINT_SITE_URL (and SHAREPOINT_LIBRARY, SHAREPOINT_ROOT_PATH) in deploy/.env, or write the values into config.yaml.
```

When you ask for help, give the **code** and the **request ID**; never include secrets.

### The errors you are most likely to meet

| Code | You see it when | What to do |
|---|---|---|
| [`CFG_REPO_LOCATION_MISSING`](docs/error-codes.md#cfg_repo_location_missing) | The adapter will not start | Set `SHAREPOINT_SITE_URL` (and library, root path) in `deploy/.env` |
| [`TOKEN_FAILED`](docs/error-codes.md#token_failed) | Microsoft refuses the app's credentials | Wrong or expired secret: use the secret **Value**, create a new one if needed |
| [`SP_UNAUTHORIZED`](docs/error-codes.md#sp_unauthorized) | `/readyz` says the token has no roles | Add a Graph **Application** permission and **Grant admin consent** |
| [`SP_ACCESS_DENIED`](docs/error-codes.md#sp_access_denied) | Uploads fail with 502 although `/readyz` is ready | Grant the app the `write` role on the site (`Sites.Selected`), or use `Sites.ReadWrite.All` |
| [`SP_LIBRARY_NOT_FOUND`](docs/error-codes.md#sp_library_not_found) | Wrong `SHAREPOINT_LIBRARY` | The message lists the libraries that exist |
| [`AUTH_INVALID_API_KEY`](docs/error-codes.md#auth_invalid_api_key), [`AUTH_MISSING_CREDENTIALS`](docs/error-codes.md#auth_missing_credentials) | REST calls get 401 | Send `X-API-Key` with a key from `deploy/.env`; recreate the container after changing it |
| [`AUTH_PERMISSION_DENIED`](docs/error-codes.md#auth_permission_denied) | A read-only key tries to write | Use a key with `write`, or extend the key's permissions |
| [`NAME_INVALID`](docs/error-codes.md#name_invalid), [`PATH_TOO_LONG`](docs/error-codes.md#path_too_long) | An upload is rejected before it starts | Rename; SharePoint forbids `" * : < > ? / \ |` and limits paths to 400 characters |
| [`SP_THROTTLED`](docs/error-codes.md#sp_throttled) | 503 with `Retry-After` | Retry; lower `graph.maxConcurrency` |
| [`CS_CERT_INACTIVE`](docs/error-codes.md#cs_cert_inactive) | SAP gets 401 after "Send certificate" | `adapter certs activate <contRep> '<authId>'` after comparing the fingerprint |
| [`CS_URL_EXPIRED`](docs/error-codes.md#cs_url_expired) | SAP gets 401 on every request | Clocks of SAP and the adapter server differ: fix time synchronisation |
| [`CS_SECKEY_MISSING`](docs/error-codes.md#cs_seckey_missing) | SAP's request carries no signature | In OAC0 leave "No signature" unchecked, and send and activate the certificate |
| [`CS_CONTREP_UNKNOWN`](docs/error-codes.md#cs_contrep_unknown) | OAC0 repository ID differs from the config | Make `contentServer.repositories[].contRep` equal the OAC0 ID |
| [`CS_ADMIN_UNSUPPORTED`](docs/error-codes.md#cs_admin_unsupported) | SAP's CSADMIN settings screen fails | Expected: configure the adapter in `config.yaml`; DMS and attachments are not affected |

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

Where documents are stored: `SHAREPOINT_SITE_URL`, `SHAREPOINT_LIBRARY`, `SHAREPOINT_ROOT_PATH` (referenced from `config.yaml` as `${...}`).

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

## Contributing and community

Contributions are welcome: bug reports with the error code, documentation fixes, test results from real SAP systems, and code.

- **Questions and bug reports:** [GitHub Issues](https://github.com/Lamiv/sharepointS4DMSAdapter/issues). Please use the templates, and look the [error code](docs/error-codes.md) up first.
- **Contributing:** read [CONTRIBUTING.md](CONTRIBUTING.md). It explains how to build and test with Docker only, the project layout and the rules (every error needs a code, every fix needs a test).
- **Security:** report vulnerabilities privately; see [SECURITY.md](SECURITY.md).
- **Conduct:** [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

There is no chat server; GitHub Issues is the place for everything.

## License

Copyright (c) 2026 Vimal Vidyadharan. Released under the [MIT License](LICENSE).

Free to use, modify and distribute, including commercially, provided you retain the copyright and license notice in copies or substantial portions of the software. Attribution in your project's documentation is appreciated; no additional attribution requirement is imposed beyond the MIT License.
