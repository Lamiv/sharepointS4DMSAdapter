# Load test results: Document REST API

**Date:** 2026-10-08 · **Tool:** k6 (`loadtest/scenario.js`) · **Runner:** `loadtest/run.sh`

## Setup

| | |
|---|---|
| Adapter | Production image, capped at **2 CPU / 1 GiB RAM** |
| Backend | `graphmock` Graph emulator, adding **60–140 ms latency to every Graph API call** to resemble Microsoft Graph |
| Load | Constant 10 / 25 / 50 / 100 concurrent clients, 60 s per level, 0.1–0.5 s think time |
| Mix | 35% download 200 KiB · 5% download 12 MiB · 20% metadata · 10% list attachments · 20% upload 200 KiB · 7% upload 1 MiB · 3% upload 12 MiB (resumable session, 3 chunks) |
| Pass criteria | errors < 1%, checks > 99%, p95 download 200 KiB < 1.5 s, p95 metadata < 1 s |

Each level was run twice. The first run uses the default 60 s metadata cache, so repeat reads of hot items such as images skip a Graph round trip. The second run turns the cache off (`METADATA_CACHE_TTL=0s`), so every read pays full Graph latency. That second run is the worst case.

## Results: all levels passed, 0 errors

Latency is median / p95 in milliseconds, measured end to end at the client.

### Metadata cache on (default)

| Clients | Requests | Req/s | Errors | Download 200K | Download 12M | Metadata | List | Upload 200K | Upload 1M | Upload 12M | MB/s up / down |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 10 | 1712 | 27.0 | 0.00% | 1 / 2 | 5 / 8 | 0 / 1 | 102 / 139 | 99 / 138 | 100 / 137 | 401 / 491 | 13.5 / 22.0 |
| 25 | 4299 | 67.0 | 0.00% | 1 / 1 | 5 / 7 | 0 / 1 | 105 / 138 | 101 / 138 | 100 / 138 | 410 / 488 | 33.3 / 46.5 |
| 50 | 8571 | 133.7 | 0.00% | 1 / 1 | 5 / 7 | 0 / 1 | 105 / 138 | 102 / 138 | 102 / 139 | 422 / 489 | 66.4 / 94.3 |
| 100 | 17077 | 266.7 | 0.00% | 1 / 1 | 6 / 8 | 0 / 1 | 102 / 138 | 101 / 138 | 101 / 139 | 414 / 490 | 133.9 / 195.3 |

| Clients | Peak adapter CPU (100% = 1 core) | Peak adapter memory |
|---|---|---|
| 10 | 7% | 50 MiB |
| 25 | 13% | 111 MiB |
| 50 | 19% | 177 MiB |
| 100 | 44% | 359 MiB |

### Metadata cache off (worst case)

| Clients | Requests | Req/s | Errors | Download 200K | Download 12M | Metadata | List | Upload 200K | Upload 1M | Upload 12M | MB/s up / down |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 10 | 1483 | 23.3 | 0.00% | 102 / 138 | 110 / 141 | 103 / 137 | 101 / 137 | 97 / 136 | 109 / 140 | 428 / 489 | 12.3 / 16.9 |
| 25 | 3674 | 57.5 | 0.00% | 102 / 137 | 105 / 142 | 103 / 138 | 105 / 139 | 103 / 139 | 97 / 138 | 423 / 498 | 30.4 / 37.1 |
| 50 | 7306 | 113.9 | 0.00% | 102 / 138 | 104 / 141 | 102 / 138 | 102 / 137 | 100 / 139 | 103 / 139 | 414 / 495 | 58.2 / 74.7 |
| 100 | 14646 | 228.5 | 0.00% | 102 / 138 | 108 / 143 | 101 / 138 | 102 / 137 | 102 / 138 | 103 / 139 | 410 / 494 | 108.3 / 156.7 |

| Clients | Peak adapter CPU | Peak adapter memory |
|---|---|---|
| 10 | 7% | 65 MiB |
| 25 | 12% | 105 MiB |
| 50 | 18% | 169 MiB |
| 100 | 34% | 343 MiB |

Throughout both runs, every overload counter stayed at zero: `http_rejected_total`, `graph_concurrency_wait`, `transfer_buffer_wait` and `graph_retries`. The adapter fetched one access token per run.

## What the numbers show

- **The adapter adds almost no latency.** Every latency figure stays flat from 10 to 100 clients and matches the emulator's 60–140 ms per Graph call. A 12 MiB upload makes four Graph calls (create session plus three chunks), which gives about 410 ms.
- **Throughput scales linearly.** Requests per second roughly double with each doubling of clients, from 27 to 267 req/s. At 100 clients the adapter moves about 330 MB/s combined while using at most 0.44 of a core.
- **Memory is bounded.** About 3.5 MiB per concurrent client at 100 clients, well below the 1 GiB limit. Large uploads stream through one 5 MiB chunk buffer each, and the global memory budget (512 MiB by default) blocks further buffer allocation rather than letting the process exceed it.
- **The cache helps a lot with hot content.** For repeatedly viewed images and documents, downloads drop from about 100 ms to about 1 ms of adapter-side latency, because each one becomes a single SharePoint content fetch.

## Caveats: what this test does not prove

1. **This is not real SharePoint.** The emulator does not reproduce SharePoint's per-app and per-tenant throttling or real network bandwidth to Microsoft 365.
   - In production, throttling (HTTP 429) will cap throughput long before the adapter does. The adapter honours `Retry-After` with one back-off for the whole process, and caps concurrent Graph calls with `graph.maxConcurrency`.
   - The 12 MiB transfer times will be bounded by the VM's Internet bandwidth.
2. **One test-harness bug was found and fixed.** In the first attempt, 100 clients showed 20–26% errors. The cause was the emulator filling its 8 GiB RAM disk with stored uploads: about 14 GB were uploaded across the run. The adapter itself returned no errors and rejected nothing. The emulator now keeps the first 1 GiB of content (`MOCK_RETAIN_BYTES`). After that it checks upload sizes but discards the bytes. Results above come from the corrected harness.
3. **Recommended next step: a pilot against the real tenant.** Point the adapter at a test site with `deploy/config.yaml`, then run:
   ```bash
   docker run --rm -v "$PWD/loadtest:/scripts" -w /scripts grafana/k6 run \
     -e BASE_URL=http://<vm>:8080/api/v1 -e API_KEY=<key> -e VUS=25 -e DURATION=120s scenario.js
   ```
   Increase VUs step by step while watching `adapter_graph_retries_total{reason="throttled"}`.

## Reproduce

```bash
./loadtest/run.sh                                          # cache on  -> loadtest/results/REPORT.md
METADATA_CACHE_TTL=0s OUT=results/cache-off ./loadtest/run.sh
# knobs: LEVELS="10 25 50 100" DURATION=60s ADAPTER_CPUS=2 ADAPTER_MEM=1g MOCK_LATENCY_MS=60-140 MOCK_THROTTLE_RATE=0
```
