# SAP troubleshooting

Start on the ZAKS Drive side. Every refused request is logged with its error code, status and title (the full explanation is in the `X-ErrorDescription` header SAP receives):

```bash
docker compose -f deploy/docker-compose.yml logs --since 15m adapter | grep -E 'request refused|level=ERROR|"level":"ERROR"'
```

The `X-ErrorCode` in the log is documented in [error codes](../error-codes.md) with its cause and fix.

| Symptom in SAP | Likely cause | What to do |
|---|---|---|
| OAC0 connection test cannot connect | Host or port wrong, firewall, `allowedNetworks` excludes SAP, `contentrepo` not in `server.interfaces` | `curl` the `serverInfo` URL from the SAP application server; check `allowedNetworks` |
| Connection test works, document commands fail with 401 | Certificate not sent, or sent but inactive | [Send and activate](oac0-configuration.md#send-and-activate-the-certificate); see [`CS_CERT_INACTIVE`](../error-codes.md#cs_cert_inactive) |
| 401 although the certificate is active | SAP's PSE was renewed, or the `authId` does not match the one stored | Send the certificate again and activate the new `authId` |
| 401 on every request, log says URL expired | Clock difference between SAP and the ZAKS Drive host | Synchronise time (NTP); `contentServer.clockSkew` is the tolerance |
| 403 on create | The document already exists | Use `update`, or choose another document ID |
| 409 `CS_BUSY` | Another writer changed the document at the same moment | SAP retries; if persistent, find the second writer |
| 409 `CS_META_UNREADABLE` | `~sapdoc.json` is corrupt | Restore the document folder from the SharePoint recycle bin or version history |
| 501 on `adminContRep` | CSADMIN is not supported | Expected; manage ZAKS Drive in `config.yaml` |
| RSCMSTH2 reports a Content-Type difference | A proxy rewrote the `Content-Type` header | ZAKS Drive returns it exactly as stored; remove header rewriting between SAP and the server |
| Signed request fails behind a reverse proxy | The proxy changed the query string (re-encoding, reordering) | The signature covers the URL as SAP sent it; pass the query string through unchanged |
| Slow or failing writes under load | Microsoft throttling | Look for [`SP_THROTTLED`](../error-codes.md#sp_throttled); lower `graph.maxConcurrency`; see [monitoring](../operations/monitoring.md) |
| `SP_ACCESS_DENIED` / `SP_UNAUTHORIZED` | Graph permission or site grant | [Graph permissions](../microsoft-365/graph-permissions.md#checking-what-the-token-carries) |
| Documents saved in SAP are not in SharePoint | The repository the document type uses is not `Z1`, or the original was never sent to it | [DMS integration](dms-integration.md); check the startup line `stored here` for the library |

## SG813: logical file KPRO_IMPORT_EXPORT has no physical path

SAP raised this while creating a DMS original in CV01N, before any request reached ZAKS Drive. It concerns SAP's own temporary file area for import/export, not the content repository: the logical file `KPRO_IMPORT_EXPORT` (maintained in transaction FILE) needs a physical path valid on the application server. Ask SAP Basis to maintain it. Do not enter a SharePoint URL there. Related: [OAC0 note](oac0-configuration.md#create-the-content-repository).

## What to send when asking for help

- The ZAKS Drive version (`zaks-drive version`).
- The `request refused` log lines for the failure, with `secKey` never present (it is not logged).
- The SAP report or transaction, and the exact SAP message.
- Never post `deploy/.env`, client secrets or tokens. See [SUPPORT](../../SUPPORT.md).
