# Testing

## What runs without any credentials

```bash
gofmt -l .                 # must print nothing
go vet ./...
go test -race -count=1 ./...
python3 scripts/check_docs.py      # documentation links and anchors
```

All of these run in CI ([`.github/workflows/ci.yml`](../../.github/workflows/ci.yml)) without Microsoft 365 or SAP credentials.

| Area | Where | What it covers |
|---|---|---|
| Configuration | `internal/config` | Defaults, environment overrides, every validation message |
| Error catalogue | `internal/apperr` | Codes, formatting, and that `docs/error-codes.md` is current |
| Graph client | `internal/graph` | Retries, throttling, error classification, drive resolution (site, library, user drive) |
| Storage and transfer | `internal/storage`, `internal/transfer` | Names and limits, paging cursors, uploads, upload sessions, memory budget |
| REST API | `internal/restapi` | Routes, authentication, permissions, conditional requests, ranges, problem responses |
| Content Server | `internal/contentrepo` | Every command, secKey verification for DSA, RSA and ECDSA, certificates, modes, rollback, concurrency, partial failures, reserved folder; benchmarks for signature cost |
| Emulator | `internal/graphmock` | Used by the tests above |

All of them run against `internal/graphmock`, not against Microsoft. Passing tests prove the code's behaviour against the emulator's model of Graph.

## What needs real systems (not run in CI)

| Test | How | Status |
|---|---|---|
| Real SharePoint access | `zaks-drive check` or `scripts/check_access.py` with a tenant | Run manually against a real tenant during development; results are not part of CI |
| SAP standard reports | RSCMSTH0-3, RSCMSTA0, RSCMSTAC from SE38 | Passed once (2026-10-16); see [SAP](../sap/connect-s4hana-to-sharepoint.md#5-test-with-sap-standard-tools) |
| DMS, GOS, ArchiveLink scenarios | In SAP | Not validated |
| End to end smoke against a running server | `scripts/e2e-smoke.sh` | Needs a running server and key; works with the emulator stack |
| Load tests | `loadtest/run.sh` | [Load testing](load-testing.md) |

## Smoke test with the emulator

```bash
docker compose -f loadtest/docker-compose.yml up -d --build graphmock adapter
API_KEY=loadtest-key CS_REPO=Z1 scripts/e2e-smoke.sh
docker compose -f loadtest/docker-compose.yml down -v
```

## Writing tests

Put tests next to the code. Use the emulator for Graph behaviour; if you add a Graph quirk to the emulator, state in a comment where it was observed. Do not add tests that need real credentials to the default `go test` run.
