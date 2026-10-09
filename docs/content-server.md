# SAP Content Server HTTP interface: setup and operations

The adapter acts as an **SAP Content Server** (OAC0 storage type *HTTP content server*). S/4HANA uses it to store DMS originals, GOS attachments and ArchiveLink documents. The documents themselves end up in SharePoint/OneDrive.

It implements the *SAP Content Server HTTP 4.5 Interface* (ABAP Platform 2025 documentation, help.sap.com topic `4d0551f0eaa85c4be10000000a42189e`) and accepts `pVersion` 0045, 0046 and 0047. The details extracted from that documentation are in [content-server-http-spec-notes.md](content-server-http-spec-notes.md).

## 1. Configure the adapter

In `deploy/config.yaml`:

```yaml
server:
  interfaces: [rest, contentrepo]
  contentRepoAddr: ":8090"
  adminToken: ${ADMIN_TOKEN}        # protects certificate activation

contentServer:
  allowedNetworks: []               # optional CIDR allowlist, e.g. S/4 app servers + user networks
  clockSkew: 5m
  autoActivateCertificates: false
  repositories:
    - contRep: Z1                   # must equal the repository ID created in OAC0
      description: S/4HANA DMS originals
      repository: DMS               # storage repository (library + rootPath) from `repositories:`
      folder: ContentServer/Z1      # below the repository root
      signature: required           # required | optional | none (see section 4)
```

Add `ADMIN_TOKEN=<random string>` to `deploy/.env`. You can generate one with `openssl rand -hex 24`. Then restart:

```bash
docker compose -f deploy/docker-compose.yml up -d --build
curl -s "http://localhost:8090/ContentServer/ContentServer.dll?serverInfo&pVersion=0047"
```

The command should answer with `serverStatus="running";...` and one `contRep="Z1";...` line.

## 2. Configure S/4HANA (Basis)

1. **Network.** The S/4HANA application servers must reach `http(s)://<adapter-host>:8090`.
   - For RISE, raise an ECS service request for outbound connectivity to the VM.
   - SAP GUI and browser front ends may also fetch documents directly with signed URLs, depending on the OAC0 front-end settings. In that case, users' networks need to reach the adapter as well.
   - For HTTPS, import the adapter's certificate chain into STRUST (SSL client PSE).
2. **OAC0**: create the content repository.

   | Field | Value |
   |---|---|
   | Content Rep. | `Z1` (same as `contRep` in config) |
   | Document Area | as required (e.g. *DMS* / *ArchiveLink*) |
   | Storage type | **HTTP content server** |
   | Version no. | `0047` (0046/0045 also accepted) |
   | HTTP server / Port / SSL port | adapter host, `8090` |
   | HTTP script | `ContentServer/ContentServer.dll` (any path works; the adapter routes by query) |
   | No signature | leave **unchecked** in production (signature on) |

3. **Send the certificate.** In OAC0, open the repository and choose **Send certificate**. SAP then calls `putCert`. The adapter stores the certificate in SharePoint (`<folder>/~certs/`), but it stays **inactive** until step 3 below.
4. **Assign the repository to its consumers:**
   - **DMS:** in OACT, create or assign a storage category pointing to `Z1`, then use it in the document type (DC10) or choose it in CV01N.
   - **GOS / ArchiveLink:** in OAC3, link object type and document type to `Z1`.
   - **Attachment service:** follow the attachment-service storage customizing (SAP Note 3094622 and related notes).

## 3. Activate the certificate

Certificates sent with `putCert` are inactive until an administrator activates them. This is the equivalent of the CSADMIN "Certificates" tab on SAP's own content server. Run these on the Docker host:

```bash
docker compose -f deploy/docker-compose.yml exec adapter /adapter certs list
docker compose -f deploy/docker-compose.yml exec adapter /adapter certs activate Z1 "CN=S4H"
```

The CLI uses the `ADMIN_TOKEN` the container already has from `deploy/.env`. `list` shows each certificate's subject, fingerprint, expiry and state. Compare the fingerprint with the System PSE certificate in STRUST before activating. Use `deactivate` to revoke a certificate.

The same functions are available over HTTP on the admin port, which only listens on localhost:

- `GET /admin/contentserver/certificates`
- `POST /admin/contentserver/certificates/activate` with body `{"contRep":"Z1","authId":"CN=S4H"}`

Then test the setup:
- Run report **RSCMST** (KPro content server test) for `Z1`.
- Create a DMS document in CV01N, check the original in, and open it again.

## 4. Security model

