# Deployment

The supported deployment is one container run with Docker Compose ([installation](../installation.md)). ZAKS Drive keeps no state in the container: documents, SAP metadata and SAP certificates are in SharePoint, so the container can be replaced at any time.

## What the Compose file does

[`deploy/docker-compose.yml`](../../deploy/docker-compose.yml) runs the image `zaks-drive:${ZAKS_DRIVE_VERSION}` (default tag `latest`: your own local build, not a stability guarantee; see [versioning](../development/release-process.md#versioning)) as the service `adapter` with:

| Setting | Value |
|---|---|
| Ports | 8080 (REST), 8090 (SAP Content Server), 9090 (admin, `127.0.0.1` only). Host ports can be changed with `REST_PORT`, `CS_PORT`, `ADMIN_PORT` in `.env` |
| User and filesystem | Distroless non-root image, read-only root, `/tmp` as a 64 MB tmpfs |
| Volume | `spool`: disk-backed spool for uploads without a known size |
| Limits | `mem_limit: 1g`, 65536 open files |
| Restart | `unless-stopped`; health check calls `/adapter -healthcheck` against the admin port |
| Logs | JSON on stdout, rotated by Docker (5 files of 50 MB) |

The service and the volume keep their names on purpose. Renaming them would leave a second container and a second volume next to the old ones after an upgrade ([branding and compatibility](../development/branding-and-compatibility.md)).

## Sizing

Transfer buffers are capped by `transfer.memoryBudgetBytes` (512 MiB by default); the 1 GiB container limit leaves room for the Go runtime. `graph.maxConcurrency` (64) limits parallel Graph calls and is the lever against throttling. Emulator measurements at 100 concurrent clients: about 0.4 core and 350 MB ([load testing](../development/load-testing.md)). Treat them as a starting point, not a guarantee.

## Several instances

Instances share no memory but share SharePoint. Sidecar writes use `If-Match` with retries, so two instances serving the same repository are safe for SAP, but the metadata cache (60 s) means a change made by one can take up to that long to be seen by the other. A load balancer in front of port 8090 must pass the query string unchanged. High availability has not been load-tested.

## TLS

Terminate TLS in a reverse proxy, or set `server.tlsCertFile` and `server.tlsKeyFile` and mount the files. SAP must trust the chain (STRUST). See [security](security.md).

## Upgrades

```bash
cd /opt/zaks-drive
git fetch --tags && git checkout v<version>      # pin a release; avoid tracking a moving branch in production
export ZAKS_DRIVE_VERSION=$(scripts/version.sh)
docker compose -f deploy/docker-compose.yml up -d --build
docker compose -f deploy/docker-compose.yml exec adapter /zaks-drive version
docker compose -f deploy/docker-compose.yml exec adapter /zaks-drive check
```

- Read the [CHANGELOG](../../CHANGELOG.md) first.
- Configuration keys, API routes and the storage layout are kept compatible; see the compatibility table in [branding and compatibility](../development/branding-and-compatibility.md).
- SAP clients see a short interruption while the container restarts; SAP retries.
- Rollback: check out the previous version and run the same command. There is no data migration to undo.

## Environment-specific notes

- **RISE with SAP:** request outbound access from the SAP application servers to the ZAKS Drive host through SAP's service request process.
- **Cloud hosts:** put a security group in front of 8080 and 8090.
- **Kubernetes, other orchestrators:** the image is a standard container and should run, but no manifests are provided or tested.
