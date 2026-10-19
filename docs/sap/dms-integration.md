# SAP DMS integration

Goal: originals of SAP DMS documents (CV01N, CV02N, CV03N) are stored in SharePoint through ZAKS Drive.

> **Status.** The content repository protocol is verified ([SAP reports](connect-s4hana-to-sharepoint.md#5-test-with-sap-standard-tools)). The DMS customising below has **not** been confirmed to work end to end. In the system used for testing, originals created in CV01N did not arrive in repository `Z1`. The steps are what SAP's documentation points to; items marked *unverified* were not proven. Have SAP DMS consultants confirm them for your release.

## How DMS decides where an original goes

With KPro, a DMS original is stored in a content repository selected by the **storage category** assigned to the document's *document type* and workstation application. The repository ID in OAC0 is only reachable if a storage category names it.

| Step | Transaction | What to check |
|---|---|---|
| 1. Repository exists, certificate active | OAC0 | [OAC0 configuration](oac0-configuration.md) |
| 2. Storage category pointing to `Z1` | OACT | Create or change a storage category whose content repository is `Z1` (*unverified for your release*) |
| 3. DMS uses KPro | DC10 | Document type setting "Use KPRO" (*unverified*) |
| 4. Document type and workstation application | DC10 | The workstation application used (for example the one for PDF or Word) must be assigned the storage category (*unverified*) |
| 5. SAP's temporary file area | FILE | Logical file `KPRO_IMPORT_EXPORT` needs a physical path ([SG813](troubleshooting.md#sg813-logical-file-kpro_import_export-has-no-physical-path)) |
| 6. Create a document | CV01N | Attach an original; save |
| 7. Verify it was stored in `Z1` | SE38 | Report `DMS_KPRO_READ` for the document (*unverified name*), or look for a `create` request in the ZAKS Drive log |

## Checking from ZAKS Drive

When CV01N saves an original, ZAKS Drive logs a request with `route="cs:create"` (or `cs:update`) for `contRep="Z1"`. No such line means SAP never sent the file; the problem is in steps 2 to 5, not in ZAKS Drive:

```bash
docker compose -f deploy/docker-compose.yml logs --since 10m adapter | grep -E 'cs:create|cs:update|request refused'
```

If the line is there and succeeds, the file is in the library under `<root>/ContentServer/Z1/<shard>/<docId>/`.

## Known limits

- SharePoint shows SAP document IDs, not CV01N document numbers. Searching SharePoint by business name is not possible.
- SAP controls access. Do not give users write access to the library ([consistency](content-server.md#consistency-concurrency-and-recovery)).
- Versions: each SAP `update` creates a SharePoint version of the component.
