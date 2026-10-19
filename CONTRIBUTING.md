# Contributing

Thank you for helping. This project (ZAKS Drive) connects SAP S/4HANA and your own applications to SharePoint Online, so correctness and clear errors matter more than clever code. Bug reports, documentation fixes and test results from real SAP systems are all welcome.

## Before you start

- **Questions and ideas:** open an issue and describe what you want to do and why. For anything larger than a fix, agree on the approach first so nobody builds something that cannot be merged.
- **Bugs:** include the output of `docker compose -f deploy/docker-compose.yml exec adapter /zaks-drive check`, the **error code** and **request ID** from the response or log, and the ZAKS Drive version (`zaks-drive version`). **Never paste secrets** (client secret, API keys, admin token, `.env`, signed URLs).
- **Security problems:** do not open a public issue; follow [SECURITY.md](SECURITY.md).

## Set up (no Go installation needed)

Everything builds and tests inside Docker:

```bash
git clone https://github.com/Lamiv/zaks-drive.git
cd zaks-drive

# unit and integration tests (the whole stack against a Microsoft Graph emulator), with the race detector
docker run --rm -v "$PWD:/src" -w /src golang:1.25 go test -race -count=1 ./...

# formatting and static checks
docker run --rm -v "$PWD:/src" -w /src golang:1.25 sh -c 'test -z "$(gofmt -l .)" && go vet ./...'
```

With Go 1.25 installed locally, `go test -race ./...` does the same. On Windows with Git Bash, prefix Docker commands that mount paths with `MSYS_NO_PATHCONV=1` and use `$(pwd -W)` instead of `$PWD`.

To try ZAKS Drive without a Microsoft tenant, use the emulator stack from the [quickstart](docs/quickstart.md).

## Project layout

| Path | What it is |
|---|---|
| `cmd/adapter` | The binary: server, `check`, `certs`, `errors` commands |
| `cmd/graphmock`, `internal/graphmock` | Microsoft Graph emulator for tests and load tests |
| `internal/graph` | Graph client: retries, throttling, upload sessions, error classification |
| `internal/transfer` | Upload and download engine with a memory budget |
| `internal/storage` | Repositories, folders, paging cursors, reserved paths |
| `internal/restapi` | Document REST API |
| `internal/contentrepo` | SAP Content Server HTTP interface, `secKey` verification, certificates |
| `internal/apperr` | **The error-code catalogue** (see below) |
| `internal/accesscheck` | `zaks-drive check` |
| `docs/`, `loadtest/`, `scripts/` | Documentation, k6 load tests, smoke test scripts |

## Rules for changes

1. **Every user-visible error has a code, a reason and a fix.** Add new errors to `internal/apperr/catalogue.go` (area, code, HTTP status, title, cause, fix) and create them with `apperr.New(apperr.YourCode, "what happened, with the concrete values")`. Do not return bare strings to API callers, to SAP or to operators. Then regenerate the documentation:
   ```bash
   docker run --rm -v "$PWD:/src" -w /src golang:1.25 sh -c 'go run ./cmd/adapter errors > docs/error-codes.md'
   ```
   A test fails if `docs/error-codes.md` is out of date.
2. **Test what you change.** Fix a bug by first writing the test that reproduces it. The emulator is deliberately strict about Graph behaviour we have seen in production (for example, `$select` dropping the download URL); add such quirks to it rather than loosening a test.
3. **Never log or return secrets.** Not the client secret, API keys, admin token, signed URLs (`secKey` is redacted) or file contents.
4. **SAP protocol changes follow SAP's documentation.** Cite the section in the code comment or commit message. If SAP's behaviour differs from its documentation, describe what the real system did and keep the lenient behaviour covered by a test.
5. **Keep it small and readable.** Match the surrounding code. Comments explain *why*.
6. **Update the docs in the same change:** `README.md`, `SETUP.md`, `docs/content-server.md`, `docs/openapi.yaml` as applicable.

## Testing against real systems

Before a larger change is merged it should have been exercised against real SharePoint, using a test site:

```bash
docker compose -f deploy/docker-compose.yml exec adapter /zaks-drive check      # access checklist
API_KEY=<key> bash scripts/e2e-smoke.sh                                        # end-to-end checks
```

For SAP-facing changes run the SAP test reports `RSCMSTH0` to `RSCMSTH3`, `RSCMSTA0` and `RSCMSTAC` against a test repository, and note the result in the pull request.

## Commits and pull requests

- One logical change per pull request, with a message that says what changed **and why**.
- All checks pass (`gofmt`, `go vet`, `go test -race`, the Docker build). CI runs these for every pull request.
- Fill in the pull request template. Mention anything you could not test.

By contributing you agree that your contribution is licensed under the [MIT License](LICENSE). Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).
