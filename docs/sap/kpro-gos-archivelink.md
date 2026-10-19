# KPro, Generic Object Services and ArchiveLink

These SAP features all store documents in a content repository through the same Content Server HTTP commands ZAKS Drive implements. KPro, as used by DMS originals, has been tested end to end once (see [DMS integration](dms-integration.md)); **GOS attachments and ArchiveLink have not been tested with ZAKS Drive.** This page lists what each needs so a tester knows where to look; it is not a validated procedure.

| Feature | How it reaches the repository | What to configure (unverified) | Commands used |
|---|---|---|---|
| **KPro** (Knowledge Provider, used by DMS) | Documents in the KPro content model are stored in repositories chosen by storage category | OACT storage category, DC10 "Use KPRO" ([DMS integration](dms-integration.md)) | create, update, get, docGet, info, delete |
| **GOS attachments** (Generic Object Services, "Create attachment") | Stored via business document service, which uses a repository assigned to the document class | Transaction OAC0 repository plus the assignment of the BDS/GOS document class to it in SAP's customising | create, get, docGet, delete |
| **ArchiveLink** | The repository is linked to an SAP object type and document type | OAC3 link entries (object type, document type, repository) and OAD* tools | create, mCreate, get, info, search, delete |

## Expectations

- All three rely on the **document ID** SAP generates. ZAKS Drive stores whatever ID it receives; characters SharePoint forbids are encoded ([storage layout](content-server.md#storage-layout)).
- ArchiveLink print lists with attribute search use `attrSearch`, which ZAKS Drive does not implement (it answers 501).
- Large originals: the memory budget and upload limits in [configuration](../configuration.md) apply.

## How to test one scenario

1. Make sure `RSCMSTH0` to `RSCMSTH3` pass ([the reports](connect-s4hana-to-sharepoint.md#5-test-with-sap-standard-tools)).
2. Configure the scenario in SAP.
3. Perform one action and look for the request in the ZAKS Drive log (`route="cs:create"`).
4. Read it back from SAP and compare the content.
5. Report the result, including SAP release and ZAKS Drive version, so it can be added to the verified list. See [SUPPORT](../../SUPPORT.md) and [CONTRIBUTING](../../CONTRIBUTING.md).
