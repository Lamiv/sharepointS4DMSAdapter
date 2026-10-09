# Setup guide: SharePoint Adapter for SAP S/4HANA

This guide installs the adapter on an Ubuntu server with Docker, connects it to SharePoint Online or OneDrive for Business, and gets it ready for SAP. Follow the sections in order. Most people finish in about an hour, not counting the Microsoft and SAP approvals.

**What you end up with:** one container on the server, offering:

| Port | Interface | Who uses it |
|---|---|---|
| 8080 | Document REST API | Fiori apps, other enterprise applications |
| 8090 | SAP Content Server HTTP interface | S/4HANA (transaction OAC0): DMS, GOS, ArchiveLink |
| 9090 | Health, metrics, certificate admin | Only the server itself (bound to `127.0.0.1`) |

Documents are stored in SharePoint. The container itself keeps no data.

---

## Contents

1. [Before you start: what you need](#1-before-you-start-what-you-need)
2. [Register the app in Microsoft Entra ID](#2-register-the-app-in-microsoft-entra-id)
3. [Test the Microsoft credentials](#3-test-the-microsoft-credentials)
4. [Prepare the Ubuntu server](#4-prepare-the-ubuntu-server)
5. [Install the adapter](#5-install-the-adapter)
6. [Configure](#6-configure)
7. [Start and verify](#7-start-and-verify)
8. [Restrict network access](#8-restrict-network-access)
9. [Connect SAP S/4HANA](#9-connect-sap-s4hana)
10. [Day-to-day operations](#10-day-to-day-operations)
11. [Troubleshooting](#11-troubleshooting)

---

## 1. Before you start: what you need

| Item | Who usually provides it | Notes |
|---|---|---|
| Ubuntu server (22.04 or 24.04), 2 vCPU, 2 GB RAM, 20 GB disk | Infrastructure team | A VM is fine. The container is limited to 1 GB RAM; measured use at 100 concurrent clients was about 0.4 CPU core and about 360 MB. Uploads without a known size are buffered on disk (Docker volume `spool`), so leave a few GB free. |
| SSH access with `sudo` | Infrastructure team | |
| Outbound HTTPS (443) from the server | Network team | To `login.microsoftonline.com`, `graph.microsoft.com` and `*.sharepoint.com`. GitHub and `proxy.golang.org` are needed only while building. |
| Inbound access from SAP to ports 8080/8090 | Network team; SAP ECS for RISE | S/4HANA application servers, and possibly users' networks (see [section 9](#9-connect-sap-s4hana)). |
| An Entra ID app registration with a client secret | Microsoft 365 / Entra admin | [Section 2](#2-register-the-app-in-microsoft-entra-id) |
| The SharePoint location | SharePoint / M365 admin | A site URL (`https://<tenant>.sharepoint.com/sites/<site>`) or OneDrive URL (`https://<tenant>-my.sharepoint.com/personal/<user>`), plus a folder name such as `SAP_DMS` |

> **SharePoint site or OneDrive?** Both work. For production, use a **SharePoint team or communication site**. A OneDrive belongs to one user account: if that account is deleted or unlicensed, its OneDrive and every SAP document in it are removed after the retention period.

---

## 2. Register the app in Microsoft Entra ID

*Done by an Entra / Microsoft 365 administrator in https://entra.microsoft.com.*

1. Go to **App registrations → New registration**.
   - Name it something like `SAP SharePoint Adapter`.
   - Supported account types: *single tenant*.
   - No redirect URI.
2. Note the **Application (client) ID** and the **Directory (tenant) ID** from the Overview page. You don't need the Object ID.
3. Go to **Certificates & secrets → Client secrets → New client secret**, and copy the **Value** immediately.
   > The **Value** column is the secret, and it's shown only once. The *Secret ID* (a GUID) is **not** the secret.
4. Go to **API permissions → Add a permission → Microsoft Graph → Application permissions**, and add one of these:

   | Storage | Permission | Scope |
   |---|---|---|
   | SharePoint site (recommended) | `Sites.Selected` | Only the sites you grant (step 6) |
   | SharePoint site (simpler) | `Sites.ReadWrite.All` | All sites in the tenant |
   | OneDrive for Business | `Files.ReadWrite.All` or `Sites.ReadWrite.All` | All files/sites in the tenant |

5. Click **Grant admin consent for <tenant>**. The status must show a green tick.
6. *(Only with `Sites.Selected`.)* Grant the app write access to the site. In [Graph Explorer](https://developer.microsoft.com/graph/graph-explorer), signed in as an admin, run:
   ```http
   GET https://graph.microsoft.com/v1.0/sites/<tenant>.sharepoint.com:/sites/<site>?$select=id
   ```
   Then post to the site ID you get back:
   ```http
   POST https://graph.microsoft.com/v1.0/sites/<site-id>/permissions
   Content-Type: application/json

   { "roles": ["write"],
     "grantedToIdentities": [{ "application": { "id": "<client-id>", "displayName": "SAP SharePoint Adapter" } }] }
   ```

Pass the **tenant ID**, **client ID** and **secret value** to whoever installs the server, over a secure channel such as a password manager. Never send them by email or chat.

---

## 3. Test the Microsoft credentials

Do this before installing anything else; it catches most setup mistakes in two minutes. Run it on the Ubuntu server, or on any Linux/macOS machine:

```bash
TENANT='<tenant-id>'; CLIENT='<client-id>'; SECRET='<secret-value>'
SITE_HOST='<tenant>.sharepoint.com'          # OneDrive: <tenant>-my.sharepoint.com
SITE_PATH='sites/<site>'                     # OneDrive: personal/<user_domain_com>
FOLDER='SAP_DMS'

TOKEN=$(curl -s -X POST "https://login.microsoftonline.com/$TENANT/oauth2/v2.0/token" \
  -d "grant_type=client_credentials&client_id=$CLIENT&client_secret=$SECRET&scope=https://graph.microsoft.com/.default" \
  | sed -E 's/.*"access_token":"([^"]+)".*/\1/')
echo "token: ${TOKEN:0:20}..."

SITE_ID=$(curl -s -H "Authorization: Bearer $TOKEN" "https://graph.microsoft.com/v1.0/sites/$SITE_HOST:/$SITE_PATH?\$select=id" \
  | sed -E 's/.*"id":"([^"]+)".*/\1/')
echo "site: $SITE_ID"

curl -s -H "Authorization: Bearer $TOKEN" "https://graph.microsoft.com/v1.0/sites/$SITE_ID/drive/root:/$FOLDER?\$select=name,webUrl"; echo
```

| Result | Meaning |
|---|---|
| JSON containing `"name":"SAP_DMS"` | ✅ Everything works; continue. |
| `token:` is followed by `{"error":"invalid_client"...` | The secret is wrong or expired. Use the secret **Value**. |
| `accessDenied` / 403 | A permission is missing, admin consent wasn't granted, or (`Sites.Selected` only) the site grant is missing. |
| `itemNotFound` on the last call | The folder doesn't exist yet. Create it in SharePoint, or let the adapter create it on the first upload. |

Clear the secret from your shell afterwards with `unset SECRET TOKEN`.

---

## 4. Prepare the Ubuntu server

```bash
# Docker Engine + Compose plugin (official Docker repository)
sudo apt-get update && sudo apt-get install -y ca-certificates curl git openssl
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
sudo apt-get update && sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo usermod -aG docker $USER && newgrp docker
docker compose version            # should print v2.x

# Accurate time is required: SAP's signed URLs expire after a few minutes
sudo timedatectl set-ntp true
timedatectl status | grep "synchronized: yes"
```

---

## 5. Install the adapter

```bash
sudo mkdir -p /opt/sharepoint-adapter && sudo chown $USER: /opt/sharepoint-adapter
git clone https://github.com/Lamiv/sharepointS4DMSAdapter.git /opt/sharepoint-adapter
cd /opt/sharepoint-adapter
```

> **Server without internet access for building?** Build the image on a machine that has access:
> ```bash
> docker build --target adapter -t sharepoint-adapter:latest .
> docker save sharepoint-adapter:latest -o adapter.tar
> ```
> Copy `adapter.tar` and the repository to the server and run `docker load -i adapter.tar`. In [section 7](#7-start-and-verify), leave out `--build`.

---

## 6. Configure

There are two files, and **neither is committed to git**:

| File | Contains |
|---|---|
| `deploy/.env` | Secrets: Microsoft credentials, API keys, admin token |
| `deploy/config.yaml` | Settings: where documents go, which interfaces run, security options |

### 6.1 Secrets: `deploy/.env`

```bash
cd /opt/sharepoint-adapter/deploy
cp .env.example .env && chmod 600 .env
echo "ADAPTER_API_KEY_S4=$(openssl rand -base64 32)"
echo "ADAPTER_API_KEY_FIORI=$(openssl rand -base64 32)"
echo "ADMIN_TOKEN=$(openssl rand -hex 24)"
nano .env
```

Fill in every line:

| Variable | Value |
|---|---|
| `GRAPH_TENANT_ID` | Directory (tenant) ID |
| `GRAPH_CLIENT_ID` | Application (client) ID |
| `GRAPH_CLIENT_SECRET` | The client secret **Value** |
| `SP_DRIVE_ID` | Leave empty unless your config uses `driveId: ${SP_DRIVE_ID}` |
| `ADAPTER_API_KEY_S4` | Generated above. The key S/4HANA or other back-end systems use for the REST API. |
| `ADAPTER_API_KEY_FIORI` | Generated above. A read-only key for Fiori/consumers. |
| `ADMIN_TOKEN` | Generated above. Protects certificate activation. |
| `REST_PORT`, `CS_PORT`, `ADMIN_PORT` | Optional, commented out in the template. Uncomment only if a default host port (8080, 8090, 9090) is already used on the server. |

### 6.2 Settings: `deploy/config.yaml`

```bash
cp config.example.yaml config.yaml
nano config.yaml
```

The example file is fully commented. You must change two sections.

**`repositories:`** says where documents are stored. Replace the two examples with your own location, and **delete any example you don't use**: the adapter retries at startup until every repository listed resolves.

```yaml
repositories:
  - id: DMS
    siteUrl: https://<tenant>.sharepoint.com/sites/<site>      # or https://<tenant>-my.sharepoint.com/personal/<user>
    # driveName: Documents      # only if the site has several document libraries
    rootPath: SAP_DMS           # folder inside the library; created automatically
```

**`contentServer:`** sets up the SAP Content Server interface. The `contRep` value must equal the repository ID your SAP Basis team creates in OAC0. Agree on it with them first, e.g. `Z1`.

```yaml
contentServer:
  allowedNetworks: []           # e.g. ["10.20.0.0/16"]; see section 8
  repositories:
    - contRep: Z1
      description: S/4HANA DMS originals
      repository: DMS           # the id from repositories: above
      folder: ContentServer/Z1  # stored under SAP_DMS/ContentServer/Z1
      signature: required       # keep "required" in production
```

> The content server's folder (`folder:` above, here `SAP_DMS/ContentServer/Z1`) is reserved: the REST API can't see or change it. If you can, point the content server at its own SharePoint library or root path anyway, and give people at most read access to it (see [docs/content-server.md](docs/content-server.md#8-consistency-concurrency-and-recovery)).

Everything else can keep its default. Things you might want to change:

| Setting | When to change |
|---|---|
| `server.interfaces` | Remove `contentrepo` or `rest` if you need only one interface |
| `server.corsOrigins` | Fiori launchpad calling the REST API directly from the browser |
| `auth.apiKeys[*].repositories / permissions` | Restrict what each key may do (`read`, `write`, `delete`) |
| `graph.maxConcurrency` | Lower it (e.g. 32) if SharePoint throttles (see troubleshooting) |
| `server.tlsCertFile` / `tlsKeyFile` | Serve HTTPS directly instead of using a reverse proxy |

---

## 7. Start and verify

```bash
cd /opt/sharepoint-adapter
docker compose -f deploy/docker-compose.yml up -d --build
docker compose -f deploy/docker-compose.yml ps
```

The first build takes 1–3 minutes. After about 15 seconds the status should show **healthy**. The container restarts by itself after crashes and reboots.

Now run these checks in order:

```bash
# 1. Connected to Microsoft and the SharePoint location? -> "ready"
#    (use your ADMIN_PORT instead of 9090 if you changed it)
curl -s localhost:9090/readyz

# 2. Upload and read back a test document through the REST API
KEY=$(grep '^ADAPTER_API_KEY_S4=' deploy/.env | cut -d= -f2-)
curl -s -H "X-API-Key: $KEY" --data-binary @README.md \
  "http://localhost:8080/api/v1/repositories/DMS/documents?folder=smoke-test&fileName=readme.md"
curl -s -H "X-API-Key: $KEY" "http://localhost:8080/api/v1/repositories/DMS/children?path=smoke-test"

# 3. SAP Content Server interface answers -> serverStatus="running" and a contRep="Z1" line
curl -s "http://localhost:8090/ContentServer/ContentServer.dll?serverInfo&pVersion=0047"
```

Check 2 should create `SAP_DMS/smoke-test/readme.md` in SharePoint; open the site to see it. Delete the test folder afterwards.

If `readyz` returns `not ready`, check the logs:

```bash
docker compose -f deploy/docker-compose.yml logs --tail 50 adapter
```

[Section 11](#11-troubleshooting) lists the common causes.

---

## 8. Restrict network access

Only SAP systems, and the users who open documents, should reach ports 8080 and 8090. Port 9090 already listens on `127.0.0.1` only. **Never expose it.**

> ⚠️ Docker adds its own firewall rules for published ports, and these **bypass `ufw`**. Use at least one of these:

1. **Allow list in the adapter** (simplest, enforced for the SAP interface). In `config.yaml`:
   ```yaml
   contentServer:
     allowedNetworks: ["10.20.30.0/24", "10.40.0.0/16"]   # SAP app servers + user networks
   ```
2. **Bind to one network interface.** In `deploy/docker-compose.yml`, change `"8090:8090"` to `"<server-private-ip>:8090:8090"`, and the same for 8080.
3. **Cloud firewall / security group** in front of the VM. This is the recommended option for cloud-hosted servers.

**HTTPS:** for production, put TLS in front of 8080/8090. You can use a reverse proxy (nginx, Caddy, SAP Web Dispatcher) with your company certificate, or set `server.tlsCertFile` / `server.tlsKeyFile` and mount the files into the container. The SAP Basis team must then import the certificate chain into STRUST.

Apply changes with `docker compose -f deploy/docker-compose.yml up -d`.

---

## 9. Connect SAP S/4HANA

*Done by the SAP Basis team. The detailed guide, with all OAC0, OACT and OAC3 fields, is [docs/content-server.md](docs/content-server.md).*

1. **Network:** the S/4HANA application servers must reach `http(s)://<server>:8090`. On RISE, raise an SAP ECS service request for this outbound connection.
2. **OAC0:** create content repository `Z1` (the same ID as `contRep` in the config):
   - storage type *HTTP content server*
   - version `0047`
   - server `<server>`, port `8090`
   - HTTP script `ContentServer/ContentServer.dll`
   - leave **No signature** unchecked
3. **OAC0 → Send certificate.** SAP sends its public certificate to the adapter. It arrives **inactive**.
4. **Activate the certificate on the server.** Compare the fingerprint with the System PSE certificate in STRUST first:
   ```bash
   cd /opt/sharepoint-adapter
   docker compose -f deploy/docker-compose.yml exec adapter /adapter certs list
   docker compose -f deploy/docker-compose.yml exec adapter /adapter certs activate Z1 "<authId shown by list>"
   ```
5. **Test:** run report **RSCMST** for `Z1`. Then create a test document in CV01N, check an original in, and open it again.
6. **Assign consumers:**
   - DMS storage category in OACT (then DC10 / CV01N)
   - GOS / ArchiveLink links in OAC3

**Fiori and other applications** use the REST API on port 8080 with an API key. The API reference is [docs/openapi.yaml](docs/openapi.yaml), which you can open in https://editor.swagger.io or import into SAP API Management.

---

## 10. Day-to-day operations

All commands run from `/opt/sharepoint-adapter`:

| Task | Command |
|---|---|
| Status | `docker compose -f deploy/docker-compose.yml ps` |
| Live logs (JSON, one line per request) | `docker compose -f deploy/docker-compose.yml logs -f adapter` |
| Health | `curl -s localhost:9090/readyz` |
| Metrics (Prometheus) | `curl -s localhost:9090/metrics \| grep ^adapter_` |
| Apply a changed `.env` / `config.yaml` | `docker compose -f deploy/docker-compose.yml up -d --force-recreate` |
| Update to the latest version | `git pull && docker compose -f deploy/docker-compose.yml up -d --build` |
| Stop | `docker compose -f deploy/docker-compose.yml down` |
| List SAP certificates | `docker compose -f deploy/docker-compose.yml exec adapter /adapter certs list` |
| Revoke a certificate | `docker compose -f deploy/docker-compose.yml exec adapter /adapter certs deactivate Z1 "<authId>"` |

**Backups:** documents, their metadata and SAP certificates all live in SharePoint. On the server, back up only `deploy/.env` and `deploy/config.yaml` (e.g. in your password vault). Rebuilding a server means repeating sections 4–7 with those two files.

**Secret rotation:** the Entra client secret expires (6–24 months, set at creation). Before it does, create a new secret, update `GRAPH_CLIENT_SECRET` in `.env`, and recreate the container.

**Monitoring:** alert on these signals:
- `/readyz` not returning `ready`
- a rising `adapter_graph_retries_total{reason="throttled"}`
- HTTP 5xx in `adapter_http_requests_total`

---

## 11. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `configuration error: ...` and the container exits | A typo or missing value in `config.yaml` / `.env` | The message names the field. Fix it and run `up -d`. |
| Log: `token endpoint returned 401: invalid_client` | Wrong or expired client secret, or a Secret **ID** used instead of the **Value** | Create a new secret and update `.env`. |
| Log: `repository initialisation failed; retrying` | `siteUrl`, `driveName` or permissions are wrong, or an unused example repository is still in the config | Rerun [section 3](#3-test-the-microsoft-credentials) with the same values. |
| `readyz` → `not ready`, log shows 403 | Missing Graph permission or admin consent; `Sites.Selected` without a site grant | [Section 2](#2-register-the-app-in-microsoft-entra-id), steps 4–6 |
| REST API returns 401 | Missing or wrong `X-API-Key` | Use the key from `.env` exactly. |
| REST API returns 404 for a repository | The key isn't allowed for that repository | Check `auth.apiKeys[*].repositories`. |
| SAP: 401 `no certificate for authId` | The certificate was never sent | OAC0 → Send certificate |
| SAP: 401 `certificate ... is not activated` | Not activated yet | [Section 9](#9-connect-sap-s4hana), step 4 |
| SAP: 401 `URL expired` | The clocks on SAP and the server differ | Check `timedatectl`, or raise `contentServer.clockSkew`. |
| SAP: 401 `secKey verification failed` | The SAP PSE was renewed after activation | Send the certificate again, then activate it. |
| SAP: 403 `client address not allowed` | The caller is outside `allowedNetworks` | Add its network. |
| Requests return 503 with `Retry-After` | SharePoint throttling | Lower `graph.maxConcurrency`; spread out bulk loads. |
| SAP: 409 `administration data ... unreadable` | A document's `~sapdoc.json` is corrupt | Restore the document folder ([docs/content-server.md](docs/content-server.md#8-consistency-concurrency-and-recovery)). |
| REST API returns 403 `path is reserved` | The path is inside the SAP content server folder | Expected: that folder belongs to SAP. Use another folder. |
| `port is already allocated` on start | Another service uses 8080, 8090 or 9090. Ubuntu's **Cockpit** uses 9090. | Find it with `sudo ss -ltnp \| grep -E ':(8080\|8090\|9090)'`. Then set `ADMIN_PORT=19090` (or `REST_PORT` / `CS_PORT`) in `deploy/.env` and run `up -d` again. Use the new port wherever this guide says `localhost:9090`. |

Still stuck? Collect `docker compose -f deploy/docker-compose.yml logs --tail 200 adapter` and the `X-Request-ID` header of the failing request. Every log line carries the same ID, and the adapter forwards it to Microsoft as `client-request-id`, so Microsoft support can trace the call. Never share `.env`.

---

### Further reading

- [README.md](README.md): architecture and features
- [docs/content-server.md](docs/content-server.md): SAP Content Server interface, detailed Basis guide
- [docs/openapi.yaml](docs/openapi.yaml): REST API reference
- [docs/load-test-results.md](docs/load-test-results.md): performance at 10/25/50/100 concurrent clients
