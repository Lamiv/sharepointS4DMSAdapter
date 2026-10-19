# Load test results

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

## Document REST API: all levels passed, 0 errors

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

## SAP Content Server HTTP interface

These runs used the same setup: an adapter capped at 2 CPU / 1 GiB, the Graph emulator at 60–140 ms per call, and 60 s per level.

Mix, shaped like KPro/ArchiveLink traffic:
- 25% `info`
- 30% `get` 200 KiB
- 5% `get` with a 50 KB offset range
- 5% `docGet`
- 18% `create` PUT (200 KiB / 1 MiB)
- 8% `create` POST multipart with 2 components
- 3% `create` 8 MiB (upload session)
- 6% `delete`

The repository ran with `signature: none`, because k6 cannot produce SAP's PKCS#7 secKeys. Verification costs about **0.26 ms per request with DSA** (SAP's default) and **0.035 ms with RSA-2048**, measured by `go test -bench Verify ./internal/contentrepo/`. At 225 req/s that is under 6% of one core.

### Metadata cache on (default, 60 s)

| Clients | Requests | Req/s | Errors | info | get 200K | get range | docGet | create PUT | create POST (2 comps) | create 8M | delete | MB/s up / down |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 10 | 1454 | 20.9 | 0.00% | 0 / 21 | 1 / 2 | 1 / 2 | 1 / 2 | 240 / 346 | 442 / 576 | 453 / 562 | 306 / 375 | 7.6 / 1.6 |
| 25 | 3742 | 56.1 | 0.00% | 0 / 1 | 1 / 1 | 1 / 1 | 1 / 2 | 216 / 274 | 408 / 482 | 414 / 482 | 303 / 360 | 21.5 / 4.5 |
| 50 | 7491 | 111.6 | 0.00% | 0 / 1 | 1 / 1 | 1 / 1 | 1 / 1 | 216 / 264 | 407 / 475 | 415 / 488 | 302 / 369 | 39.0 / 8.9 |
| 100 | 14974 | 224.5 | 0.00% | 0 / 1 | 1 / 1 | 1 / 1 | 1 / 1 | 216 / 265 | 404 / 475 | 410 / 498 | 302 / 366 | 78.0 / 17.7 |

Peak adapter usage: 4% / 58 MiB (10 clients), 9% / 104 MiB (25), 20% / 172 MiB (50), 32% / 312 MiB (100).

### Metadata cache off (worst case)

| Clients | Requests | Req/s | Errors | info | get 200K | get range | docGet | create PUT | create POST (2 comps) | create 8M | delete | MB/s up / down |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 10 | 1277 | 18.2 | 0.00% | 100 / 139 | 102 / 139 | 96 / 137 | 100 / 141 | 251 / 352 | 442 / 562 | 475 / 615 | 405 / 481 | 5.5 / 1.5 |
| 25 | 3166 | 47.2 | 0.00% | 98 / 139 | 103 / 140 | 109 / 139 | 105 / 140 | 218 / 281 | 410 / 508 | 412 / 487 | 405 / 476 | 17.7 / 3.7 |
| 50 | 6340 | 95.3 | 0.00% | 103 / 138 | 102 / 139 | 101 / 137 | 106 / 137 | 219 / 263 | 405 / 483 | 419 / 490 | 404 / 487 | 36.0 / 7.3 |
| 100 | 12828 | 191.6 | 0.00% | 100 / 137 | 101 / 137 | 103 / 137 | 102 / 138 | 216 / 265 | 401 / 481 | 409 / 494 | 403 / 479 | 64.9 / 15.3 |

Peak adapter usage: 5% / 71 MiB (10 clients), 12% / 119 MiB (25), 35% / 202 MiB (50), 32% / 348 MiB (100).

### What the content server numbers show

- **All levels passed with 0 errors**, and latency is flat from 10 to 100 clients. The cost of each operation is its number of sequential Graph round trips:
  - **Reads** (`info`, `get`, `docGet`) take one folder listing. The listing returns the component files, the sidecar and their download URLs. With a warm cache, a read costs only the SharePoint content fetch.
  - **create PUT** takes two rounds (about 216 ms). The existence check runs in parallel with the upload, then the sidecar is written.
  - **create POST** takes one extra round for the "document already exists" check that the spec requires before any component is stored.
  - **delete** takes a listing, the delete, and on a cold cache a sidecar read.
- **About the first data run:** the 0.2–0.8% "errors" there were exactly the 30 seed `create` calls being repeated at each level. They correctly returned 403 "document already exists", as the spec requires. The seed requests now mark 403 as expected; the tables above come from the corrected run.
- **Real SharePoint caveat:** in the emulator, reading the sidecar is free. Against real SharePoint, a cold read adds one SharePoint content fetch for the sidecar (tens of ms), and 8 MiB uploads are bounded by the VM's bandwidth.

## Reproduce

```bash
./loadtest/run.sh                                          # cache on  -> loadtest/results/REPORT.md
METADATA_CACHE_TTL=0s OUT=results/cache-off ./loadtest/run.sh
SCENARIO=contentserver.js OUT=results/contentserver ./loadtest/run.sh   # SAP Content Server interface
# knobs: LEVELS="10 25 50 100" DURATION=60s ADAPTER_CPUS=2 ADAPTER_MEM=1g MOCK_LATENCY_MS=60-140 MOCK_THROTTLE_RATE=0
```
