# Contributing to ZAKS Drive

ZAKS Drive is open source and built for the community. Whether you are an SAP developer, an integration engineer, a Go developer or someone who runs infrastructure, you are welcome to fix bugs, improve the code, extend integrations, write documentation, test on your own SAP system and help shape where the project goes. The [governance](GOVERNANCE.md) page explains how decisions are made.

You do not need to be an expert in everything. SAP people often know the protocol and not Go; Go people often know the code and not SAP. Both are needed.

## Ways to contribute

| You can... | Start here |
|---|---|
| **Fix a bug or improve performance** | Pick an issue labelled [`good first issue`](https://github.com/Lamiv/zaks-drive/labels/good%20first%20issue) or [`help wanted`](https://github.com/Lamiv/zaks-drive/labels/help%20wanted) |
| **Test on your SAP landscape** | Run the SAP test reports or a DMS, GOS or ArchiveLink scenario and file an [SAP test report](https://github.com/Lamiv/zaks-drive/issues/new?template=sap_test_report.md): the release, what you configured, what worked |
| **Improve the documentation** | Fix what confused you, add deployment guides, ABAP or Fiori examples, troubleshooting knowledge. Documentation changes are first-class contributions |
| **Discuss ideas** | GitHub Discussions (when enabled) or an issue |
| **Propose a larger change** | Write an [RFC](docs/rfcs/README.md) |
| **Help others** | Answer questions, reproduce issues, review pull requests |
| **Report a vulnerability** | Privately, as described in [SECURITY.md](SECURITY.md); never as a public issue |

Use **Discussions** for questions and exploring ideas, **Issues** for actionable work (a bug, a task, a test report), and **pull requests** for implementations. For anything larger than a fix, say what you plan before you build it, so nobody builds something that cannot be merged.

Please read the [Code of Conduct](CODE_OF_CONDUCT.md).

## Your first contribution in six steps

1. **Fork** the repository on GitHub (the *Fork* button), then clone your fork:
   ```bash
   git clone https://github.com/<your-user>/zaks-drive.git
   cd zaks-drive
   git remote add upstream https://github.com/Lamiv/zaks-drive.git
   ```
2. **Create a branch** for one change:
   ```bash
   git fetch upstream
   git switch -c fix/short-description upstream/main
   ```
3. **Set up** (below), then make the change.
4. **Run the checks** (below). They are the same ones CI runs.
5. **Commit** with a message that says what changed and why, and **push** to your fork:
   ```bash
   git push -u origin fix/short-description
   ```
6. **Open a pull request** against `Lamiv/zaks-drive` `main` and fill in the template. A maintainer will review; expect questions and requests for changes, which is normal. Mention anything you could not test.

If you are unsure, open the pull request as a draft and ask.

## Development environment

You need either **Docker** or **Go 1.25+**. No Microsoft 365 tenant and no SAP system are needed for development: ZAKS Drive ships with a Microsoft Graph emulator.

**With Docker only** (nothing to install):

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.25 go test -race -count=1 ./...
docker run --rm -v "$PWD:/src" -w /src golang:1.25 sh -c 'test -z "$(gofmt -l .)" && go vet ./...'
```

On Windows with Git Bash, prefix such commands with `MSYS_NO_PATHCONV=1` and use `$(pwd -W)` instead of `$PWD`.

**With Go installed:**

```bash
go build ./...
go vet ./...
gofmt -l .                 # must print nothing
go test -race -count=1 ./...   # -race needs cgo; without a C compiler use: go test -count=1 ./...
python3 scripts/check_docs.py  # documentation links and anchors
```

### Running against the Graph emulator

The unit and integration tests already run the whole stack against the emulator (`internal/graphmock`). To try ZAKS Drive by hand without any credentials:

```bash
docker compose -f loadtest/docker-compose.yml up -d --build graphmock adapter
curl -s localhost:9090/readyz
API_KEY=loadtest-key CS_REPO=Z1 bash scripts/e2e-smoke.sh
docker compose -f loadtest/docker-compose.yml down -v
```

More: [quickstart](docs/quickstart.md), [local development](docs/development/local-development.md), [testing](docs/development/testing.md).

### Real systems are optional

Tests that need real Microsoft 365 or SAP credentials are not part of CI and are not required for a pull request. If you do have a test tenant or an SAP system, say in the pull request what you ran (`zaks-drive check`, SAP reports `RSCMSTH0` to `RSCMSTH3`, `RSCMSTA0`, `RSCMSTAC`) and the result. That is valuable evidence.

## Project layout

| Path | What it is |
|---|---|
| `cmd/adapter` | The `zaks-drive` binary: server, `check`, `certs`, `errors`, `version` |
| `cmd/graphmock`, `internal/graphmock` | Microsoft Graph emulator for tests and load tests |
| `internal/graph` | Graph client: retries, throttling, upload sessions, error classification |
| `internal/transfer` | Upload and download engine with a memory budget |
| `internal/storage` | Repositories, folders, paging cursors, reserved paths |
| `internal/restapi` | The REST API |
| `internal/contentrepo` | SAP Content Server HTTP interface, `secKey` verification, certificates |
| `internal/apperr` | **The error-code catalogue** |
| `internal/safelog` | **The logging policy** (what may be logged) |
| `internal/accesscheck` | `zaks-drive check` |
| `docs/`, `loadtest/`, `scripts/` | Documentation, k6 load tests, smoke test and link-check scripts |

## Code style and security practices

1. **Every user-visible error has a code, a reason and a fix.** Add new errors to `internal/apperr/catalogue.go` (area, code, HTTP status, title, cause, fix) and create them with `apperr.New(apperr.YourCode, "what happened, with the concrete values")`. Do not return bare strings to API callers, to SAP or to operators. Regenerate the documentation:
   ```bash
   go run ./cmd/adapter errors > docs/error-codes.md
   ```
   A test fails if `docs/error-codes.md` is out of date.
2. **Never log or return secrets, and never log text from outside.** Not the client secret, API keys, admin token, `Authorization` values, signed URLs (`secKey`), Graph or Entra error text, request URLs or file contents. Log with `safelog.Err(err)` and the error's code, status and title; show people text only through `safelog.Redact`. A test fails the build if a log call takes `err.Error()` or an error's `Detail`. See the [logging policy](docs/operations/security.md#logging-policy).
3. **Test what you change.** Fix a bug by first writing the test that reproduces it. The emulator is deliberately strict about Graph behaviour seen in production (for example `$select` dropping the download URL); add such quirks to it instead of loosening a test.
4. **SAP protocol changes follow SAP's documentation.** Cite the section in a comment or the commit message. If a real SAP system differs from the documentation, describe what it did and keep the lenient behaviour covered by a test.
5. **Keep compatibility.** Do not change API routes, request or response contracts, authentication behaviour, configuration keys, storage paths or SAP protocol behaviour in an ordinary pull request. If a change is needed, say so in the pull request and expect an [RFC](docs/rfcs/README.md) and a compatibility assessment.
6. **Keep it small and readable.** Match the surrounding code, run `gofmt`, and write comments that explain *why*.
7. **No secrets in the repository.** `deploy/.env` and `deploy/config.yaml` are git-ignored; keep real values out of tests, examples and screenshots.

## Documentation

Documentation changes are welcome on their own. Fix what confused you, add an ABAP or Fiori example, write down a troubleshooting finding.

- Keep claims honest: mark what you verified and on which release, and what you did not. The [README feature table](README.md#what-is-verified) is the reference for status.
- Use "ZAKS Drive" (with a space) in prose and `zaks-drive` in commands and identifiers.
- Do not copy text or logos from SAP or Microsoft; reference their products by name only.
- Run `python3 scripts/check_docs.py` to check links and anchors.
- If you change behaviour, update the docs in the same pull request: the relevant page under `docs/`, `docs/api/openapi.yaml` for the REST API (a test compares it to the routes), and the [CHANGELOG](CHANGELOG.md).

## Reviewing

Reviewing is a contribution. You do not need merge rights to comment on a pull request: reproduce the problem, read the diff, run it, and say what you found. Be kind and specific.

## Commits and pull requests

- One logical change per pull request, with a message that says what changed **and why**.
- All checks pass: `gofmt`, `go vet`, `go test`, the documentation link check and the Docker build. CI runs them on every pull request, from forks too, with no access to any secret.
- Maintainers review and merge. Changes that touch compatibility, security or releases need maintainer approval ([governance](GOVERNANCE.md)).

## Recognition

Your commits keep your name. The [CHANGELOG](CHANGELOG.md) and release notes credit contributors, and test reports, reviews and documentation count the same as code.

By contributing you agree that your contribution is licensed under the [MIT License](LICENSE).
