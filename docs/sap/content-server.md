# SAP Content Server HTTP interface: reference

ZAKS Drive implements the **SAP Content Server HTTP interface** that SAP uses for repositories of storage type *HTTP content server* (transaction OAC0). SAP programs address documents by an ID (`docId`) and components by a name (`compId`); ZAKS Drive stores them in SharePoint.

It follows SAP's *Content Server HTTP 4.5 Interface* documentation (ABAP Platform 2025, help.sap.com topic `4d0551f0eaa85c4be10000000a42189e`) and accepts `pVersion` 0045, 0046 and 0047. The details extracted from that documentation are in [content-server-http-spec-notes.md](content-server-http-spec-notes.md).

How to set it up: [Connect SAP S/4HANA to SharePoint using ZAKS Drive](connect-s4hana-to-sharepoint.md) and [OAC0 configuration](oac0-configuration.md). Problems: [SAP troubleshooting](troubleshooting.md).

> **Protocol support is not the same as a validated business scenario.** This page lists what the protocol layer implements and what was verified with SAP's own test reports. See [Verified against a real SAP system](#verified-against-a-real-sap-system) and the [SAP overview](overview.md).

## Command support

| Command | Status | Notes |
|---|---|---|
| serverInfo | ✅ | ASCII (default) and HTML |
| info | ✅ | Multipart (default; each part has `Content-Length: 0`, the component size is in `X-Content-Length`) or HTML; optional `compId` |
| get | ✅ | Default component `data`, else `data1`; `fromOffset` / `toOffset` (inclusive, `-1` = end); always status 200 |
| docGet | ✅ | Multipart with content |
| create | ✅ | PUT (one component) and POST multipart (0 to n components, rolled back on error); 403 if it exists |
| mCreate | ✅ | One result line per document; 201, 250 (some already existed) or 500 |
| update | ✅ | PUT creates or overwrites one component. POST replaces the document: **components not sent are deleted** (per the specification) |
| append | ✅ | SharePoint has no append, so the existing content plus the new data are streamed into a new version |
| delete | ✅ | GET (as specified) or DELETE; one component or the whole document |
| search | ✅ | `N;offset;offset;`, forward (streamed) or backward (range up to 64 MiB) |
| putCert | ✅ | DER, PEM or PKCS#7; 406 if unreadable |
| attrSearch | ❌ 501 | Print-list attribute index search; not needed for DMS or GOS |
| adminContRep (`configGet`, `configSet`, `certGet`, ...) | ❌ 501 | SAP's CSADMIN tool reading or writing the content server's own configuration. ZAKS Drive is configured in `config.yaml`, and certificates are managed with `zaks-drive certs`. Not used at run time by DMS, GOS, attachments or ArchiveLink; only a CSADMIN session against this repository reports an error ([`CS_ADMIN_UNSUPPORTED`](../error-codes.md#cs_admin_unsupported)) |
| getCert | ❌ 501 | The 0046 cache-server command; the specification gives no syntax |

HTTP statuses follow SAP's specification (200, 201, 250, 400, 401, 403, 404, 406, 409, 500, plus 405, 413, 501 and 503). The `X-ErrorDescription` header carries the error code, what happened and the fix; see [error codes](../error-codes.md).

## Signature verification and modes

Every URL that SAP signs carries a `secKey`: a PKCS#7 signature over the signed parameter values, concatenated **in URL order**. Which parameters are signed per command comes from the "Sign" column of SAP's documentation.

- ZAKS Drive checks the signature against the **activated** certificate for the request's `contRep` and `authId`. Supported algorithms: DSA, RSA, ECDSA. Supported digests: MD5, RIPEMD-160, SHA-1 and SHA-2.
- It also rejects URLs whose `expiration` has passed (plus `clockSkew`), and URLs whose `accessMode` (`r`, `c`, `u`, `d`) does not allow the operation.
- Certificates SAP sends with `putCert` are stored **inactive** until an administrator activates them (`zaks-drive certs activate`); see [certificates](oac0-configuration.md#send-and-activate-the-certificate).
- Verification takes about 0.26 ms per request for DSA and 0.035 ms for RSA-2048 (Go benchmarks in `internal/contentrepo`).

Signature modes, per content repository (`contentServer.repositories[].signature`):

| Mode | Behaviour |
|---|---|
| `required` | Every command except `serverInfo` and `putCert` needs a valid secKey. Use with the OAC0 option "No signature" **unchecked** |
| `optional` | Follows the document protection (`docProt`): a secKey is needed when the document's `docProt` covers the access mode (for `create`: the `docProt` URL parameter). Any secKey that is present is always verified |
| `none` | Signatures are ignored. Tests only, or behind `allowedNetworks` |

SharePoint is accessed by ZAKS Drive's Entra application; SAP decides who may do what, and SharePoint sees only the application's identity.

## Storage layout

```text
<repository root>/<folder>/                e.g. SAP_DMS/ContentServer/Z1/
  ~certs/<hash>.json                       certificates received via putCert (+ activation state)
  <shard>/<docId>/<compId>                 one file per component
  <shard>/<docId>/~sapdoc.json             Content-Type/charset/version per component, docProt, timestamps
```

- `<shard>` is the first byte (two hex characters) of the SHA-1 of the document ID. It spreads documents over 256 folders, because SharePoint struggles with more than about 5,000 items in one folder. To find a document's folder: `printf '%s' <docId> | sha1sum | cut -c1-2`. The layout is internal: SAP only ever uses document IDs.
- A component's `Content-Type` is stored exactly as SAP sent it and returned unchanged by `info`, `docGet` and `get`. If SAP sent none, none is returned (SAP's report RSCMSTH2 compares it literally).
- Characters SharePoint rejects in a `docId` or `compId` are stored as `%XX` (`scan:1.tif` becomes `scan%3A1.tif`) and translated back in every response.
- SharePoint versioning applies: `update` and `append` create new versions of the component file.
- Path limits: a typical SAP document path is about 95 characters, and the worst case about 345, against SharePoint's limit of 400 ([limits](../microsoft-365/sharepoint-configuration.md#sharepoint-limits-that-apply)).

## Verified against a real SAP system

On 2026-10-16, on S/4HANA 2025, with repository `signature: required` and pVersion 0047, the SAP test reports **RSCMSTH0, RSCMSTH1, RSCMSTH2 (docProt), RSCMSTH3 (mCreate), RSCMSTA0 and RSCMSTAC finished with 0 errors**. That covers `serverInfo`, `putCert`, signed `create` / `update` / `info` / `get` / `docGet` / `mCreate` in every protection setting, and SAP's API layer with caching. Not yet run: RSCMSTAT (transport between repositories) and RSCMSTHS.

Findings from running them, all fixed: SAP expects the Content-Type returned exactly as it sent it; it sends multipart parts without `X-compId` (the empty-document form); `adminContRep` commands are not supported and answer 501.

Still unconfirmed by a test: whether SAP signs URL-decoded or raw parameter values (ZAKS Drive accepts both), and GOS and ArchiveLink scenarios (not tested). A DMS original was later tested end to end by the project owner (upload from SAP, storage in SharePoint, retrieval), see [DMS integration](dms-integration.md). This is not an SAP certification.

## Consistency, concurrency and recovery

**Isolation from the REST API.** The content server folder (the `folder` above, including `~certs/`) is **reserved**. The REST API cannot list, read, write or delete anything inside it, and cannot delete a parent folder that contains it: reads answer 404 and writes answer 403 ([`PATH_RESERVED`](../error-codes.md#path_reserved)). This applies to every configured content repository, even when the `contentrepo` interface is switched off. Giving the content server its own library or root path is still recommended for production.

**Concurrent writes.**
- Within one instance, writes to the same document are serialised.
- Across instances, the metadata file is written conditionally on its eTag (`If-Match`). If another instance changed it meanwhile, ZAKS Drive reloads it, re-applies its change and retries up to 12 times with jittered backoff, then answers 409 ([`CS_BUSY`](../error-codes.md#cs_busy)).
- SAP normally does not modify one document from two sessions at once (KPro and DMS lock it), so conflicts should be rare.

**Partial failures.** Component files and `~sapdoc.json` are separate SharePoint writes:

| Situation | Behaviour |
|---|---|
| `create`: component stored, metadata write fails | The component is deleted again and SAP receives an error, so it can repeat the create |
| `create` (multipart) or `mCreate`: any component fails | The whole document is removed, as the specification requires |
| `update` / `append`: content stored, metadata write fails | SAP receives an error. The new content is live (the previous version stays in SharePoint version history); content type and timestamps may lag until the next write |
| `delete` of a component: file deleted, metadata write fails | Reported as success; the stale metadata entry is dropped on the next read |
| `~sapdoc.json` missing but components present | The document is treated as **fully protected** (`docProt=rcud`): with `signature: optional`, every access needs a valid secKey |
| `~sapdoc.json` unreadable or corrupt | The document is **unavailable** (409, [`CS_META_UNREADABLE`](../error-codes.md#cs_meta_unreadable)) until the file is repaired or restored. Nothing is served with guessed protection |

**Recovery.** Each SAP document is self-contained in its folder: the components plus `~sapdoc.json`. Restore **the whole document folder together** from the SharePoint recycle bin (93 days), from the library's version history, or from your Microsoft 365 backup. See [backup and recovery](../operations/backup-recovery.md).

**People and other tools in the library.** Treat the content server folder as owned by ZAKS Drive: give users read access at most, and do not edit or move files there by hand. Hand edits are not tracked, the metadata cache (60 s) and SAP's references can then disagree with SharePoint, and anyone with write access can also change the `~certs/` records. Restrict write access to administrators.

## Logging and metrics

Every request is logged as JSON with `iface="contentrepo"` and `route="cs:<command>"`; refused requests also log the error code, status, title, `contRep`, `docId` and `compId` (never the `secKey`, and never file contents). Prometheus metrics use the same labels; see [monitoring](../operations/monitoring.md).
