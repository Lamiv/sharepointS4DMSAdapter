# Security policy

## Reporting a vulnerability

**Please do not open a public issue for a security problem.**

Use GitHub's private vulnerability reporting: on the repository page choose **Security → Report a vulnerability**. (The repository owner must have this feature enabled in the repository settings; no other contact address is published.) There is no fixed response time; this is a best-effort open-source project. Include:

- what you found and the impact you expect,
- the steps to reproduce it, or a proof of concept,
- the version (`zaks-drive version`) and how ZAKS Drive is deployed.

You will get an acknowledgement and an assessment as soon as the maintainer can. This is a volunteer-maintained project, so there is no guaranteed response time, but security reports are handled first. Please give a reasonable period to fix a problem before you disclose it publicly. You will be credited in the fix unless you prefer otherwise.

## Supported versions

Only the latest commit on `main` receives fixes. There are no long-term-support branches.

## What is in scope

ZAKS Drive's own code and configuration defaults: authentication and authorisation (API keys, JWT, admin token), the SAP `secKey` signature check, certificate handling, path and folder restrictions, request parsing, logging of sensitive data, and the Docker image.

Out of scope: vulnerabilities in Microsoft 365, SAP software or Docker itself, and attacks that need the attacker to already control the ZAKS Drive server or its `deploy/.env` file.

## How ZAKS Drive protects your data

| Area | Behaviour |
|---|---|
| Microsoft access | App-only (client credentials) with the least permission that works: `Sites.Selected` plus a site grant is recommended. The secret lives only in `deploy/.env`. |
| Callers of the REST API | API keys (compared as SHA-256 hashes) with per-repository `read` / `write` / `delete`, or JWT validated against a JWKS. A caller cannot tell a missing repository from one it may not use. |
| SAP callers | Every request can be signed by SAP (PKCS#7 `secKey`, parameters bound, expiry checked). SAP's certificate is stored inactive until an administrator activates it. `allowedNetworks` restricts source addresses. |
| Folder isolation | SAP's documents, metadata and certificates live under a reserved folder that the REST API can neither read nor change. |
| Paging cursors | Signed and bound to repository and folder, so they cannot be forged to reach other libraries. |
| Logs and errors | `secKey`, secrets, tokens, request credentials and file contents are never logged, and logs never copy text written by Microsoft or a caller (only codes, statuses, titles and IDs; see [logging policy](../docs/operations/security.md#logging-policy)). Error responses carry codes, reasons and fixes ([docs/error-codes.md](../docs/error-codes.md)). |
| Admin port | Bound to `127.0.0.1` only and protected by `ADMIN_TOKEN`. Do not publish it. |
| Container | Distroless, non-root, read-only file system. |
| Management command | `zaks-drive` runs only `docker` (explicit arguments, no shell, only the deployment's own compose file), never deletes volumes or images, never uses `sudo`, verifies downloads against a SHA-256 manifest before using them, sanitizes everything it prints and builds support bundles from allowlisted fields. See [the command guide](../docs/cli.md#security-considerations). |

## Hardening checklist for operators

- [ ] Use `Sites.Selected` with a `write` grant for the one site, not a tenant-wide permission.
- [ ] Rotate the Entra client secret before it expires and whenever it may have been seen; keep it out of tickets, chats and Git.
- [ ] Keep `deploy/.env` at mode `600`; back it up in a password manager, not in the repository.
- [ ] Serve HTTPS (reverse proxy or `server.tlsCertFile`) and import the chain into SAP STRUST.
- [ ] Set `contentServer.allowedNetworks` to your SAP servers; keep `signature: required` in production.
- [ ] Give people read access at most to the SAP content folder in SharePoint, and never edit its files by hand.
- [ ] Never put a privileged API key into browser code; call ZAKS Drive from your backend or use JWT.
- [ ] Keep `LOG_LEVEL=info` in production (debug shows request metadata).
- [ ] Update regularly (`git pull` and rebuild) and watch the repository's security advisories.
