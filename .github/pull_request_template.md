## What and why

<!-- What changes, and what problem does it solve? Link the issue. -->

## Checklist

- [ ] A small, compatible change, or it links an accepted [RFC](../docs/rfcs/README.md) with a compatibility assessment

- [ ] Tests added or updated (a bug fix has a test that failed before)
- [ ] `go test ./...`, `gofmt`, `go vet` and `python3 scripts/check_docs.py` pass (CI runs them)
- [ ] Logging follows the [logging policy](../docs/operations/security.md#logging-policy): no raw errors, headers, URLs or secrets in logs
- [ ] New or changed errors use a code from `internal/apperr/catalogue.go` and `docs/error-codes.md` is regenerated
- [ ] Docs updated (`README.md`, `docs/…`; run `python3 scripts/check_docs.py`)
- [ ] No secrets, tenant names or customer data in code, tests, logs or screenshots

## How it was tested

<!-- Emulator only, or also real SharePoint / real SAP (which reports)? Say what you could not test. -->
