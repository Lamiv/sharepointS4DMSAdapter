# Release process

Nothing in this repository publishes automatically. The release workflow ([`.github/workflows/release.yml`](../../.github/workflows/release.yml)) runs only when a maintainer starts it manually.

## Recommended first release

A **pre-release such as `v0.1.0-beta.1`**, not `v1.0.0`. Reasons: SAP scenarios beyond the protocol reports (DMS, GOS, ArchiveLink) are not validated, RSCMSTAT and RSCMSTHS have not been run, there has been no independent security review, and the performance figures come from an emulator.

Criteria for `v1.0.0` (all must be true):

- DMS originals verified end to end on a real SAP system, and the result documented.
- RSCMSTAT and RSCMSTHS run.
- Load test repeated against a real test tenant.
- An independent security review of the signature verification and authentication code.
- The module path settled (the repository is already renamed to `zaks-drive`).
- Name approved by the repository owner (done); an independent trademark search is optional but recommended for wide distribution.

## Prerequisites (owner approval)

1. **Name:** approved by the repository owner as approving authority. The brand is "ZAKS Drive", not the AKS-related name.
2. Repository renamed to `zaks-drive` ([repository rename](repository-rename.md)).
3. `LICENSE` unchanged (MIT, with its copyright notice).
4. A reviewed pull request for the branding work.

## Steps

1. Update `CHANGELOG.md`: move *Unreleased* entries under the new version and date.
2. Set the version string: the binary reports the value passed at build time (`-X main.version=...`); the Dockerfile takes the `VERSION` build argument.
3. Run the checks in [testing](testing.md): `gofmt`, `go vet`, `go test -race ./...`, `python3 scripts/check_docs.py`, Docker build and smoke test.
4. Tag: `git tag -a v0.1.0-beta.1 -m "ZAKS Drive 0.1.0-beta.1"` (only after approval) and push the tag.
5. Run the *Release* workflow manually, giving the tag. It builds Linux binaries for amd64 and arm64, writes SHA-256 checksums and attaches them to a draft GitHub release. The arm64 binary is cross-compiled and has not been tested on arm64 hardware.
6. Review the draft, then publish it. Publishing is a separate, manual decision.

## Not done

- Container image publishing to a registry: not set up. Decide on a registry first.
- SBOM and build provenance: evaluated, not implemented. They can be added to the release workflow (`actions/attest-build-provenance`, an SBOM generator) once publishing is decided.
- Signing of artifacts.
