# Repository rename plan

**Status: not executed.** Renaming the GitHub repository, changing its settings, publishing releases and pushing require the owner's explicit approval. This page is the checklist for when that approval is given.

Current repository: `Lamiv/sharepointS4DMSAdapter`. Proposed: `zaks-drive` (the owner or organisation to be decided).

## Before renaming

1. Trademark clearance for "ZAKS Drive" and written permission from the organisation behind the AKS-related name ([release process](release-process.md)).
2. Decide the owner (personal account or an organisation).
3. Merge the branding work through a reviewed pull request.
4. Decide whether the Go module path changes (below).

## Rename

1. GitHub: *Settings → General → Repository name*. GitHub redirects web and `git` URLs from the old name until another repository takes it, so existing clones and links keep working; do not rely on this long term.
2. Locally: `git remote set-url origin https://github.com/<owner>/zaks-drive.git`.
3. Update, in one commit:
   - the clone URLs in `README.md`, `docs/installation.md`, `docs/quickstart.md`;
   - `DocsURL` in `internal/apperr/apperr.go`, then regenerate `docs/error-codes.md` (`go run ./cmd/adapter errors > docs/error-codes.md`);
   - the OCI source label in the `Dockerfile`;
   - badges, issue-template links and `.github/ISSUE_TEMPLATE/config.yml`.
4. Search for the old name: `git grep -n 'sharepointS4DMSAdapter'`.
5. Tell users: deployments that pull with `git pull` keep working through the redirect; update the remote URL.

## Go module path

The module is `sharepointadapter`, which is not an import path anyone depends on. Leave it until after the rename. If changed later, change it in `go.mod` and every import in one commit and run the full test suite. Nothing else depends on it.

## After renaming

- Re-check Dependabot, Actions permissions and branch protection.
- Add the social preview image (`docs/images/social-preview.svg` rendered at 1280x640) in *Settings → General → Social preview*.
- Set the description and topics ([discoverability](../../README.md#documentation)); enabling Discussions is an owner decision.
- Run `python3 scripts/check_docs.py` and the CI workflow once.
