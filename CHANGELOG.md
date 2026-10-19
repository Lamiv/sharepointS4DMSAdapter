# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). The project has not had a tagged release yet; see the [release process](docs/development/release-process.md).

## [Unreleased]

### Added

- ZAKS Drive branding: `zaks-drive` binary and image, `version` command, usage text, start-up banner, OCI labels. See [branding and compatibility](docs/development/branding-and-compatibility.md).
- Error codes with cause and fix on every error, a generated [error catalogue](docs/error-codes.md), and `zaks-drive errors`.
- SharePoint location (`SHAREPOINT_SITE_URL`, `SHAREPOINT_LIBRARY`, `SHAREPOINT_ROOT_PATH`) read from `.env`; all configuration problems are reported together.
- OneDrive (`userId`) drive resolution in the Graph emulator and tests.
- The repository is renamed to `zaks-drive`, and the Go module path is now `github.com/Lamiv/zaks-drive`.
- One versioning scheme (SemVer from the git tag) for the CLI, image tag, OCI label and release artifacts; `scripts/version.sh`, `ZAKS_DRIVE_VERSION` in Compose, and CI/release checks. See [versioning](docs/development/release-process.md#versioning).
- Documentation restructure: index, quickstart, installation, configuration reference, architecture, SAP guides, Microsoft 365 guides, operations and development guides; `scripts/check_docs.py` link checker.
- Governance files: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `SUPPORT.md`, `ROADMAP.md`, issue and pull request templates, CI workflow and Dependabot configuration.

### Changed

- Documentation files moved: `docs/openapi.yaml` to `docs/api/`, SAP documents to `docs/sap/`, `docs/load-test-results.md` to `docs/development/`. `SETUP.md`, `docs/content-server.md` and `docs/load-test-results.md` remain as pointers.
- The Compose file builds the image `zaks-drive:latest`. The service, volume, ports, routes, configuration keys and metric names are unchanged.

### Deprecated

- The `/adapter` binary name inside the image is an alias kept for existing Compose files, health checks and scripts. It is planned for removal in a later minor release; this entry will state the version when decided.

### Security

- Reviewed documentation of controls; see [security](docs/operations/security.md) for what is implemented and what is not.
