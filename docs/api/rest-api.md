# REST API guide

The REST API (port 8080, base path `/api/v1`) lets custom applications store and retrieve files in SharePoint without handling Microsoft Graph themselves. It is separate from the [SAP Content Server interface](../sap/content-server.md), and `rest` is the default `server.interfaces` value.

The full contract is in [openapi.yaml](openapi.yaml). This page gives working examples.

> **There is no document management frontend.** ZAKS Drive has no UI for browsing, previewing or managing documents; it is an API and an SAP-facing server. Use SharePoint, or build a frontend on this API.

The examples assume `BASE=http://localhost:8080/api/v1`, a repository named `DMS`, and an API key in `$KEY` (see [security](../operations/security.md) and [`auth.apiKeys`](../configuration.md)). Try them without any Microsoft 365 tenant using the emulator stack in [local development](../development/local-development.md).

## Authentication and permissions

Send the key as `X-API-Key: <key>` or `Authorization: ApiKey <key>`. If JWT validation is configured, `Authorization: Bearer <token>` is accepted. Each key has permissions `read`, `write` and `delete` and a list of repositories it may see. A repository a key may not access answers 404, so names are not leaked. `/healthz`, `/readyz`, `/metrics` and `/version` are served on the admin port (9090), not under `/api/v1`, and need no key.

## Repositories

```bash
curl -sS -H "X-API-Key: $KEY" "$BASE/repositories"
```

## Upload

Raw body (the name via `fileName`, `Slug` or `X-File-Name`):

```bash
curl -sS -X POST -H "X-API-Key: $KEY" -H 'Content-Type: application/pdf' \
  --data-binary @invoice.pdf \
  "$BASE/repositories/DMS/documents?folder=invoices/2026&fileName=invoice-1001.pdf&conflict=fail"
```

Multipart (put `folder`, `conflict` and `fileName` **before** the file part):

```bash
curl -sS -X POST -H "X-API-Key: $KEY" \
  -F folder=invoices/2026 -F conflict=rename -F file=@invoice.pdf \
  "$BASE/repositories/DMS/documents"
```

The answer is `201` with the item (`id`, `name`, `size`, `eTag`, ...) and a `Location` header. Missing folders are created. `conflict` is `rename` (default), `replace` or `fail`. Files above `simpleUploadMax` use an upload session automatically; the maximum is `transfer.maxUploadBytes` (`413` above it).

## Read

```bash
curl -sS -H "X-API-Key: $KEY" "$BASE/repositories/DMS/documents/$ID"                  # metadata
curl -sS -H "X-API-Key: $KEY" -o out.pdf "$BASE/repositories/DMS/documents/$ID/content"
curl -sS -H "X-API-Key: $KEY" -H 'Range: bytes=0-1023' "$BASE/repositories/DMS/documents/$ID/content"
curl -sS -H "X-API-Key: $KEY" "$BASE/repositories/DMS/documents/$ID/versions"
curl -sS -H "X-API-Key: $KEY" "$BASE/repositories/DMS/documents/$ID/thumbnail" -o thumb.png
```

Downloads support `Range`, `If-Range` and `If-None-Match` (ETag).

## Replace and delete

```bash
curl -sS -X PUT -H "X-API-Key: $KEY" -H "If-Match: $ETAG" --data-binary @new.pdf \
  "$BASE/repositories/DMS/documents/$ID/content"
curl -sS -X DELETE -H "X-API-Key: $KEY" -H "If-Match: $ETAG" "$BASE/repositories/DMS/documents/$ID"
```

`If-Match` is optional; with it, a changed document answers `412` ([`ETAG_MISMATCH`](../error-codes.md#etag_mismatch)) instead of being overwritten. Use the `eTag` value from the document's metadata, quotes included. (The `ETag` header of the content download is a different tag, for caching and `If-None-Match`.)

## Browse and search

```bash
curl -sS -H "X-API-Key: $KEY" "$BASE/repositories/DMS/children?path=invoices/2026&top=100"
curl -sS -H "X-API-Key: $KEY" "$BASE/repositories/DMS/lookup?path=invoices/2026/invoice-1001.pdf"
curl -sS -X POST -H "X-API-Key: $KEY" -H 'Content-Type: application/json' \
  -d '{"path":"projects/P-1000/drawings"}' "$BASE/repositories/DMS/folders"
curl -sS -H "X-API-Key: $KEY" "$BASE/repositories/DMS/search?q=invoice"
```

Lists return `{"value":[...],"nextCursor":"..."}`. Pass `nextCursor` back as `cursor`; cursors are signed and cannot be edited.

## Documents attached to a business object

```bash
curl -sS -X POST -H "X-API-Key: $KEY" --data-binary @contract.pdf \
  "$BASE/repositories/DMS/objects/BUS2012/4500000123/documents?fileName=contract.pdf"
curl -sS -H "X-API-Key: $KEY" "$BASE/repositories/DMS/objects/BUS2012/4500000123/documents"
```

Files are stored in the folder `<type>/<key>` of the repository (here `BUS2012/4500000123`). Both parts must be valid SharePoint names.

## Errors

Errors are `application/problem+json` with `code`, `title`, `detail` and a `hint` naming the fix:

```json
{"type":"about:blank","title":"...","status":404,"code":"DOC_NOT_FOUND","detail":"...","hint":"...","requestId":"..."}
```

`type` links to the error catalogue; quote `requestId` in support requests.

Look up every code in [error codes](../error-codes.md). Handle `429` and `503` by waiting for `Retry-After`.