- **secKey verification.**
  - Every signed URL is checked against the activated certificate for its `contRep` and `authId`. The check is a PKCS#7 SignedData signature over the signed parameter values, concatenated **in URL order**. The signed parameters per command come from the spec's "Sign" column.
  - Supported algorithms: DSA, RSA, ECDSA. Supported digests: MD5, RIPEMD-160, SHA-1, SHA-2.
  - The adapter also rejects URLs whose `expiration` has passed (plus `clockSkew`), and URLs whose `accessMode` does not allow the operation.
- **Signature modes** (`signature:` per repository):

  | Mode | Behaviour |
  |---|---|
  | `required` | Every command except `serverInfo`/`putCert` needs a valid secKey. Use with OAC0 "No signature" **unchecked**. |
  | `optional` | Follows the spec's document protection: a secKey is needed when the document's `docProt` covers the access mode (for create: the `docProt` URL parameter). Any secKey that is present is always verified. |
  | `none` | Signatures are ignored. Only for tests, or behind `allowedNetworks`. |

- **SharePoint access** is done by the adapter's Entra app (client credentials). The SAP side controls who may access what, and SharePoint sees only the app's identity.
- **Cost:** verification takes about 0.26 ms per request for DSA and about 0.035 ms for RSA-2048 (Go benchmarks).

## 5. Storage layout

```
<repository root>/<folder>/
  ~certs/<hash>.json                  certificates received via putCert (+ activation state)
  <shard>/<docId>/<compId>            one file per component
  <shard>/<docId>/~sapdoc.json        Content-Type/charset/version per component, docProt, timestamps
```

- `<shard>` is the first byte (hex) of SHA-1(docId). It spreads documents over 256 folders.
- Characters SharePoint rejects in docId/compId are stored as `%XX` (for example `scan:1.tif` becomes `scan%3A1.tif`), and translated back on every response.
- SharePoint versioning applies: `update` and `append` create new versions of the component file.

## 6. Command support

| Command | Status | Notes |
|---|---|---|
| serverInfo | ✅ | ascii (default) and html |
| info | ✅ | multipart (default, part `Content-Length: 0`, size in `X-Content-Length`) or html; optional compId |
| get | ✅ | default component `data`, else `data1`; `fromOffset`/`toOffset` (inclusive, -1 = end), always 200 |
| docGet | ✅ | multipart with content |
| create | ✅ | PUT (one component) and POST multipart (0..n components, rolled back on error); 403 if it exists |
| mCreate | ✅ | per-document result lines; 201, 250 (some already existed) or 500 |
| update | ✅ | PUT creates or overwrites one component. POST replaces the document: **components not sent are deleted** (per spec) |
| append | ✅ | SharePoint has no append, so existing content plus new data are streamed into a new version |
| delete | ✅ | GET (per spec) or DELETE; one component or the whole document |
| search | ✅ | `N;offset;offset;` forward (streamed) or backward (range ≤ 64 MiB) |
| putCert | ✅ | DER, PEM or PKCS#7; 406 if unreadable |
| attrSearch | ❌ 501 | print-list attribute index search; not needed for DMS/GOS |
| getCert | ❌ 501 | 4.6 cache-server command; the spec gives no syntax |

## 7. Points still to verify against the real S/4HANA system

The SAP documentation leaves these open. The adapter takes the lenient option for each, but please confirm during the first RSCMST / CV01N tests:

1. **Signed values: decoded or raw.** The spec doesn't say whether SAP signs URL-decoded or raw values. The adapter accepts either.
2. **toOffset.** The adapter treats it as inclusive.
3. **Certificate format in putCert.** The adapter accepts DER, PEM and PKCS#7.
4. **Response header names.** The spec names them differently on different pages, so the adapter sends both `X-contentRep`/`X-contRep` and `X-numberComps`/`X-numComps`.
5. **Front-end access in OAC0.** If SAP GUI/browser clients fetch documents directly, check that they can reach the adapter.

## 8. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| 401 `no certificate for authId` | Send the certificate from OAC0 again; check `certs list`. |
| 401 `certificate ... is not activated` | Run `certs activate <contRep> <authId>`. |
| 401 `secKey verification failed` | A different PSE signed than the one whose certificate was activated (e.g. after a PSE renewal). Send the certificate again and activate it. |
| 401 `URL expired` | Clock difference between SAP and the VM. Check NTP, or raise `clockSkew`. |
| 403 `document already exists` | SAP re-sent a create for an existing docId (expected behaviour per spec). |
| 503 + `Retry-After` | SharePoint throttling. Lower `graph.maxConcurrency` or spread the load. |

Every request is logged as JSON with `iface="contentrepo"` and `route="cs:<command>"`. Prometheus metrics use the same labels.
