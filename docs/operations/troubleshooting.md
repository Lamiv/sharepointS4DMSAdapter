# Troubleshooting

Every error that ZAKS Drive returns or logs has a code with cause and fix in [error codes](../error-codes.md). SAP-side symptoms are in [SAP troubleshooting](../sap/troubleshooting.md). Start with:

```bash
docker compose -f deploy/docker-compose.yml exec adapter /zaks-drive check     # tests every access, with fixes
curl -s localhost:9090/readyz                                                   # ready, or the reason
docker compose -f deploy/docker-compose.yml logs --tail 100 adapter
```

| Symptom | Likely cause | Fix |
|---|---|---|
| Container exits with `configuration error` | A missing or invalid value | The message lists every problem with its field; fix and `up -d` |
| Token error `invalid_client` ([`TOKEN_FAILED`](../error-codes.md#token_failed)) | Wrong or expired secret, or the Secret ID used instead of the Value | New secret, update `.env` ([Entra](../microsoft-365/entra-app-registration.md)) |
| `the access token has NO application permissions` / `SP_UNAUTHORIZED` | Permission missing, delegated instead of application, or no admin consent | [Graph permissions](../microsoft-365/graph-permissions.md#checking-what-the-token-carries) |
| 403 from SharePoint (`SP_ACCESS_DENIED`) | `Sites.Selected` without a site grant, or a grant with role `read` | Grant `write` to the app on the site |
| `CFG_SITE_URL_INVALID` | A library or page link in `SHAREPOINT_SITE_URL` | Use the site address only |
| `SP_LIBRARY_NOT_FOUND` | Wrong library name | Use the display name; the error lists existing libraries |
| Documents not where you expect | Another site, library or OneDrive is configured | `grep 'stored here'` in the log; compare the library URL |
| `permission denied` reading `/etc/adapter/config.yaml` | The non-root container cannot read the file | `chmod 644 deploy/config.yaml` |
| `port is already allocated` | Another service uses 8080, 8090 or 9090 (Ubuntu Cockpit uses 9090) | Set `REST_PORT`, `CS_PORT` or `ADMIN_PORT` in `.env` |
| REST 401 | Missing or wrong key | `X-API-Key` exactly as in `.env` |
| REST 404 for a repository | The key may not use it | `auth.apiKeys[].repositories` |
| REST 403 `PATH_RESERVED` | The path is in the SAP content server folder | Use another folder |
| 503 with `Retry-After` | SharePoint throttling or the in-flight limit | Lower `graph.maxConcurrency`; spread out bulk loads |
| Container restarts repeatedly | Look at `docker compose ... logs` for the first error; `OOMKilled` means the 1 GiB limit was hit | Lower `transfer.memoryBudgetBytes` or raise `mem_limit` |

If it is still unclear, collect the log lines for the failing request and its `X-Request-ID` (see [SUPPORT](../../.github/SUPPORT.md)). Never share `deploy/.env`.
