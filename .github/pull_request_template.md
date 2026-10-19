## What and why

<!-- What changes, and what problem does it solve? Link the issue. -->

## Checklist

- [ ] Tests added or updated (a bug fix has a test that failed before)
- [ ] `go test -race ./...`, `gofmt` and `go vet` pass
- [ ] New or changed errors use a code from `internal/apperr/catalogue.go` and `docs/error-codes.md` is regenerated
- [ ] Docs updated (`README.md`, `SETUP.md`, `docs/…`)
- [ ] No secrets, tenant names or customer data in code, tests, logs or screenshots

## How it was tested

<!-- Emulator only, or also real SharePoint / real SAP (which reports)? Say what you could not test. -->
