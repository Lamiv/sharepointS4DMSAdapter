# Load testing

The load tests measure ZAKS Drive itself, not Microsoft: they run against the Graph emulator with 60 to 140 ms of added latency per Graph call. They do not show how real Microsoft Graph, tenant throttling or your network will behave, so repeat them against a test tenant before sizing a production system.

## Run

```bash
LEVELS="10 25 50 100" DURATION=60s ADAPTER_CPUS=2 ADAPTER_MEM=1g ./loadtest/run.sh
SCENARIO=contentserver.js OUT=results/contentserver ./loadtest/run.sh      # SAP Content Server interface
```

`loadtest/run.sh` starts the emulator and an adapter container (resource-capped) under the Compose project `zaks-drive-lt`, runs [k6](https://k6.io) at each level and writes `loadtest/results/REPORT.md` (git-ignored). Docker is required; k6 runs as a container.

| File | Purpose |
|---|---|
| `loadtest/scenario.js` | REST API mix: downloads, metadata, listing, uploads including resumable |
| `loadtest/contentserver.js` | SAP Content Server commands (with `signature: none`; signature cost is measured by Go benchmarks) |
| `loadtest/docker-compose.yml` | Emulator, adapter, k6 |

Environment variables: `LEVELS`, `DURATION`, `SCENARIO`, `OUT`, `ADAPTER_CPUS`, `ADAPTER_MEM`, `MOCK_LATENCY_MS`, `MOCK_THROTTLE_RATE`, `METADATA_CACHE_TTL`.

## Results

Published results, with setup and caveats: [load test results](load-test-results.md). Do not quote them as performance of your deployment.
