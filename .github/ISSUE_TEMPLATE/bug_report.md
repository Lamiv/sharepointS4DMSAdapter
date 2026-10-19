---
name: Bug report
about: Something does not work, or an error message is unclear
labels: bug
---

<!-- Never paste secrets: client secret, API keys, ADMIN_TOKEN, deploy/.env, signed URLs. -->

**What happened**

**What you expected**

**Error code and request ID** (from the response, the SAP `X-ErrorDescription` header, or the log)

```
code:
requestId:
```

**Which interface** — REST API / SAP Content Server (which SAP transaction or report?) / command line

**Output of the access check**

```
docker compose -f deploy/docker-compose.yml exec adapter /adapter check
```

**Relevant log lines** (`docker compose -f deploy/docker-compose.yml logs --since 30m adapter | grep 'request refused'`)

```
```

**Environment**

- Adapter version (`git log -1 --oneline`):
- Docker / OS:
- SharePoint: site or OneDrive; permission used (`Sites.Selected` / `Sites.ReadWrite.All` / `Files.ReadWrite.All`):
- SAP release (if relevant):
