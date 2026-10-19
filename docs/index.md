# ZAKS Drive documentation

ZAKS Drive is an open-source gateway that lets SAP S/4HANA and custom applications store documents in Microsoft SharePoint Online and OneDrive for Business. Start with the [README](../README.md) for what it is; this index is the way into everything else.

## Choose your path

| I want to... | Read |
|---|---|
| Try it in five minutes without Microsoft 365 or SAP | [Quickstart](quickstart.md) |
| Install it on a server | [Installation](installation.md), then [Configuration](configuration.md) |
| Connect SAP S/4HANA | [Connect SAP S/4HANA to SharePoint using ZAKS Drive](sap/connect-s4hana-to-sharepoint.md) |
| Call it from my application | [REST API guide](api/rest-api.md), [openapi.yaml](api/openapi.yaml) |
| Set up Microsoft 365 | [Entra app registration](microsoft-365/entra-app-registration.md), [Graph permissions](microsoft-365/graph-permissions.md), [SharePoint configuration](microsoft-365/sharepoint-configuration.md) |
| Run it in production | [Deployment](operations/deployment.md), [Security](operations/security.md), [Monitoring](operations/monitoring.md), [Backup and recovery](operations/backup-recovery.md) |
| Fix an error | [Error codes](error-codes.md), [Operations troubleshooting](operations/troubleshooting.md), [SAP troubleshooting](sap/troubleshooting.md) |
| Understand or change the code | [Architecture](architecture.md), [Local development](development/local-development.md), [Testing](development/testing.md) |

## SAP

[Overview](sap/overview.md) · [Connect SAP S/4HANA](sap/connect-s4hana-to-sharepoint.md) · [Content Server reference](sap/content-server.md) · [OAC0 and certificates](sap/oac0-configuration.md) · [DMS integration](sap/dms-integration.md) · [KPro, GOS, ArchiveLink](sap/kpro-gos-archivelink.md) · [Troubleshooting](sap/troubleshooting.md) · background: [specification notes](sap/content-server-http-spec-notes.md), [interface research](sap/research-content-repository-interface.md)

## Microsoft 365

[Entra app registration](microsoft-365/entra-app-registration.md) · [Graph permissions](microsoft-365/graph-permissions.md) · [SharePoint configuration](microsoft-365/sharepoint-configuration.md)

## Operations

[Deployment](operations/deployment.md) · [Security](operations/security.md) · [Monitoring](operations/monitoring.md) · [Backup and recovery](operations/backup-recovery.md) · [Troubleshooting](operations/troubleshooting.md)

## Development

[Local development](development/local-development.md) · [Testing](development/testing.md) · [Load testing](development/load-testing.md) · [Load test results](development/load-test-results.md) · [Release process](development/release-process.md) · [Branding and compatibility](development/branding-and-compatibility.md) · [Repository rename plan](development/repository-rename-plan.md)

## Project

[Roadmap](roadmap.md) · [Changelog](../CHANGELOG.md) · [Contributing](../CONTRIBUTING.md) · [Security policy](../SECURITY.md) · [Support](../SUPPORT.md)

## Moved documents

Earlier versions kept files at different paths. `SETUP.md`, `docs/content-server.md` and `docs/load-test-results.md` remain as short pointers to the new locations; `docs/openapi.yaml` and the other moved files are now under `docs/api/` and `docs/sap/`.
