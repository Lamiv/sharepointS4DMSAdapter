#!/usr/bin/env bash
# Runs the Document REST API load test at 10, 25, 50 and 100 concurrent
# clients against the Graph emulator and writes loadtest/results/REPORT.md.
#
#   LEVELS="10 25 50 100" DURATION=60s ADAPTER_CPUS=2 ADAPTER_MEM=1g ./loadtest/run.sh
#   SCENARIO=contentserver.js OUT=results/contentserver ./loadtest/run.sh   # SAP Content Server interface
set -euo pipefail
cd "$(dirname "$0")"
export MSYS_NO_PATHCONV=1
LEVELS="${LEVELS:-10 25 50 100}"
DURATION="${DURATION:-60s}"
SCENARIO="${SCENARIO:-scenario.js}"
PROJECT=spadapter-lt
compose() { docker compose -p "$PROJECT" -f docker-compose.yml "$@"; }

OUT="${OUT:-results}"
mkdir -p results "$OUT"
rm -f results/row-*.md results/summary-*.json results/stats-*.txt results/header.md

compose up -d --build graphmock adapter
trap 'compose down -v >/dev/null 2>&1 || true' EXIT

for _ in $(seq 1 60); do
  curl -fs http://localhost:9090/readyz >/dev/null 2>&1 && break
  sleep 1
done
curl -fs http://localhost:9090/readyz >/dev/null || { echo "adapter not ready"; compose logs adapter; exit 1; }

ADAPTER_CTR=$(compose ps -q adapter)
for vus in $LEVELS; do
  echo "=== $vus concurrent clients for $DURATION ==="
  ( while true; do docker stats --no-stream --format '{{.CPUPerc}} {{.MemUsage}}' "$ADAPTER_CTR" 2>/dev/null; sleep 2; done ) > "results/stats-$vus.txt" &
  STATS_PID=$!
  compose run --rm -e VUS="$vus" -e DURATION="$DURATION" k6 run --quiet "$SCENARIO" || echo "!! k6 thresholds failed at $vus clients"
  kill "$STATS_PID" 2>/dev/null || true
  wait "$STATS_PID" 2>/dev/null || true
  sleep 3
done

curl -fs http://localhost:9090/metrics | grep -E '^adapter_(graph_retries_total|http_rejected_total|transfer_buffer_wait_seconds_count|graph_concurrency_wait_seconds_count|token_refresh_total)' > results/adapter-metrics.txt || true

{
  if [ "$SCENARIO" = contentserver.js ]; then
    echo "# Load test report: SAP Content Server HTTP interface"
  else
    echo "# Load test report: Document REST API"
  fi
  echo
  echo "- Date: $(date -u +'%Y-%m-%d %H:%M UTC')"
  echo "- Duration per level: $DURATION, constant concurrent clients (k6 VUs), 0.1-0.5 s think time"
  echo "- Adapter container limits: ${ADAPTER_CPUS:-2} CPU, ${ADAPTER_MEM:-1g} RAM"
  echo "- Metadata cache TTL: ${METADATA_CACHE_TTL:-60s} (0s = every read goes to Graph)"
  echo "- Graph emulator latency: ${MOCK_LATENCY_MS:-60-140} ms per API call, throttle rate ${MOCK_THROTTLE_RATE:-0}"
  if [ "$SCENARIO" = contentserver.js ]; then
    echo "- Mix: 25% info, 30% get 200 KiB, 5% get 50 KB range, 5% docGet, 18% create PUT (80% 200 KiB / 20% 1 MiB), 8% create POST multipart (2 components), 3% create 8 MiB (upload session), 6% delete"
    echo "- Signature mode: none (k6 cannot create SAP PKCS#7 secKeys; verification costs ~0.26 ms DSA / ~0.035 ms RSA per request, see Go benchmarks)"
  else
    echo "- Mix: 35% download 200 KiB, 5% download 12 MiB, 20% metadata, 10% list, 20% upload 200 KiB, 7% upload 1 MiB, 3% upload 12 MiB (chunked session)"
  fi
  echo
  echo "Latency columns: median / p95 in ms (end-to-end at the client)."
  echo
  if [ -f results/header.md ]; then cat results/header.md; else
    echo "| Clients | Requests | Req/s | Errors | Download 200K | Download 12M | Metadata | List | Upload 200K | Upload 1M | Upload 12M | MB/s up / down |"
    echo "|---|---|---|---|---|---|---|---|---|---|---|---|"
  fi
  for vus in $LEVELS; do cat "results/row-$vus.md" 2>/dev/null || echo "| $vus | run failed | | | | | | | | | | |"; done
  echo
  echo "## Adapter container resources (peak of 2 s samples)"
  echo
  echo "| Clients | Peak CPU (100% = 1 core) | Peak memory |"
  echo "|---|---|---|"
  for vus in $LEVELS; do
    f="results/stats-$vus.txt"
    cpu=$(awk '{gsub("%","",$1); if ($1+0>m) m=$1+0} END {printf "%.0f%%", m}' "$f" 2>/dev/null || echo "-")
    mem=$(awk '{print $2}' "$f" 2>/dev/null | sort -h | tail -1)
    echo "| $vus | $cpu | ${mem:--} |"
  done
  echo
  echo "## Adapter counters after the run"
  echo
  echo '```'
  cat results/adapter-metrics.txt 2>/dev/null
  echo '```'
} > "$OUT/REPORT.md"
[ "$OUT" != results ] && cp results/summary-*.json results/stats-*.txt results/adapter-metrics.txt "$OUT"/ 2>/dev/null || true
cat "$OUT/REPORT.md"
