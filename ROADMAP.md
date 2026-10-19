# Roadmap

This is a statement of direction, not a commitment. There are no dates. Items move when someone does the work; contributions are welcome ([CONTRIBUTING](CONTRIBUTING.md)).

## Now: reach a trustworthy first pre-release

- Validate DMS originals end to end on a real SAP system (document type, storage category, KPro) and document the confirmed steps.
- Run SAP reports RSCMSTAT and RSCMSTHS.
- Test GOS attachments and ArchiveLink.
- Repeat the load test against a real test tenant.
- Publish a `v0.1.0-beta.x` pre-release ([release process](docs/development/release-process.md)).

## Next

- Entra authentication with a certificate instead of a client secret.
- Container image publishing, with SBOM and build provenance.
- Per-key rate limiting.
- Tested arm64 builds.
- A tested high-availability recipe (several instances behind a load balancer).
- Kubernetes manifests or a Helm chart.

## Later, if there is demand

- `attrSearch` (print-list attribute search).
- A read-only frontend for browsing SAP documents.
- Other storage back ends behind the storage interface.

## Not planned

- CMIS: SharePoint does not support it.
- Mapping SAP users to SharePoint permissions.
- Replacing SAP's own authorisation.

Ideas and votes: open a [feature request](https://github.com/Lamiv/zaks-drive/issues/new?template=feature_request.md).
