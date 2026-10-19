# Governance

ZAKS Drive is open source (MIT) and built for the community: SAP developers, integration engineers, Go contributors and infrastructure engineers are welcome to improve the code, extend integrations, fix bugs, write documentation and help shape the direction of the project.

**Where things stand today.** The project currently has **one maintainer**, [@Lamiv](https://github.com/Lamiv) (copyright holder in the [LICENSE](../LICENSE)). There is no foundation, steering committee or established contributor community yet. This document describes how decisions are made now and how that can grow; it does not claim a structure that does not exist.

## Roles

### Maintainers

Maintainers:

- review and merge contributions;
- maintain the security and compatibility standards of the project;
- coordinate releases and approve them (releases are never automatic);
- make the final decision when consensus cannot be reached, and explain it in writing.

### Contributors

Anyone who takes part. Contributors:

- propose and implement improvements;
- review code and join design discussions, whether or not they have write access;
- receive attribution for accepted contributions (see [Recognition](#recognition)).

No agreement beyond the [MIT License](../LICENSE) and the [Code of Conduct](../.github/CODE_OF_CONDUCT.md) is required to contribute.

### Becoming a reviewer or maintainer

Repeated, high-quality contribution is the path. These are the expectations, so the process is transparent:

| Role | Typically means | What it adds |
|---|---|---|
| **Reviewer** | Several accepted non-trivial pull requests or substantial reviews over a few months, in an area such as the SAP Content Server interface, the Graph client, documentation or operations | Asked to review changes in that area; may be listed in `.github/CODEOWNERS` for it |
| **Maintainer** | A sustained record as reviewer: sound judgement on compatibility and security, constructive reviews, reliability, and agreement to follow the maintainer duties above | Merge rights, release approval, a vote in decisions |

A maintainer can invite a contributor, or a contributor can ask. Invitations are discussed among the existing maintainers and confirmed in the open (an issue or discussion) unless the person prefers otherwise. A maintainer who becomes inactive for a long period can step down or be moved to "emeritus" with thanks. These numbers are guidelines, not thresholds: nobody is promoted by counting commits.

## How decisions are made

| Kind of change | Where | Who decides |
|---|---|---|
| Bug fixes, documentation, small improvements | GitHub Issues and pull requests | A maintainer reviews and merges |
| Ideas, questions, early design | GitHub Discussions (once enabled) or an issue | Open conversation; no decision needed |
| Significant changes: new storage providers, authentication changes, protocol behaviour, new interfaces, deployment patterns | A public RFC pull request ([docs/rfcs](rfcs/README.md)) | Maintainers decide after open discussion; the outcome and reasons are recorded in the RFC |
| Breaking changes (routes, request or response contracts, configuration keys, storage layout, SAP protocol behaviour) | The RFC or pull request must include a **compatibility assessment** | Needs explicit maintainer approval and a CHANGELOG entry |
| Security vulnerabilities | Private disclosure ([SECURITY.md](../.github/SECURITY.md)); never a public issue | Maintainers, privately, then a coordinated fix and advisory |

Consensus is the goal. When it cannot be reached after open discussion, a maintainer decides and writes down why. Decisions can be revisited if circumstances change.

The project does not treat a proposal as a promise: accepting an RFC means "this design is acceptable", not "this will be built or scheduled".

## Compatibility and security standards

Changes are reviewed against these standards:

- API routes, contracts, authentication semantics, configuration keys and SAP protocol behaviour stay compatible unless a breaking change is approved as above.
- Every user-visible error has a catalogue code, cause and fix ([CONTRIBUTING](../.github/CONTRIBUTING.md)).
- Logs and errors follow the [logging policy](operations/security.md#logging-policy).
- Claims in the documentation match what has been verified. The README feature table separates verified, untested and not implemented.
- No SAP certification, production-readiness or security assurance is claimed without evidence.

## Recognition

Accepted contributions are credited:

- GitHub keeps the commit and pull request history and the contributors graph, and the author of a merged pull request stays the author of the commit.
- The [CHANGELOG](../CHANGELOG.md) names contributors for their changes, and release notes thank the people involved in the release.
- Test reports, reproduction steps and reviews count as contributions and are acknowledged the same way.

The project does not publish contributor counts or community badges that are not backed by real data.

## Repository safeguards

Recommended settings, which only the repository owner can apply (see [owner actions](#settings-that-need-the-owners-approval)):

- **Branch protection on `main`:** pull requests required, at least one approving review, the CI checks (`Test`, `Documentation links`, `Docker image`, CodeQL) required, no force pushes, linear history optional.
- **Secrets:** CI workflows use `pull_request` with read-only permissions, so pull requests from forks run without any secret. The CI needs no Microsoft 365 or SAP credentials. The release workflow runs only when a maintainer starts it manually, and only maintainers can.
- **Never in the repository:** secrets, tokens, certificates, private keys, tenant credentials and production configuration. `deploy/.env` and `deploy/config.yaml` are git-ignored.
- **Releases and compatibility-sensitive changes** need maintainer approval ([release process](development/release-process.md)).
- **License:** MIT. The copyright and permission notice must be preserved.

## Settings that need the owner's approval

Nothing below is enabled by the project's own files; the owner decides.

| Setting | Recommendation |
|---|---|
| GitHub Discussions | Enable, with the categories: General, Q&A, Ideas & Architecture, SAP Integration, Show and Tell. Use Discussions for questions and exploring ideas, Issues for actionable work, pull requests for implementations |
| Branch protection (or a ruleset) for `main` | As described above |
| Private vulnerability reporting | Enable (.github/SECURITY.md relies on it) |
| Labels | Create the labels listed in [`.github/labels.yml`](../.github/labels.yml) |
| CODEOWNERS enforcement | Turn on "Require review from Code Owners" if wanted |
| Dependabot alerts and security updates, CodeQL default setup or the provided workflow | Enable |
| Social preview and avatar | Upload `docs/images/social-preview.png` and `docs/images/logo-512.png` |
| Issue chooser link to Discussions | After enabling Discussions, add the link in `.github/ISSUE_TEMPLATE/config.yml` (a commented example is there) |

## Changing this document

Governance changes are proposed by pull request like any other significant change and need maintainer approval, with open discussion first.
