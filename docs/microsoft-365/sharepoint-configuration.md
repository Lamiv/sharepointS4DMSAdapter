# SharePoint and OneDrive configuration

Documents live in a SharePoint document library (or a user's OneDrive). ZAKS Drive keeps no database: the files, SAP's metadata files and SAP's certificates are all stored there.

## Choose the location

| | SharePoint site | OneDrive for Business |
|---|---|---|
| Recommended for | Production, shared repositories | Pilots and tests |
| Survives staff changes | Yes | **No**: when the owner's account is deleted or unlicensed, the OneDrive and every document in it are removed after the retention period |
| Quota | Site collection quota | The user's quota |
| Permission | `Sites.Selected` + grant, or `Sites.ReadWrite.All` | `Files.ReadWrite.All` (with `userId`) or `Sites.ReadWrite.All` (with the personal site's URL) |

Consumer OneDrive (personal Microsoft accounts) is **not** supported: the client-credentials flow needs an organisational tenant.

## Set the location in `deploy/.env`

```dotenv
SHAREPOINT_SITE_URL=https://contoso.sharepoint.com/sites/SAP_DOCS
SHAREPOINT_LIBRARY=Documents
SHAREPOINT_ROOT_PATH=SAP_DMS
```

| Variable | Rules |
|---|---|
| `SHAREPOINT_SITE_URL` | The **site** address only. A link copied from inside a library (`.../Forms/AllItems.aspx`) is rejected with [`CFG_SITE_URL_INVALID`](../error-codes.md#cfg_site_url_invalid) |
| `SHAREPOINT_LIBRARY` | The library's **display name**, which can differ from its URL segment (the default library is called "Documents" while its address says "Shared Documents"). A wrong name is answered with the list of libraries that exist ([`SP_LIBRARY_NOT_FOUND`](../error-codes.md#sp_library_not_found)). Empty selects the site's default library |
| `SHAREPOINT_ROOT_PATH` | A folder inside the library; created automatically. Everything ZAKS Drive stores lies below it, and clients can reach nothing outside it |

`config.example.yaml` reads these three variables, so the same file works on every server. For a OneDrive, write `userId: jane@contoso.com` into `config.yaml` instead of `siteUrl`/`driveName`. A Graph `driveId` (starting with `b!`) can also be used. See the [configuration reference](../configuration.md#repositories).

At start-up ZAKS Drive logs where documents are stored (`docker compose -f deploy/docker-compose.yml logs adapter | grep 'stored here'`), and `zaks-drive check` prints the library URL. If documents are not where you expect, compare that URL with the library in your browser.

## What ZAKS Drive creates

```text
<library>/<SHAREPOINT_ROOT_PATH>/
├── <folders and files uploaded through the REST API>
└── ContentServer/<contRep>/            reserved for SAP; the REST API cannot see or change it
    ├── ~certs/                         SAP certificates and their activation state
    └── <xx>/<SAP document id>/         one folder per SAP document: components + ~sapdoc.json
```

See [SAP Content Server interface](../sap/content-server.md#storage-layout) for the layout and why it is sharded.

## SharePoint limits that apply

| Limit | Value | What ZAKS Drive does |
|---|---|---|
| Path (after the domain) | 400 characters | Refuses longer paths up front with [`PATH_TOO_LONG`](../error-codes.md#path_too_long). A typical SAP document path is about 95 characters |
| One name | 255 characters | [`NAME_TOO_LONG`](../error-codes.md#name_too_long). Characters SharePoint forbids are stored as `%XX` for SAP documents, three characters each |
| Forbidden characters | `" * : < > ? / \ \|`, leading/trailing space, trailing dot | [`NAME_INVALID`](../error-codes.md#name_invalid) for REST names; encoded for SAP identifiers |
| Items per folder | About 5,000 before SharePoint struggles | SAP documents are spread over 256 sub-folders |
| Windows sync / Office desktop | 260 characters for the *local* path | Only matters if people sync the library to a PC; SAP and ZAKS Drive use the cloud API |
| Throttling | Set by Microsoft per app and tenant | Retries with the `Retry-After` delay, caps parallel calls (`graph.maxConcurrency`); see [`SP_THROTTLED`](../error-codes.md#sp_throttled) |

## Operating notes

- **Versions:** replacing a file (REST `PUT .../content`, SAP `update` and `append`) creates a SharePoint version; earlier versions stay in the library's version history.
- **Deletes** go to the SharePoint recycle bin (93 days by default), which is also the recovery path; see [backup and recovery](../operations/backup-recovery.md).
- **People in the library:** treat the SAP folder as owned by ZAKS Drive. Give people read access at most, and do not edit or move files there by hand.
- **Moving to another library:** SAP certificates are stored with the documents. After changing the location, send SAP's certificate again from OAC0 and activate it, or move the `~certs` folder along.

## Testing the credentials without installing anything

```bash
python3 scripts/check_access.py deploy/.env contoso.sharepoint.com sites/SAP_DOCS Documents SAP_DMS
```

The script (standard library only) checks the token, its roles, the site, the library, and upload, download, upload session, versions and delete, and prints for each the access required, what was tested and PASS / FAIL / SKIP with the fix. It never prints the secret.
