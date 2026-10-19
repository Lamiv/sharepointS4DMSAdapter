# Branding and compatibility

The project is named **ZAKS Drive** (with a space in prose and headings; `zaks-drive` in technical identifiers). It was previously called SharePoint Adapter / SharePoint S/4 DMS Adapter. This page records what was renamed and what deliberately was not, so existing deployments keep working.

## Naming rules

| Where | Form |
|---|---|
| Headings, prose, help text | ZAKS Drive |
| Binary, image, project, Compose project names | `zaks-drive` |
| Do not use | `ZAKSDrive`, `ZDrive`, or `ZAKS` alone as the product name |

Tagline: *Open-Source Enterprise Content Gateway*.

## Executable names

The project has two programs. They were one until the management command was added, so the name `zaks-drive` changed meaning on the host. The plan, so that nothing existing breaks:

| Name | Where | What it is | Status |
|---|---|---|---|
| `zaks-drive` | on the **host** (`<prefix>/bin`) | the management command: setup, start, stop, status, logs, doctor, update (`cmd/zaks-drive`, [docs/cli.md](../cli.md)) | new; the public command name |
| `zaks-drive-server` | **inside the image** | the server (`cmd/adapter`); the image's `ENTRYPOINT` | canonical server name from the release that introduces the command |
| `/zaks-drive` | inside the image | compatibility name of the **server** | kept: `docker compose exec adapter /zaks-drive check` still works |
| `/adapter` | inside the image | compatibility name of the server (pre-rename) | kept: old health checks and scripts still work |

- The two meanings never meet: the host has no server binary on its `PATH`, and the image has no management command. Both help texts say which program they are.
- `docker-compose.yml` health checks now call `/zaks-drive-server -healthcheck`; images built before the change keep working with their own compose file.
- The image carries three copies of the server binary under these names (it grew from about 38 MB to about 54 MB). The two compatibility names are planned for removal in a later minor release, **not before `1.0`**; the CHANGELOG will say when, and the removal will be announced one release ahead.
- A server released together with a command carries the same version; see the [compatibility policy](../cli.md#compatibility-policy).

## Changed

| Item | Now | Before |
|---|---|---|
| Binary | `/zaks-drive` | `/adapter` |
| Docker image name in Compose | `zaks-drive:${ZAKS_DRIVE_VERSION:-latest}` | `sharepoint-adapter` |
| Command-line banner, usage, `version` command, `-version` flag | ZAKS Drive | adapter |
| Log banner | `ZAKS Drive starting` | adapter starting |
| OCI image labels | title, description, source, license | none |
| Load-test Compose project | `zaks-drive-lt` | `spadapter-lt` |
| Content Server HTML info page text | ZAKS Drive | adapter |
| Documentation | Re-organised under `docs/` | flat |

## Kept on purpose

| Item | Why |
|---|---|
| `/adapter` in the image (a copy of the binary) | Existing `docker compose exec adapter /adapter ...` commands, health checks and scripts keep working |
| Compose service name `adapter` | Renaming it would leave the old container running next to the new one on upgrade (same ports) |
| The `spool` volume and all storage paths | Changing them needs a migration; none was made |
| Ports 8080, 8090, 9090 | Not changed for branding |
| API routes (`/api/v1/...`), request and response bodies, authentication, error codes | Contracts |
| Configuration keys and environment variable names (`GRAPH_*`, `ADAPTER_*`, `SHAREPOINT_*`, ...) | Existing `.env` and `config.yaml` files keep working |
| Metric names `adapter_*` | Existing dashboards and alerts |
| SAP `serverVendorId` = `SharePointAdapter` and the repository identifiers | Part of what SAP sees; changing it is a protocol-visible change |
| Internal package names | Not an importable library; renaming them would touch every file for no user benefit. (The Go module path was changed to `github.com/Lamiv/zaks-drive` with the repository rename.) |
| The label `sharepointadapter/cursor` inside the paging-cursor key | It is key material, not a module path; changing it would invalidate cursors that clients hold |
| The audience `api://zaks-drive` in the example JWT configuration | Only an example; set your own |

## Upgrading from the old name

Pull the new version and run `docker compose -f deploy/docker-compose.yml up -d --build`. The image tag changes from `sharepoint-adapter` to `zaks-drive`; the old image remains until you remove it with `docker image rm`. No configuration changes are required.

## Trademark note

ZAKS Drive is an independent open-source project and is not affiliated with, endorsed by, or certified by SAP SE or Microsoft Corporation. SAP, SAP S/4HANA, Microsoft, SharePoint, and other referenced product names are trademarks of their respective owners.

**Approval:** the repository owner, as approving authority, approved the name "ZAKS Drive" and its use. The brand is "ZAKS Drive"; the AKS-related name is not used as a product brand. See [repository rename](repository-rename.md).
