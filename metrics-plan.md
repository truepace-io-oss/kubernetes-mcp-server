# Prometheus metrics — Implementation Plan

> **Status:** In progress (this change implements it). Companion log: `metrics-process.md`.
> **Goal:** expose Prometheus metrics for the MCP: per-tool usage/latency/errors, agent-auth outcomes, per-cluster apiserver calls (free via client-go) + reachability, write-guard blocks, and Go/process runtime — served on a **separate, unauthenticated port** (not the public OIDC-gated `/mcp`, not the public ingress). Wire the chart's ServiceMonitor to it.

## Metrics
| Metric | Type | Labels | Source |
|---|---|---|---|
| `kmcp_tool_calls_total` | counter | `tool, cluster, result` (`ok\|error\|forbidden\|blocked`) | tool wrapper |
| `kmcp_tool_call_duration_seconds` | histogram | `tool, cluster` | tool wrapper |
| `kmcp_auth_requests_total` | counter | `method` (`static\|oidc\|none`), `result` (`allow\|deny`) | auth chain |
| `kmcp_cluster_up` | gauge | `cluster` | background prober (`Ping`) |
| `kmcp_writes_blocked_total` | counter | `cluster, reason` (`global_readonly\|cluster_readonly`) | write guard |
| `kmcp_build_info` | gauge=1 | `version, goversion` | main |
| `rest_client_requests_total` | counter | `host, code, method` | **client-go adapter** (free) |
| `rest_client_request_duration_seconds` | histogram | `host, verb` | **client-go adapter** (free) |
| `go_*`, `process_*` | — | — | default registry collectors (free) |

**Cardinality rules:** only bounded labels (`tool` ~12, `cluster` few, enums, status code, apiserver host). **Never** namespace/object-name/user.

## Design
- New `internal/metrics` package: defines the custom collectors (via `promauto` on the default registry, so Go+process come free), helpers (`RecordTool`, `RecordAuth`, `SetClusterUp`, `RecordWriteBlocked`, `SetBuildInfo`), a `RegisterClientGo()` that plugs Prometheus adapters into `k8s.io/client-go/tools/metrics`, and `StartClusterProbe(ctx, reg, interval)`.
- **Serving:** config `metricsAddr` (default `:9091`, empty = off). `main.go` starts a second `http.Server` serving `promhttp.Handler()` — **no auth middleware**.
- **Tool instrumentation:** a generic `addInstrumented[In]` wrapper around `mcp.AddTool` that times the call, extracts the resolved cluster (via a `metricCluster()` method on `clusterParam`, promoted to all input structs), classifies the result, and records.
- **Write guard:** `assertWritable` records `kmcp_writes_blocked_total` directly (precise reason).
- **Auth:** the verifier `chain` records `allow`/`deny` + method.
- **Chart:** add a `metrics` container port (9091) + Service port, render `metricsAddr` into `config.yaml`, and fix the ServiceMonitor to scrape the metrics port/path. Bump chart to `0.3.0`.

## Steps
1. `internal/config`: add `MetricsAddr` (default `:9091`, env `KMCP_METRICS_ADDR`).
2. `internal/metrics`: collectors + helpers + client-go adapter + cluster probe + build info. (promote `prometheus/client_golang` to a direct dep.)
3. `internal/mcpserver`: `addInstrumented` wrapper; `metricCluster()`; record write-guard blocks.
4. `internal/auth`: record auth allow/deny in `chain`.
5. `main.go`: `metrics.RegisterClientGo()`, `SetBuildInfo`, start metrics server + cluster probe.
6. Unit tests: scrape `promhttp` and assert counters move (tool ok/blocked, auth deny, build_info).
7. Chart: metrics port + Service + config + ServiceMonitor fix; `values.metrics.{enabled,port}`; bump to `0.3.0`.
8. Docs: `docs/metrics.md` + README section. Run `make test` + `make test-e2e` + `helm template`.

**Acceptance:** `curl :9091/metrics` shows `kmcp_*`, `rest_client_*`, `go_*`; a tool call increments `kmcp_tool_calls_total`; a blocked write increments `kmcp_writes_blocked_total`; ServiceMonitor scrapes the right port; all tests green.
