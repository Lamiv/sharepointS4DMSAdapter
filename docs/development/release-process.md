# Release process

Nothing in this repository publishes automatically. The release workflow ([`.github/workflows/release.yml`](../../.github/workflows/release.yml)) runs only when a maintainer starts it manually.

## Recommended first release

A **pre-release such as `v0.1.0-beta.1`**, not `v1.0.0`. Reasons: DMS originals were verified end to end on one system but the SAP customising used is not yet documented, GOS and ArchiveLink are not tested, RSCMSTAT and RSCMSTHS have not been run, there has been no independent security review, and the performance figures come from an emulator.

Criteria for `v1.0.0` (all must be true):

- DMS originals verified end to end on a real SAP system (done once) **and** the SAP customising documented so it can be reproduced; GOS and ArchiveLink tested or explicitly scoped out.
- RSCMSTAT and RSCMSTHS run.
- Load test repeated against a real test tenant.
- An independent security review of the signature verification and authentication code.
- The module path settled (the repository is already renamed to `zaks-drive`).
- Name approved by the repository owner (done); an independent trademark search is optional but recommended for wide distribution.

## Versioning

One scheme, one source of truth: the **git tag**.

| Where | Form | Example |
|---|---|---|
| Git tag | `vMAJOR.MINOR.PATCH[-prerelease]` ([SemVer 2.0.0](https://semver.org/spec/v2.0.0.html)) | `v0.1.0-beta.1` |
| CLI (`zaks-drive version`, `-version`, the start-up log, `GET /version`) | the tag without `v` | `ZAKS Drive 0.1.0-beta.1 (go1.25, linux/amd64)` |
| Docker image tag and `org.opencontainers.image.version` label | the same string | `zaks-drive:0.1.0-beta.1` |
| Release artifacts | `zaks-drive_<version>_linux_<arch>` plus `SHA256SUMS` | `zaks-drive_0.1.0-beta.1_linux_amd64` |

- **Pre-1.0:** `0.y.z` means the interfaces can still change; breaking changes are listed in the [CHANGELOG](../../CHANGELOG.md) and bump `y`. From `1.0.0`, breaking changes bump `MAJOR`.
- **What is versioned:** the program. The REST API has its own major version in its URL (`/api/v1`) and in `docs/api/openapi.yaml`; it does not move with every release. SAP Content Server compatibility is the protocol version (`pVersion` 0045 to 0047), not the product version.
- **Builds without a tag** get `<last tag>-<n>-g<commit>[-dirty]` from `scripts/version.sh`, or `dev` when built without a version.
- **Stamping:** the Dockerfile takes the `VERSION` build argument; Compose passes `ZAKS_DRIVE_VERSION`:

  ```bash
  export ZAKS_DRIVE_VERSION=$(scripts/version.sh)
  docker compose -f deploy/docker-compose.yml up -d --build      # builds and runs zaks-drive:<version>
  ```

- **`latest` is not a version.** Without `ZAKS_DRIVE_VERSION`, Compose tags your own local build `zaks-drive:latest`. That tag moves with every build and says nothing about stability. Pin a version in production and in your own notes. No image is published to a registry yet, so there is no official `latest`.
- **CI and the release workflow check the scheme:** CI builds an image with a test version and verifies that both binaries and the OCI label report it; the release workflow rejects tags that are not SemVer, and builds with the tag minus `v`.

## Prerequisites (owner approval)

1. **Name:** approved by the repository owner as approving authority. The brand is "ZAKS Drive", not the AKS-related name.
2. Repository renamed to `zaks-drive` ([repository rename](repository-rename.md)).
3. `LICENSE` unchanged (MIT, with its copyright notice).
4. A reviewed pull request for the branding work.

## Steps

1. Update `CHANGELOG.md`: move *Unreleased* entries under the new version and date.
2. Nothing to edit: the version comes from the tag ([versioning](#versioning)).
3. Run the checks in [testing](testing.md): `gofmt`, `go vet`, `go test -race ./...`, `python3 scripts/check_docs.py`, Docker build and smoke test.
4. Tag: `git tag -a v0.1.0-beta.1 -m "ZAKS Drive 0.1.0-beta.1"` (only after approval) and push the tag.
5. Run the *Release* workflow manually, giving the tag. It builds Linux binaries for amd64 and arm64 named `zaks-drive_<version>_linux_<arch>`, writes SHA-256 checksums and attaches them to a draft GitHub release. The arm64 binary is cross-compiled and has not been tested on arm64 hardware.
6. Review the draft, then publish it. Publishing is a separate, manual decision.

## Not done

- Container image publishing to a registry: not set up. Decide on a registry first.
- SBOM and build provenance: evaluated, not implemented. They can be added to the release workflow (`actions/attest-build-provenance`, an SBOM generator) once publishing is decided.
- Signing of artifacts.
