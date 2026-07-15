# Prometheus metrics — Process Log

Companion to [`metrics-plan.md`](./metrics-plan.md). **Status: ✅ implemented & tested.**

## Summary
Added Prometheus metrics on a separate unauthenticated port (`:9091`), wired into tools, auth, the write-guard, a cluster probe, and client-go; chart exposes the port + fixes the ServiceMonitor. Chart bumped to **0.3.0** (needs a `v0.3.0` tag to publish image+chart).

## What was done
- **`internal/config`**: `MetricsAddr` field (default `:9091`, env `KMCP_METRICS_ADDR`, `"off"` disables).
- **`internal/metrics`** (new): collectors via `promauto` on the default registry (so `go_*`/`process_*` come free): `kmcp_tool_calls_total`, `kmcp_tool_call_duration_seconds`, `kmcp_auth_requests_total`, `kmcp_cluster_up`, `kmcp_writes_blocked_total`, `kmcp_build_info`, plus client-go adapters (`rest_client_requests_total`, `rest_client_request_duration_seconds`) registered via `k8s.io/client-go/tools/metrics`. Helpers: `RecordTool/RecordAuth/RecordWriteBlocked/SetClusterUp/SetBuildInfo/RegisterClientGo`. Promoted `prometheus/client_golang` to a direct dep.
- **`internal/mcpserver`**: generic `addTool` wrapper times + classifies (`ok|error|forbidden|blocked`) + records every tool; `clusterParam.metricCluster()` (promoted to all inputs); write-guard records `kmcp_writes_blocked_total`.
- **`internal/auth`**: verifier `chain` records `kmcp_auth_requests_total{method,result}` (allow/deny).
- **`main.go`**: `RegisterClientGo()` + `SetBuildInfo()` before building clients; a second `http.Server` serves `promhttp.Handler()` on `metricsAddr` (no auth), plus a 30s `probeClusters` goroutine for `kmcp_cluster_up`; graceful shutdown of both.
- **Chart**: `metrics.{enabled,port}` values; `metricsAddr` rendered into `config.yaml`; `metrics` container + Service port; ServiceMonitor fixed to `port: metrics` `path: /metrics` and gated on `metrics.enabled`; **Chart 0.3.0**.
- **Grafana dashboard**: `deploy/helm/kubernetes-mcp/dashboards/kubernetes-mcp.json` (9 panels) rendered as a sidecar-discovered ConfigMap via `templates/grafana-dashboard.yaml`, gated by `grafanaDashboard.enabled` (label `grafana_dashboard: "1"`, optional folder/namespace). Validated: embedded JSON parses; label present.
- **Docs**: `docs/metrics.md` (incl. dashboard), README "Metrics" section, chart README, `examples/config.yaml` `metricsAddr`.

## Validation (run)
    go vet ./...                → clean ; gofmt clean
    go test ./internal/... -race → auth/clusters/config/mcpserver/metrics all ok
    KUBEBUILDER_ASSETS=… go test ./test/e2e/... → ok (5.65s)
    helm lint                   → 0 failed
    helm template (sm on)       → metricsAddr ":9091", metrics container/Service port, ServiceMonitor port=metrics path=/metrics ; valid YAML
    helm template metrics.enabled=false → metricsAddr "off", no metrics port / ServiceMonitor
    runtime smoke: curl :9091/metrics → kmcp_build_info, kmcp_cluster_up, rest_client_*, go_*, process_* present

## Open items
- Release **`v0.3.0`** (tag) so the image + chart publish; then bump the `environments` vendir/image to `0.3.0` and, once metrics are wanted there, set `serviceMonitor.enabled: true` (VictoriaMetrics/Prometheus operator must be present — it is in prod-averion-tools).
- The distroless `runAsUser` fix (0.2.x) is already in the chart values; 0.3.0 carries it, so the `environments` pod-security override can be dropped after bumping.
