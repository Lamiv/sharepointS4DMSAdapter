# Monitoring

ZAKS Drive exposes health endpoints, Prometheus metrics and JSON logs. All of them are on the admin port (9090, bound to `127.0.0.1` in the Compose file).

## Health

| Endpoint | Meaning |
|---|---|
| `GET /healthz` | The process is up. Used by the Docker health check |
| `GET /readyz` | `ready`, or `503` with the reason (for example the Graph error or the permissions the token carries). Not ready while a repository cannot be resolved |
| `GET /version` | The version string |

## Metrics

`GET /metrics` returns Prometheus text. The adapter's own series:

| Metric | Use |
|---|---|
| `adapter_http_requests_total{iface,route,method,code}` | Request rate and errors per interface and route |
| `adapter_http_request_duration_seconds` | Latency |
| `adapter_http_in_flight_requests`, `adapter_http_rejected_total` | Load and requests refused at `maxInFlight` |
| `adapter_bytes_total` | Bytes transferred |
| `adapter_graph_requests_total`, `adapter_graph_request_duration_seconds` | Calls to Microsoft Graph |
| `adapter_graph_retries_total{reason}` | Retries; `reason="throttled"` means Microsoft is throttling |
| `adapter_graph_concurrency_wait_seconds` | Time waiting for a free Graph slot (`graph.maxConcurrency` reached) |
| `adapter_token_refresh_total` | Token refreshes, including failures |
| `adapter_transfer_buffer_wait_seconds`, `adapter_transfers_active` | Memory budget pressure and active transfers |

Go runtime and process metrics are included. Check names against your running version with `curl -s localhost:9090/metrics | grep ^adapter_`.

## Suggested alerts

These are suggestions, not shipped rules:

- `/readyz` not `ready` for more than a few minutes.
- A sustained rise of `adapter_graph_retries_total{reason="throttled"}`.
- A non-zero rate of 5xx in `adapter_http_requests_total`, split by `iface`.
- Any increase in `adapter_http_rejected_total` (the server is saturated).
- Token refresh failures, which usually mean an expired client secret.

## Logs

One JSON line per request with `iface`, `request_id`, `method`, `route`, `path`, `status`, `duration`, `remote`. SAP Content Server routes are `cs:<command>`; refused requests also log the error code and reason. The `request_id` is the `X-Request-ID` header, forwarded to Microsoft as `client-request-id`, so a slow or failed call can be traced on both sides.

```bash
docker compose -f deploy/docker-compose.yml logs -f adapter
docker compose -f deploy/docker-compose.yml logs --since 15m adapter | grep 'request refused'
```

`log.level` or `LOG_LEVEL=debug` shows what SAP sends (secKey values are redacted). Debug logging is verbose; return to `info` afterwards.
