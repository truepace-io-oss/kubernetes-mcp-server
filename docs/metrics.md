# Metrics

The MCP exposes Prometheus metrics on a **separate, unauthenticated port**
(default `:9091`, `metricsAddr` / `KMCP_METRICS_ADDR`; set `off` to disable).
It is deliberately **not** behind the `/mcp` OIDC auth and must **not** be routed
through the public ingress — only an in-cluster scraper (ServiceMonitor) should
reach it. Metric labels contain no secrets, namespaces, object names or user
identities.

```bash
curl http://localhost:9091/metrics
```

## What's exposed

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `kmcp_tool_calls_total` | counter | `tool, mcp_cluster, result` | MCP tool calls. `result` = `ok\|error\|forbidden\|blocked` (`forbidden` = Kubernetes 403, `blocked` = read-only guard). |
| `kmcp_tool_call_duration_seconds` | histogram | `tool, mcp_cluster` | Tool latency. |
| `kmcp_auth_requests_total` | counter | `method, result` | Agent auth. `method` = `static\|oidc\|none`, `result` = `allow\|deny` (denies = 401s). |
| `kmcp_cluster_up` | gauge | `mcp_cluster` | Apiserver reachability (1/0), from a 30s background probe. |
| `kmcp_writes_blocked_total` | counter | `mcp_cluster, reason` | Mutations blocked by the guard (`global_readonly\|cluster_readonly`). |
| `kmcp_build_info` | gauge=1 | `version, goversion` | Build info. |
| `rest_client_requests_total` | counter | `host, code, method` | Kubernetes API calls per cluster **with status codes** (client-go). |
| `rest_client_request_duration_seconds` | histogram | `host, verb` | Apiserver latency per cluster (client-go). |
| `go_*`, `process_*` | — | — | Go runtime & process (standard collectors). |

Counters with label sets only appear after the first sample (e.g.
`kmcp_tool_calls_total` shows up once a tool is called).

## Scraping (Helm)

Metrics are on by default. Enable the `ServiceMonitor` to scrape them:
```yaml
metrics:
  enabled: true
  port: 9091
serviceMonitor:
  enabled: true
```
The chart adds a `metrics` container/Service port and points the ServiceMonitor
at it (`port: metrics`, `path: /metrics`). Set `metrics.enabled: false` to drop
the metrics port entirely (renders `metricsAddr: "off"`).

## Grafana dashboard

The chart ships a dashboard (`deploy/helm/kubernetes-mcp/dashboards/kubernetes-mcp.json`,
9 panels: build-info, cluster-up, auth, tool calls by tool/result, tool latency
p95, writes-blocked, apiserver requests-by-code/latency). The call-count panels
show **totals over the dashboard's selected time range** (`increase(...[$__range])`)
rather than req/s, and the p95 panels are computed over `$__range` too — so they
stay populated for bursty, low-frequency interactive usage (a handful of tool
calls, then idle). Widen the time picker (top-right) to see more history. Enable
it as a Grafana-sidecar-discovered ConfigMap:
```yaml
grafanaDashboard:
  enabled: true
  # label/labelValue default to grafana_dashboard: "1" (kube-prometheus-stack /
  # victoria-metrics-k8s-stack sidecar). folder: "" sets an optional Grafana folder.
```
Requires the Grafana dashboard sidecar. The dashboard uses a `datasource`
template variable, so it binds to whatever Prometheus/VictoriaMetrics datasource
you pick.

## Useful queries
- Tool error rate: `sum(rate(kmcp_tool_calls_total{result!="ok"}[5m])) by (tool)`
- RBAC denials: `sum(rate(kmcp_tool_calls_total{result="forbidden"}[5m])) by (mcp_cluster)`
- Auth 401s: `sum(rate(kmcp_auth_requests_total{result="deny"}[5m]))`
- Apiserver 5xx per cluster: `sum(rate(rest_client_requests_total{code=~"5.."}[5m])) by (host)`
- Cluster down: `kmcp_cluster_up == 0`
