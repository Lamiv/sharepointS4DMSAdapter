# Repository rename

**Status: done.** The repository owner renamed the GitHub repository from `Lamiv/sharepointS4DMSAdapter` to `Lamiv/zaks-drive`. GitHub redirects the old web and `git` URLs, so existing clones keep working; update them anyway:

```bash
git remote set-url origin https://github.com/Lamiv/zaks-drive.git
```

## Name approval

The repository owner, as approving authority, approved the name "ZAKS Drive" and its use. The brand is "ZAKS Drive"; it does not use the AKS-related name as a product brand. No independent trademark search is recorded here; run one if you need legal certainty in your jurisdiction.

## What was updated after the rename

- Clone URLs in `README.md`, `CONTRIBUTING.md`, `docs/installation.md`, `docs/quickstart.md`.
- `DocsURL` in `internal/apperr/apperr.go` (error responses link to the catalogue), the OCI source label in the `Dockerfile`, the issue template links, `SUPPORT.md`, `ROADMAP.md`, the CI badge.

Check for leftovers with `git grep -n 'sharepointS4DMSAdapter'`; only this page should mention the old name.

## Go module path

The module path changed from `sharepointadapter` to `github.com/Lamiv/zaks-drive` (`go.mod` and every import, in one commit; the full test suite passes). The module is not published as a library, so nothing external depends on the old path. The label `sharepointadapter/cursor` in `cmd/adapter/main.go` is HMAC key material for paging cursors and was deliberately kept.

## Remaining owner steps

- Confirm Dependabot, Actions permissions and branch protection still apply.
- Set the About description and topics, and upload `docs/images/social-preview.png` (Settings, General, Social preview).
- Enable private vulnerability reporting, which [SECURITY.md](../../SECURITY.md) relies on. Enabling Discussions is an owner decision.
- Run `python3 scripts/check_docs.py` and the CI workflow once after the first push.
