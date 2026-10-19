# OAC0 configuration and certificates

This page is the field-level reference for creating the SAP content repository and exchanging SAP's signing certificate. The end-to-end walk-through is [Connect SAP S/4HANA to SharePoint using ZAKS Drive](connect-s4hana-to-sharepoint.md).

Transaction codes, screen layouts and authorisations vary by SAP release and by who runs the system (on premise, RISE private cloud). Have SAP Basis perform these steps; an authorisation object, not just the transaction code, is required.

## Before you create the repository

1. ZAKS Drive runs with `server.interfaces` containing `contentrepo`, and `contentServer.repositories` contains an entry whose `contRep` is the ID you will create in OAC0 ([configuration](../configuration.md#contentserver-sap)).
2. The endpoint answers from the SAP application server, not just from the Docker host:

   ```bash
   curl -sS 'http://<zaks-drive-host>:8090/ContentServer/ContentServer.dll?serverInfo&pVersion=0047'
   # serverStatus="running";serverVendorId="SharePointAdapter";...
   # contRep="Z1";contRepDescription="...";contRepStatus="running";...
   ```

   (The vendor ID in this answer is a fixed identifier for compatibility; see [branding and compatibility](../development/branding-and-compatibility.md).)
3. SAP talks to the **Content Server port (8090 by default)**. It does not use the REST port or the admin port.
4. For HTTPS, terminate TLS in a reverse proxy or set `server.tlsCertFile` and `server.tlsKeyFile`, and import the server's certificate chain into STRUST (SSL client PSE). The TLS certificate of the server is unrelated to SAP's signing certificate below.
5. On RISE or other hosted SAP landscapes, request outbound connectivity from the SAP application servers to the ZAKS Drive host and port. SAP front ends (SAP GUI, browsers) may also fetch documents directly, depending on the OAC0 front-end setting, so users' networks may need access too.

## Create the content repository

Create an **unused** repository ID that matches `contRep`. These are the values used in the setup that passed SAP's test reports:

| OAC0 field | Value |
|---|---|
| Content repository | `Z1` (1 or 2 characters; must equal `contentServer.repositories[].contRep`) |
| Description | Your description |
| Document Area | `DMS` (Document Management System) for DMS originals. For ArchiveLink use the ArchiveLink area and the OAC3 assignments instead |
| Storage type | `04`, HTTP content server |
| Version | `0047` (`0046` and `0045` are also accepted) |
| HTTP server | The ZAKS Drive host name or IP address, reachable from SAP. No `http://`, no path |
| Port / SSL port | The published Content Server port (default `8090`), or the HTTPS port |
| HTTP script | `ContentServer/ContentServer.dll` |
| HTTPS frontend / backend | Match your deployment and client connectivity |
| No signature (if shown) | Leave **unchecked** when the repository has `signature: required` |

Save, then run OAC0's **Connection test** (identify the button by its tooltip; icon positions vary). Compare SAP's message with the ZAKS Drive log:

```bash
docker compose -f deploy/docker-compose.yml logs --since 10m adapter | grep -E 'request refused|content server'
```

A connection test only proves that `serverInfo` works. Signing and the document commands are covered by the [test reports](connect-s4hana-to-sharepoint.md#5-test-with-sap-standard-tools).

> The OAC0 **physical/back-end path** is a path on the SAP application server for certain local operations. It is not the SharePoint destination: never enter a SharePoint URL there ([SG813](troubleshooting.md#sg813-logical-file-kpro_import_export-has-no-physical-path)).

## Send and activate the certificate

SAP signs the URLs it hands to ZAKS Drive with the private key of its *system PSE*. ZAKS Drive verifies them with the matching public certificate, which SAP sends once and which an administrator must then activate.

1. **Send.** In OAC0, select `Z1` and choose **Send certificate**. SAP calls `putCert`. ZAKS Drive stores the certificate in SharePoint (`<folder>/~certs/`) as **inactive**.
2. **List** what arrived:

   ```bash
   docker compose -f deploy/docker-compose.yml exec adapter /zaks-drive certs list
   ```

   Expect a record with `contRep: Z1`, an `authId`, a `sha256Fingerprint` and `active: false`. The list omits the certificate bytes on purpose (`certificate: null`).
3. **Compare** the SHA-256 fingerprint with the system's signing certificate in **STRUST** (System PSE). Only continue if they match.
4. **Activate** using the **exact `authId` from the list**, as one quoted argument. Do not copy another system's distinguished name.

   ```bash
   docker compose -f deploy/docker-compose.yml exec adapter /zaks-drive certs activate Z1 '<authId from certs list>'
   docker compose -f deploy/docker-compose.yml exec adapter /zaks-drive certs list     # expect active: true
   ```

5. **Revoke** with `certs deactivate Z1 '<authId>'`.

Notes:

- The command reads `ADMIN_TOKEN` from the container's environment (set it in `deploy/.env`). The same functions exist over HTTP on the admin port, which is published on `127.0.0.1` only: `GET /admin/contentserver/certificates`, `POST .../activate` and `.../deactivate` with `{"contRep":"Z1","authId":"..."}`.
- After the SAP system's PSE is renewed, the new certificate must be sent and activated again.
- A successful `putCert` alone does not activate anything. Until activation, signed requests fail with [`CS_CERT_INACTIVE`](../error-codes.md#cs_cert_inactive).
- Immediately after a restart `certs list` can answer *not available* (404) until the SAP interface has finished starting; retry after `/readyz` reports `ready`.
- Certificates live in SharePoint with the documents, so they survive container recreation and are shared by several instances.

## Signature behaviour to expect

| Setting | Effect |
|---|---|
| `signature: required` and "No signature" unchecked | Every request from SAP must be signed and verified. Recommended |
| `signature: optional` | A signature is required only where the document's protection (`docProt`) asks for it; a signature that is present is always verified |
| `signature: none` | Signatures are ignored. Only for tests and isolated networks. Never for the production repository |

See [signature verification and modes](content-server.md#signature-verification-and-modes) and [`allowedNetworks`](../configuration.md#contentserver-sap).
