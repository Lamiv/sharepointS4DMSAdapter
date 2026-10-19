# SAP integration overview

ZAKS Drive offers SAP two ways to reach SharePoint. They share one Microsoft Graph client and one set of limits, and can run together or separately (`server.interfaces`).

| Interface | Port | Used by | Guide |
|---|---|---|---|
| SAP Content Server HTTP 4.5 (`contentrepo`) | 8090 | SAP repositories of storage type 04 (OAC0): DMS/KPro, GOS, ArchiveLink | [Connect SAP S/4HANA](connect-s4hana-to-sharepoint.md), [protocol reference](content-server.md) |
| REST API (`rest`, the default) | 8080 | Custom ABAP, CAP, Fiori backends, scripts | [REST API](../api/rest-api.md) |

## Protocol support versus tested scenarios

| Area | Protocol implemented | Verified |
|---|---|---|
| Content Server commands (serverInfo, info, get, docGet, create, mCreate, update, append, delete, search, putCert) | Yes | SAP reports RSCMSTH0-3, RSCMSTA0, RSCMSTAC passed (2026-10-16); also covered by automated tests with a Graph emulator |
| Signed URLs (secKey, DSA/RSA/ECDSA) | Yes | Real SAP signatures accepted |
| DMS originals through KPro | Uses the same commands | Verified end to end on one system (upload from SAP, storage in SharePoint, retrieval); customising to be documented ([DMS integration](dms-integration.md)) |
| GOS attachments, ArchiveLink | Uses the same commands | Not tested ([details](kpro-gos-archivelink.md)) |
| attrSearch, adminContRep, getCert | No (answer 501) | n/a |
| Transport between repositories (RSCMSTAT), RSCMSTHS | Same commands | Not run |

ZAKS Drive is not SAP-certified and is not an SAP product. See the disclaimer in the [README](../../README.md#disclaimer).

## What SAP does and does not control

- SAP decides which SAP user may read or write a document, and signs the URLs it gives out. ZAKS Drive verifies the signature, expiry and access mode.
- SharePoint sees one identity, the Entra application. SAP users do not exist in SharePoint, and SharePoint permissions do not map to SAP roles.
- Documents are addressed by SAP document ID, not by name. People browsing the library will see shard folders and IDs, not business names.

## Where to go next

[Connect SAP S/4HANA to SharePoint using ZAKS Drive](connect-s4hana-to-sharepoint.md), then [OAC0 configuration](oac0-configuration.md), [DMS integration](dms-integration.md) and [troubleshooting](troubleshooting.md).
