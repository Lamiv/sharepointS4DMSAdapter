# Security

This page states what ZAKS Drive does for security, what depends on how you deploy it, and what it does not do. It is a description of the code, not an audit or a certification. Report vulnerabilities as described in [SECURITY.md](../../.github/SECURITY.md).

## Threat surface

| Listener | Default port | Who connects | Protected by |
|---|---|---|---|
| REST API | 8080 | Applications | API key or JWT, per-key permissions and repositories |
| SAP Content Server | 8090 | SAP application servers | SAP's signed URLs (`secKey`), `allowedNetworks` |
| Admin (health, metrics, certificate admin) | 9090 | Operators, Prometheus | Bound to `127.0.0.1` in the Compose file. Certificate admin additionally needs `ADMIN_TOKEN` |
| Outbound | 443 | Microsoft Entra, Microsoft Graph | TLS |

## Implemented in the code

- **API keys** are looked up by their SHA-256 hash; configuration can hold the hash (`sha256:`) instead of the plaintext. A fast unsalted hash is appropriate only because keys are long random machine tokens: generate them with `openssl rand -base64 32`, never use a human-chosen password as a key. (CodeQL's `go/weak-sensitive-data-hashing` flags this lookup; it is a false positive for random keys and is not suppressed.) Each key has `read`, `write`, `delete` permissions and a repository list; a repository a key may not use answers 404.
- **JWT bearer tokens** can be validated against an issuer, audience and JWKS URL, with roles mapped to permissions.
- **SAP signatures:** PKCS#7 `secKey` verification, certificate activation by an administrator, URL expiry, access modes, document protection (`docProt`). See [content server](../sap/content-server.md#signature-verification-and-modes).
- **Reserved content server folder:** the REST API cannot read or modify the folder that holds SAP documents and certificates.
- **Paging cursors** are HMAC-signed and cannot be altered by clients.
- **Input limits:** maximum upload size, SharePoint name and path validation before any Graph call, an in-flight request cap (`maxInFlight`) returning 503 beyond it.
- **Logging policy:** see [Logging policy](#logging-policy) below. Request paths and file names are logged, so do not put sensitive data in file names.
- **Container hardening in the Compose file:** distroless non-root image, read-only root filesystem, `no-new-privileges`, memory limit, admin port on localhost.
- **Management command:** the host-side `zaks-drive` command is a privileged tool with its own controls (no shell, only the deployment's containers, no deletions, verified downloads, sanitized output, allowlisted support bundles): [docs/cli.md](../cli.md#security-considerations).
- **Least-privilege Graph option:** `Sites.Selected` with a per-site grant ([Graph permissions](../microsoft-365/graph-permissions.md)).

## Logging policy

Logs are safe to share in an issue after you remove business identifiers you consider private. The rule, enforced by tests:

**A log line carries only approved fields, never free text from outside the process.**

| Layer | What is logged | What is never logged |
|---|---|---|
| REST API | request ID, route, method, path, status, duration, remote address; for failures the error `code`, `status`, catalogue `title` | the error `detail` (it can quote a caller or Microsoft), request or response headers, API keys, `Authorization` values, bodies |
| Microsoft Graph | operation, attempt, wait, reason category (`throttled`, `server`, `network`, `auth`), HTTP status, Graph's error code (identifier-shaped only), Microsoft's `request-id` | Graph's error message text, response bodies, request URLs (download and upload-session URLs are pre-authenticated), tokens |
| Authentication | a failure category and code (`AUTH_INVALID_API_KEY`, `TOKEN_FAILED`, ...), granted token roles | credentials, the contents of the `Authorization` header (not even its scheme), Entra's error text, the client secret |
| SAP Content Server | command, `contRep`, `docId`, `compId`, status, code, title, certificate fingerprint and `authId` | `secKey`, signed query strings (the debug log redacts `secKey`), request headers other than a short allow-list (`Content-Type`, `Content-Length`, ...), file contents |
| Configuration | the key that is wrong and its code; YAML parser messages have quoted values hidden | secret values |

How it is done: errors are described by `safelog.Summary` (type, code and status, never message text, and never a URL) and text shown to people goes through `safelog.Redact` (URL query strings, bearer tokens, JWTs and credential-shaped parameters removed). Unknown errors are logged by type only. `internal/safelog/policy_test.go` fails the build if a log call takes `err.Error()` or an error's `detail`, and the tests in `internal/restapi` and `internal/contentrepo` inject synthetic secrets into headers, Graph error bodies, connection failures and `secKey` parameters and check that none appears in a log or response.

To investigate a problem, use the request ID from the response or `X-Request-ID`: it appears in every log line for the request and is sent to Microsoft as `client-request-id`. For a failure on Microsoft's side, the logged Microsoft `request-id` is what Microsoft support asks for. Debug level (`LOG_LEVEL=debug`) adds SAP request details but still redacts `secKey` and header values.

Request headers are read by literal name in the code (`r.Header.Get("Slug")`). A variable header name makes CodeQL treat the value as a possible credential, and a test enforces the rule so the CodeQL clear-text-logging check stays meaningful.

Limits: identifiers such as file names, document IDs and site URLs are logged. A name a user chooses can contain anything, so treat logs as confidential business data.

## Secret management

Secrets are the Entra client secret, API keys, the admin token and any TLS private key.

- Put them only in `deploy/.env` (or your secret store) on the server. `deploy/.env` and `deploy/config.yaml` are git-ignored; never commit them, and never put a real secret into `config.example.yaml` or an issue.
- Transfer secrets through a password manager or secure channel, not chat or e-mail.
- The Entra secret has an expiry date. Rotate it before then: create a new secret, update `.env`, run `docker compose -f deploy/docker-compose.yml up -d` (recreate), confirm `zaks-drive check`, then delete the old secret. The same procedure applies at once if a secret is exposed.
- The Value of a secret (not the Secret ID) is what `GRAPH_CLIENT_SECRET` needs.
- `docker inspect` and the host environment expose container environment variables to anyone with Docker access on the host; restrict that access.

## Depends on your deployment

| Topic | Guidance |
|---|---|
| TLS | The server can serve TLS (`tlsCertFile`, `tlsKeyFile`) or sit behind a reverse proxy. Without either, API keys and documents travel in clear text on your network. A proxy must pass the query string of Content Server requests unchanged |
| Network exposure | Publish 8080 and 8090 only to the networks that need them. Use `allowedNetworks` and firewall rules for 8090 |
| API key hygiene | One key per application, minimum permissions, minimum repositories, rotate on staff or system changes |
| `auth.disabled` / `AUTH_DISABLED` | Switches REST authentication off. For emulator tests only |
| `signature: none` | Switches SAP signature checks off. For tests only |
| `enablePprof` | Exposes profiling on the admin port. Leave off in production |
| SharePoint permissions | Give people read access at most to the library that ZAKS Drive writes; SharePoint's own access control, retention, labels and DLP apply to the files |
| Host and image updates | Rebuild regularly for Go and base-image fixes; see [deployment](deployment.md#upgrades) |

## Not implemented

- Entra authentication with a **certificate** instead of a client secret ([roadmap](../roadmap.md)).
- Per-key rate limiting (only a global in-flight cap exists).
- A dedicated audit trail. Request logs record request ID, route, path, status, size and remote address, but not which API key was used, and they are ordinary logs, not tamper-evident.
- Malware scanning of uploads, content inspection, encryption beyond what TLS and Microsoft 365 provide.
- Mapping SAP users to SharePoint permissions. SharePoint sees only the application identity.
- Any independent security review or penetration test. None has been done.
